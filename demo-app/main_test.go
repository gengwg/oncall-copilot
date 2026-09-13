package main

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// chaos runs one /chaos?mode=<mode> request against the real handler.
func chaos(t *testing.T, mode string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handleChaos(w, httptest.NewRequest(http.MethodGet, "/chaos?mode="+mode, nil))
	return w
}

func stopChaos(t *testing.T) {
	t.Helper()
	handleChaosStop(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/chaos/stop", nil))
}

// shrinkBallast keeps the memory tests to a few MB instead of 50MB a hit.
func shrinkBallast(t *testing.T) {
	t.Helper()
	orig := ballastChunk
	ballastChunk = 1 << 20
	t.Cleanup(func() { ballastChunk = orig })
}

func ballastLen() int {
	chaosMu.Lock()
	defer chaosMu.Unlock()
	return len(memBallast)
}

// The leak scenario only works if repeated hits retain every chunk: the alert
// and the heap_trend evidence both look for a rising heap.
func TestMemoryChaosAccumulates(t *testing.T) {
	shrinkBallast(t)
	stopChaos(t)

	for i := 1; i <= 3; i++ {
		if w := chaos(t, "memory"); w.Code != http.StatusOK {
			t.Fatalf("hit %d: got status %d, want 200", i, w.Code)
		}
		if got := ballastLen(); got != i {
			t.Fatalf("after %d hits: holding %d chunks, want %d (chunks are being replaced, not retained)", i, got, i)
		}
	}

	stopChaos(t)
	if got := ballastLen(); got != 0 {
		t.Fatalf("after /chaos/stop: holding %d chunks, want 0", got)
	}
}

// Two concurrent cpu-chaos requests, or one racing /chaos/stop, must not
// double-close cpuStop. Before the mutex this panicked the process.
func TestCPUChaosConcurrentStopNoPanic(t *testing.T) {
	stopChaos(t)
	t.Cleanup(func() { stopChaos(t) })

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() { defer wg.Done(); chaos(t, "cpu") }()
		go func() { defer wg.Done(); stopChaos(t) }()
	}
	wg.Wait()
}

// Burners must actually exit when chaos is cleared, or every cpu run leaks
// NumCPU goroutines that keep the box pegged.
func TestCPUChaosBurnersStop(t *testing.T) {
	stopChaos(t)
	chaos(t, "cpu")

	chaosMu.Lock()
	stop := cpuStop
	chaosMu.Unlock()
	if stop == nil {
		t.Fatal("cpuStop is nil after enabling cpu chaos")
	}

	stopChaos(t)
	select {
	case <-stop:
	default:
		t.Fatal("stop channel still open after /chaos/stop; burners would run forever")
	}

	chaosMu.Lock()
	cleared := cpuStop
	chaosMu.Unlock()
	if cleared != nil {
		t.Fatal("cpuStop not reset to nil after /chaos/stop")
	}
}

// Re-enabling cpu chaos must release the previous generation of burners.
func TestCPUChaosRestartReleasesPrevious(t *testing.T) {
	stopChaos(t)
	t.Cleanup(func() { stopChaos(t) })

	chaos(t, "cpu")
	chaosMu.Lock()
	first := cpuStop
	chaosMu.Unlock()

	chaos(t, "cpu")
	select {
	case <-first:
	default:
		t.Fatal("first generation of burners was orphaned by the second /chaos?mode=cpu")
	}
}

// /chaos/stop must drop the heap immediately, not wait for the next forced GC
// ~2min later, or the leak alert idles above its threshold after recovery.
func TestChaosStopReleasesHeapImmediately(t *testing.T) {
	shrinkBallast(t)
	ballastChunk = 32 << 20 // large enough to read clearly above baseline
	stopChaos(t)

	var ms runtime.MemStats
	for range 3 {
		chaos(t, "memory")
	}
	runtime.ReadMemStats(&ms)
	grown := ms.HeapAlloc

	stopChaos(t)
	runtime.ReadMemStats(&ms)
	if ms.HeapAlloc >= grown {
		t.Fatalf("heap is %d bytes after /chaos/stop, was %d before; the ballast was not collected", ms.HeapAlloc, grown)
	}
}

func TestChaosRejectsUnknownMode(t *testing.T) {
	w := chaos(t, "banana")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", w.Code)
	}
}

func TestErrorChaosFailsRequestsAndCounts(t *testing.T) {
	stopChaos(t)
	t.Cleanup(func() { stopChaos(t) })

	before := errTotal.Load()
	chaos(t, "error")

	var failed int
	for range 200 {
		w := httptest.NewRecorder()
		handleWork(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code == http.StatusBadGateway {
			failed++
		}
	}
	if failed == 0 {
		t.Fatal("error chaos enabled but no request failed in 200 tries")
	}
	if got := errTotal.Load() - before; int(got) != failed {
		t.Fatalf("errTotal rose by %d, want %d", got, failed)
	}

	stopChaos(t)
	for range 50 {
		w := httptest.NewRecorder()
		handleWork(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("after /chaos/stop: got status %d, want 200", w.Code)
		}
	}
}

// investigate.sh reads these exact metric names; renaming one silently breaks
// every piece of evidence in the incident report.
func TestMetricsExposesEvidenceSeries(t *testing.T) {
	w := httptest.NewRecorder()
	handleMetrics(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, m := range []string{
		"demo_requests_total",
		"demo_errors_total",
		"demo_latency_mode",
		"demo_error_mode",
		"demo_heap_alloc_bytes",
	} {
		if !strings.Contains(body, "\n"+m+" ") && !strings.HasPrefix(body, m+" ") {
			t.Errorf("/metrics is missing a value line for %s", m)
		}
	}
}

func TestMetricsLatencyModeReflectsChaos(t *testing.T) {
	stopChaos(t)
	t.Cleanup(func() { stopChaos(t) })

	read := func() string {
		w := httptest.NewRecorder()
		handleMetrics(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		for line := range strings.SplitSeq(w.Body.String(), "\n") {
			if v, ok := strings.CutPrefix(line, "demo_latency_mode "); ok {
				return v
			}
		}
		return ""
	}

	if got := read(); got != "0" {
		t.Fatalf("demo_latency_mode = %q with chaos off, want 0", got)
	}
	chaos(t, "latency")
	if got := read(); got != "1" {
		t.Fatalf("demo_latency_mode = %q with latency chaos on, want 1", got)
	}
	stopChaos(t)
	if got := read(); got != "0" {
		t.Fatalf("demo_latency_mode = %q after /chaos/stop, want 0", got)
	}
}
