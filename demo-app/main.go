// demo-app is a small HTTP service with a /chaos endpoint for injecting
// failures (latency, errors, memory growth, CPU burn). It exposes Prometheus
// metrics and writes structured JSON logs, giving the on-call copilot a
// realistic incident target.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	addr = flag.String("addr", ":8080", "listen address")

	reqTotal    atomic.Int64
	errTotal    atomic.Int64
	latencyMode atomic.Bool
	errorMode   atomic.Bool

	// chaosMu guards the chaos state that is not a simple atomic. Without it,
	// two concurrent /chaos calls can both pass the cpuStop nil check and
	// double-close the channel, panicking the process.
	chaosMu    sync.Mutex
	memBallast [][]byte      // retained chunks simulating a leak; guarded by chaosMu
	cpuStop    chan struct{} // nil when no burners are running; guarded by chaosMu

	// ballastChunk is the per-hit allocation, var so tests can shrink it.
	ballastChunk = 50 << 20
)

func main() {
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleWork)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/metrics", handleMetrics)
	mux.HandleFunc("/chaos", handleChaos)
	mux.HandleFunc("/chaos/stop", handleChaosStop)

	slog.Info("demo-app listening", "addr", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}

// handleWork simulates a normal request: some latency, occasional work.
func handleWork(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqTotal.Add(1)

	if latencyMode.Load() {
		d := time.Duration(800+rand.IntN(1200)) * time.Millisecond
		time.Sleep(d)
		slog.Warn("slow request", "path", r.URL.Path, "injected_latency_ms", d.Milliseconds())
	} else {
		time.Sleep(time.Duration(rand.IntN(50)) * time.Millisecond)
	}

	if errorMode.Load() && rand.Float64() < 0.5 {
		errTotal.Add(1)
		slog.Error("request failed", "path", r.URL.Path, "err", "upstream dial timeout", "status", 502)
		http.Error(w, `{"error":"upstream dial timeout"}`, http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"took_ms":%d}`, time.Since(start).Milliseconds())
}

// handleChaos enables a failure mode: /chaos?mode=latency|error|memory|cpu
func handleChaos(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	switch mode {
	case "latency":
		latencyMode.Store(true)
	case "error":
		errorMode.Store(true)
	case "memory":
		// Append, don't replace: replacing drops the previous chunk on the floor
		// and heap stays flat, so the leak alert never sees a rising trend.
		b := make([]byte, ballastChunk)
		for i := range b {
			b[i] = byte(i)
		}
		chaosMu.Lock()
		memBallast = append(memBallast, b)
		heldMB := len(memBallast) * ballastChunk >> 20
		chaosMu.Unlock()
		slog.Warn("memory ballast allocated", "mb", ballastChunk>>20, "held_mb", heldMB)
	case "cpu":
		// Stop any existing burners first so re-enabling doesn't orphan them.
		chaosMu.Lock()
		if cpuStop != nil {
			close(cpuStop)
		}
		stop := make(chan struct{})
		cpuStop = stop
		chaosMu.Unlock()
		for i := 0; i < runtime.NumCPU(); i++ {
			go func() {
				for {
					select {
					case <-stop:
						return
					default:
						runtime.Gosched()
					}
				}
			}()
		}
	default:
		http.Error(w, `{"error":"mode must be latency|error|memory|cpu"}`, http.StatusBadRequest)
		return
	}
	slog.Info("chaos enabled", "mode", mode)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"chaos":%q}`, mode)
}

func handleChaosStop(w http.ResponseWriter, r *http.Request) {
	latencyMode.Store(false)
	errorMode.Store(false)
	chaosMu.Lock()
	memBallast = nil
	if cpuStop != nil {
		close(cpuStop)
		cpuStop = nil
	}
	chaosMu.Unlock()
	// Dropping the reference does not lower HeapAlloc until a GC cycle runs, and
	// the next forced one can be ~2min out. Collect now so the leak alert
	// resolves promptly instead of idling above the threshold.
	runtime.GC()
	slog.Info("chaos cleared")
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"chaos":"cleared"}`))
}

// handleMetrics emits Prometheus text format without a client dependency.
func handleMetrics(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, `# HELP demo_requests_total Total requests.
# TYPE demo_requests_total counter
demo_requests_total %d
# HELP demo_errors_total Total failed requests.
# TYPE demo_errors_total counter
demo_errors_total %d
# HELP demo_latency_mode Latency chaos active (1) or not (0).
# TYPE demo_latency_mode gauge
demo_latency_mode %s
# HELP demo_error_mode Error chaos active (1) or not (0).
# TYPE demo_error_mode gauge
demo_error_mode %s
# HELP demo_heap_alloc_bytes Current heap allocation.
# TYPE demo_heap_alloc_bytes gauge
demo_heap_alloc_bytes %d
`, reqTotal.Load(), errTotal.Load(),
		strconv.Itoa(btoi(latencyMode.Load())), strconv.Itoa(btoi(errorMode.Load())),
		ms.HeapAlloc)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
