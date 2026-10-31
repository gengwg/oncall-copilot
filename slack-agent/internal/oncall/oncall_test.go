package oncall

import (
	"testing"
	"time"
)

func TestTrackerMineAndCurrent(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC) // 08:00 PDT handoff
	shifts := []Shift{
		{UserPK: "TINA", UserEmail: "tina@x", Start: t0.Add(-24 * time.Hour), End: t0},
		{UserPK: "ME", UserEmail: "me@x", Start: t0, End: t0.Add(24 * time.Hour)},
		{UserPK: "YOOFI", UserEmail: "yoofi@x", Start: t0.Add(24 * time.Hour), End: t0.Add(48 * time.Hour)},
	}
	now := t0.Add(2 * time.Hour)
	tr := NewTracker("ME", 2*time.Hour, func() time.Time { return now })

	if _, ok := tr.Mine(now); ok {
		t.Fatal("no table yet must read as not on call")
	}
	tr.Update(shifts)

	cases := []struct {
		name string
		at   time.Time
		mine bool
		cur  string
	}{
		{"start of my shift is mine", t0, true, "me@x"},
		{"middle is mine", t0.Add(12 * time.Hour), true, "me@x"},
		{"one second before handoff is theirs", t0.Add(-time.Second), false, "tina@x"},
		{"end of my shift is the next person's", t0.Add(24 * time.Hour), false, "yoofi@x"},
	}
	for _, c := range cases {
		_, mine := tr.Mine(c.at)
		cur, ok := tr.Current(c.at)
		if mine != c.mine || !ok || cur.UserEmail != c.cur {
			t.Errorf("%s: mine=%v cur=%s ok=%v", c.name, mine, cur.UserEmail, ok)
		}
	}

	// Stale table: fail closed even in the middle of my shift.
	now = t0.Add(5 * time.Hour)
	if _, ok := tr.Mine(now); ok {
		t.Fatal("table older than maxAge must read as not on call")
	}
	if _, ok := tr.Current(now); ok {
		t.Fatal("stale table must not name anyone")
	}
}

func TestAtGap(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	shifts := []Shift{{UserPK: "A", Start: t0, End: t0.Add(time.Hour)}}
	if _, ok := At(shifts, t0.Add(2*time.Hour)); ok {
		t.Fatal("a gap in the rota is nobody's shift")
	}
}
