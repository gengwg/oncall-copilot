// Package health summarizes the files the alert poller keeps for the daily
// digest. It holds no state: everything is a function of the directory and the
// time it is asked.
package health

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	window = 24 * time.Hour
	// The poller runs every minute; past this, a missing success means it is down now.
	staleAfter = 10 * time.Minute
	// Longest silence inside a problem that still counts as the poller running.
	maxQuiet = 10 * time.Minute
)

type event struct {
	at     time.Time
	kind   string
	detail string
}

// Poller summarizes the poller's health files for the 24 hours before now.
// It returns "" when there was no problem, otherwise one or two lines. The
// poller writes one health.log line per run while a problem lasts, so counts
// are runs, not incidents.
func Poller(dir string, now time.Time) string {
	var out []string
	lastOK, err := readLastOK(dir)
	switch {
	case err != nil:
		out = append(out, "poller health unknown: "+err.Error())
	case now.Sub(lastOK) > staleAfter:
		out = append(out, "poller: no healthy run since "+lastOK.UTC().Format(time.RFC3339))
	}
	events, err := readLog(dir)
	if err != nil {
		out = append(out, "poller health unknown: "+err.Error())
	} else if s := summarize(events, now); s != "" {
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

func readLastOK(dir string) (time.Time, error) {
	b, err := os.ReadFile(filepath.Join(dir, "last-ok"))
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}, fmt.Errorf("last-ok: %w", err)
	}
	return t, nil
}

// readLog keeps the lines that parse; a missing file means no problem was
// ever recorded.
func readLog(dir string) ([]event, error) {
	b, err := os.ReadFile(filepath.Join(dir, "health.log"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var evs []event
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.SplitN(l, "\t", 3)
		if len(f) != 3 {
			continue
		}
		at, err := time.Parse(time.RFC3339, f[0])
		if err != nil {
			continue
		}
		switch f[1] {
		case "poll-failed", "ping-failed", "held", "gap":
			evs = append(evs, event{at, f[1], f[2]})
		}
	}
	return evs, nil
}

func summarize(evs []event, now time.Time) string {
	// A gap is a stop only when no run reported inside its span. The first
	// healthy run after a hold, a token expiry or failed pings writes a gap
	// covering it, and that gap is the same problem, not a second outage.
	var polls, pings, runs, gaps, longest, holdMin int
	var last *event
	for i, e := range evs {
		if e.at.After(now) || now.Sub(e.at) > window {
			continue
		}
		switch e.kind {
		case "poll-failed":
			polls++
		case "ping-failed":
			pings++
		case "held":
			runs++
		case "gap":
			mins, since := parseGap(e.detail)
			if covers, hold := covered(evs, since, e.at); covers {
				if hold {
					holdMin += mins
				}
				continue
			}
			gaps++
			longest = max(longest, mins)
		}
		last = &evs[i]
	}
	var parts []string
	if polls > 0 {
		parts = append(parts, plural(polls, "failed poll"))
	}
	if pings > 0 {
		parts = append(parts, plural(pings, "failed ping"))
	}
	if runs > 0 {
		s := "heartbeat held for " + plural(runs, "run")
		if holdMin > 0 {
			s += fmt.Sprintf(" (about %d min)", holdMin)
		}
		parts = append(parts, s)
	}
	if gaps > 0 {
		s := plural(gaps, "gap")
		if longest > 0 {
			s += fmt.Sprintf(" (longest %d min)", longest)
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("poller: %s in the last 24h (last: %s %s %s)", strings.Join(parts, ", "),
		last.at.UTC().Format(time.RFC3339), last.kind, last.detail)
}

// parseGap reads "<N> min since <RFC3339>"; what does not parse comes back zero.
func parseGap(detail string) (mins int, since time.Time) {
	f := strings.Fields(detail)
	if len(f) < 4 || f[1] != "min" || f[2] != "since" {
		return 0, time.Time{}
	}
	mins, _ = strconv.Atoi(f[0])
	since, _ = time.Parse(time.RFC3339, f[3])
	return mins, since
}

// covered reports whether problem lines account for a gap: none of the
// stretches between since, consecutive problem lines and end is longer than
// maxQuiet, so some run was reporting throughout. A longer silence is a real
// stop. hold says a held line is among them, so the gap's length is the hold's
// duration.
func covered(evs []event, since, end time.Time) (covers, hold bool) {
	if since.IsZero() {
		return false, false
	}
	var in []time.Time
	for _, e := range evs {
		if e.kind == "gap" || e.at.Before(since) || e.at.After(end) {
			continue
		}
		in = append(in, e.at)
		hold = hold || e.kind == "held"
	}
	if len(in) == 0 {
		return false, false
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Before(in[j]) })
	prev := since
	for _, t := range append(in, end) {
		if t.Sub(prev) > maxQuiet {
			return false, false
		}
		prev = t
	}
	return true, hold
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
