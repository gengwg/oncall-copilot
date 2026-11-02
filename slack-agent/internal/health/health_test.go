package health

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 2, 16, 5, 0, 0, time.UTC)

func ts(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339) }

// state writes last-ok (skipped when empty) and health.log (skipped when nil).
func state(t *testing.T, lastOK string, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	if lastOK != "" {
		if err := os.WriteFile(filepath.Join(dir, "last-ok"), []byte(lastOK+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if lines != nil {
		if err := os.WriteFile(filepath.Join(dir, "health.log"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func line(ago time.Duration, kind, detail string) string {
	return ts(ago) + "\t" + kind + "\t" + detail
}

func TestMissingLastOKIsUnknown(t *testing.T) {
	got := Poller(t.TempDir(), now)
	if !strings.HasPrefix(got, "poller health unknown: ") || !strings.Contains(got, "last-ok") {
		t.Fatalf("got %q", got)
	}
	got = Poller(filepath.Join(t.TempDir(), "nope"), now)
	if !strings.HasPrefix(got, "poller health unknown: ") {
		t.Fatalf("missing dir: %q", got)
	}
}

func TestGarbledLastOKIsUnknown(t *testing.T) {
	got := Poller(state(t, "yesterday-ish", nil), now)
	if !strings.HasPrefix(got, "poller health unknown: ") {
		t.Fatalf("got %q", got)
	}
}

func TestFreshLastOKAndNoLogIsQuiet(t *testing.T) {
	if got := Poller(state(t, ts(time.Minute), nil), now); got != "" {
		t.Fatalf("no log: %q", got)
	}
	if got := Poller(state(t, ts(time.Minute), []string{}), now); got != "" {
		t.Fatalf("empty log: %q", got)
	}
}

func TestStaleLastOK(t *testing.T) {
	old := ts(11 * time.Minute)
	got := Poller(state(t, old, nil), now)
	if got != "poller: no successful run since "+old {
		t.Fatalf("got %q", got)
	}
	if got := Poller(state(t, ts(10*time.Minute), nil), now); got != "" {
		t.Fatalf("exactly 10 min is still fresh: %q", got)
	}
}

func TestCountsByKindInsideTheWindowOnly(t *testing.T) {
	d := state(t, ts(time.Minute), []string{
		line(30*time.Hour, "poll-failed", "HTTP 500"), // outside
		line(23*time.Hour, "poll-failed", "HTTP 502"),
		line(5*time.Hour, "poll-failed", "HTTP 401"),
		line(4*time.Hour, "poll-failed", "HTTP 401"),
		line(3*time.Hour, "ping-failed", "curl 7"),
	})
	want := "poller: 3 failed polls, 1 failed ping in the last 24h (last: " + ts(3*time.Hour) + " ping-failed curl 7)"
	if got := Poller(d, now); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestSingularAndPlural(t *testing.T) {
	d := state(t, ts(time.Minute), []string{
		line(2*time.Hour, "poll-failed", "HTTP 401"),
		line(time.Hour, "held", "degraded"),
	})
	got := Poller(d, now)
	if !strings.Contains(got, "1 failed poll,") || !strings.Contains(got, "heartbeat held for 1 run in") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "1 failed polls") || strings.Contains(got, "1 runs") {
		t.Fatalf("plural of one: %q", got)
	}
}

func TestSeparateGapIsCountedWithLongest(t *testing.T) {
	d := state(t, ts(time.Minute), []string{
		line(10*time.Hour, "gap", "20 min since "+ts(10*time.Hour+20*time.Minute)),
		line(2*time.Hour, "gap", "62 min since "+ts(3*time.Hour+2*time.Minute)),
	})
	got := Poller(d, now)
	want := "poller: 2 gaps (longest 62 min) in the last 24h (last: " + ts(2*time.Hour) + " gap 62 min since " + ts(3*time.Hour+2*time.Minute) + ")"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	one := Poller(state(t, ts(time.Minute), []string{line(2*time.Hour, "gap", "62 min since "+ts(3*time.Hour+2*time.Minute))}), now)
	if !strings.HasPrefix(one, "poller: 1 gap (longest 62 min) in") {
		t.Fatalf("one: %q", one)
	}
}

// The first healthy run after a hold writes a gap covering it; that gap is the
// hold, not a second outage.
func TestGapCoveringAHoldIsTheHold(t *testing.T) {
	since := 3*time.Hour + 2*time.Minute
	var lines []string
	for i := 0; i < 14; i++ {
		lines = append(lines, line(3*time.Hour-time.Duration(i)*time.Minute, "held", "degraded"))
	}
	lines = append(lines, line(2*time.Hour+45*time.Minute, "gap", "62 min since "+ts(since)))
	got := Poller(state(t, ts(time.Minute), lines), now)
	if !strings.Contains(got, "heartbeat held for 14 runs (about 62 min)") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "gap") && !strings.Contains(got, "(last:") {
		t.Fatalf("hold's gap must not be counted: %q", got)
	}
	if strings.Contains(got, "1 gap") {
		t.Fatalf("hold's gap counted as outage: %q", got)
	}
}

func TestHoldStillGoingHasNoDuration(t *testing.T) {
	d := state(t, ts(time.Minute), []string{line(2*time.Minute, "held", "x"), line(time.Minute, "held", "x")})
	got := Poller(d, now)
	if !strings.Contains(got, "heartbeat held for 2 runs in the last 24h") {
		t.Fatalf("got %q", got)
	}
}

func TestGarbledAndFutureLinesAreIgnored(t *testing.T) {
	d := state(t, ts(time.Minute), []string{
		"not a log line",
		"notatime\tpoll-failed\tHTTP 500",
		now.Add(time.Hour).Format(time.RFC3339) + "\tpoll-failed\tfuture",
		ts(time.Hour) + "\tbogus-kind\tx",
		ts(time.Hour) + "\tgap\tno length here",
		"",
	})
	got := Poller(d, now)
	// The one parseable gap has no usable length, so it counts without one.
	if strings.Contains(got, "failed poll") || strings.Contains(got, "future") {
		t.Fatalf("ignored lines counted: %q", got)
	}
}

func TestUnreadableLogIsUnknown(t *testing.T) {
	dir := state(t, ts(time.Minute), nil)
	if err := os.Mkdir(filepath.Join(dir, "health.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Poller(dir, now); !strings.HasPrefix(got, "poller health unknown: ") {
		t.Fatalf("got %q", got)
	}
}

func TestStaleAndEventsBothReported(t *testing.T) {
	old := ts(2 * time.Hour)
	d := state(t, old, []string{line(3*time.Hour, "poll-failed", "HTTP 401")})
	got := Poller(d, now)
	parts := strings.Split(got, "\n")
	if len(parts) != 2 || parts[0] != "poller: no successful run since "+old || !strings.HasPrefix(parts[1], "poller: 1 failed poll in the last 24h") {
		t.Fatalf("got %q", got)
	}
}
