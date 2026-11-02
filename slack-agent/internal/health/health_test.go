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
	if got != "poller: no healthy run since "+old {
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

// A token expiry or failed pings end the same way as a hold: the first good
// run back writes a gap over the whole stop.
func TestGapCoveringFailedPollsOrPingsIsThatProblem(t *testing.T) {
	for _, tc := range []struct{ kind, detail, want string }{
		{"poll-failed", "HTTP 401", "poller: 3 failed polls in the last 24h (last: "},
		{"ping-failed", "curl exit 28", "poller: 3 failed pings in the last 24h (last: "},
	} {
		lines := []string{
			line(3*time.Hour, tc.kind, tc.detail),
			line(3*time.Hour-time.Minute, tc.kind, tc.detail),
			line(3*time.Hour-2*time.Minute, tc.kind, tc.detail),
			line(2*time.Hour+56*time.Minute, "gap", "9 min since "+ts(3*time.Hour+5*time.Minute)),
		}
		got := Poller(state(t, ts(time.Minute), lines), now)
		if !strings.HasPrefix(got, tc.want) || strings.Contains(got, "gap") {
			t.Fatalf("%s: got %q", tc.kind, got)
		}
	}
}

// Problem lines outside a gap's span do not explain it: nothing reported while
// the poller was stopped.
func TestGapWithNoProblemLineInItsSpanIsAGap(t *testing.T) {
	lines := []string{
		line(8*time.Hour, "poll-failed", "HTTP 401"),
		line(5*time.Hour, "ping-failed", "curl exit 28"),
		line(2*time.Hour, "gap", "62 min since "+ts(3*time.Hour+2*time.Minute)),
	}
	got := Poller(state(t, ts(time.Minute), lines), now)
	want := "poller: 1 failed poll, 1 failed ping, 1 gap (longest 62 min) in the last 24h (last: " + ts(2*time.Hour) + " gap 62 min since " + ts(3*time.Hour+2*time.Minute) + ")"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// A problem line explains a gap only while it keeps the span covered: a stop
// that begins after the last problem line is a real stop.
func TestStopAfterProblemLinesIsStillAGap(t *testing.T) {
	stop := func(n int, kind string) []string {
		var l []string
		for i := 0; i < n; i++ {
			l = append(l, line(4*time.Hour-time.Duration(i)*time.Minute, kind, "x"))
		}
		// last problem line at 4h-(n-1)min; the stop runs until 59 min ago.
		since := ts(4*time.Hour - time.Duration(n-1)*time.Minute)
		return append(l, line(time.Hour, "gap", "181 min since "+since))
	}
	for _, tc := range []struct {
		name, kind string
		n          int
		want       string
	}{
		{"one poll-failed", "poll-failed", 1, "poller: 1 failed poll, 1 gap (longest 181 min) in"},
		{"one ping-failed", "ping-failed", 1, "poller: 1 failed ping, 1 gap (longest 181 min) in"},
		{"ten minutes of token expiry", "poll-failed", 10, "poller: 10 failed polls, 1 gap (longest 181 min) in"},
		{"one held", "held", 1, "poller: heartbeat held for 1 run, 1 gap (longest 181 min) in"},
	} {
		got := Poller(state(t, ts(time.Minute), stop(tc.n, tc.kind)), now)
		if !strings.HasPrefix(got, tc.want) {
			t.Fatalf("%s: got %q", tc.name, got)
		}
	}
}

func TestGapCoverageBoundaryIs600Seconds(t *testing.T) {
	for _, tc := range []struct {
		uncovered time.Duration
		gap       bool
	}{{600 * time.Second, false}, {601 * time.Second, true}} {
		end := 3 * time.Hour
		lines := []string{
			line(end+tc.uncovered, "poll-failed", "x"),
			line(end, "gap", "20 min since "+ts(end+tc.uncovered+time.Minute)),
		}
		got := Poller(state(t, ts(time.Minute), lines), now)
		if has := strings.Contains(got, "1 gap"); has != tc.gap {
			t.Fatalf("%v uncovered: got %q", tc.uncovered, got)
		}
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
	if len(parts) != 2 || parts[0] != "poller: no healthy run since "+old || !strings.HasPrefix(parts[1], "poller: 1 failed poll in the last 24h") {
		t.Fatalf("got %q", got)
	}
}
