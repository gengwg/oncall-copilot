// Package oncall answers "whose shift is it" from IRM final shifts. It fails
// closed: no data, stale data or an error all read as "not my shift".
package oncall

import (
	"sync"
	"time"
)

// Shift is one row of IRM's final_shifts: who covers [Start, End).
type Shift struct {
	UserPK    string    `json:"user_pk"`
	UserEmail string    `json:"user_email"`
	Start     time.Time `json:"shift_start"`
	End       time.Time `json:"shift_end"`
}

func (s Shift) Covers(at time.Time) bool { return !at.Before(s.Start) && at.Before(s.End) }

// At returns the shift covering the instant, if any.
func At(shifts []Shift, at time.Time) (Shift, bool) {
	for _, s := range shifts {
		if s.Covers(at) {
			return s, true
		}
	}
	return Shift{}, false
}

// Tracker caches the shift table and refuses to answer from a stale copy.
type Tracker struct {
	me     string
	maxAge time.Duration
	now    func() time.Time

	mu        sync.Mutex
	shifts    []Shift
	fetchedAt time.Time
}

func NewTracker(me string, maxAge time.Duration, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{me: me, maxAge: maxAge, now: now}
}

func (t *Tracker) Update(shifts []Shift) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.shifts = shifts
	t.fetchedAt = t.now()
}

func (t *Tracker) fresh() ([]Shift, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fetchedAt.IsZero() || t.now().Sub(t.fetchedAt) > t.maxAge {
		return nil, false
	}
	return t.shifts, true
}

// Mine reports my shift covering at. False when it is not mine, or when the
// table is missing or older than maxAge.
func (t *Tracker) Mine(at time.Time) (Shift, bool) {
	shifts, ok := t.fresh()
	if !ok {
		return Shift{}, false
	}
	s, ok := At(shifts, at)
	if !ok || s.UserPK != t.me {
		return Shift{}, false
	}
	return s, true
}

// Current reports whoever covers at, for the "not acked: on call is X" reply.
func (t *Tracker) Current(at time.Time) (Shift, bool) {
	shifts, ok := t.fresh()
	if !ok {
		return Shift{}, false
	}
	return At(shifts, at)
}
