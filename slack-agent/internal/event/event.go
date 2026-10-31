// Package event holds the outbox event the poller writes and the record the
// agent keeps about what it did with it.
package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Event is one brief, as alert-relay/irm-poll.sh writes it to the outbox.
type Event struct {
	IRMID       string  `json:"irm_id"`
	IRMNumber   int     `json:"irm_number"`
	IRMURL      string  `json:"irm_url"`
	Alertname   string  `json:"alertname"`
	Severity    string  `json:"severity"`
	Cluster     string  `json:"cluster"`
	Node        string  `json:"node"`
	StartsAt    string  `json:"starts_at"`
	Fault       string  `json:"fault"`
	Verdict     string  `json:"verdict"`
	FailureMode string  `json:"failure_mode"`
	Degraded    *string `json:"degraded"`
	DCDraft     *string `json:"dc_draft"`
	Brief       string  `json:"brief"`
	TS          string  `json:"ts"`
}

// StartTime parses the alert's firing time. ok is false when it is missing or
// unparseable, which callers treat as "unknown", never as "now".
func (e Event) StartTime() (t time.Time, ok bool) {
	t, err := time.Parse(time.RFC3339Nano, e.StartsAt)
	return t, err == nil
}

// Record is what the agent did with one event. Stages only move forward and
// each is saved before the next side effect, so a restart repeats nothing.
type Record struct {
	IRMID        string `json:"irm_id"`
	Event        Event  `json:"event"`
	SlackTS      string `json:"slack_ts,omitempty"`
	PostedAt     string `json:"posted_at,omitempty"`
	PostAttempts int    `json:"post_attempts"`
	PostFailed   bool   `json:"post_failed,omitempty"`
	Collapsed    bool   `json:"collapsed,omitempty"`
	Deferred     bool   `json:"deferred,omitempty"` // off-shift: held for the morning digest, no DM
	DeferredAt   string `json:"deferred_at,omitempty"`
	Digested     bool   `json:"digested,omitempty"`
	AckDecided   bool   `json:"ack_decided"`
	AckedAt      string `json:"acked_at,omitempty"`
	AckNote      string `json:"ack_note,omitempty"`
	DCStatus     string `json:"dc_status,omitempty"`
	UpdatedAt    string `json:"updated_at"`
}

// Store keeps one JSON file per record in a directory the agent owns.
type Store struct{ dir string }

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

// Get returns nil, nil when there is no record yet.
func (s *Store) Get(id string) (*Record, error) {
	b, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("record %s: %w", id, err)
	}
	return &r, nil
}

// Save writes whole-then-rename so a crash never leaves a torn record.
func (s *Store) Save(r *Record) error {
	if r.IRMID == "" {
		return errors.New("record without irm_id")
	}
	r.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(r.IRMID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(r.IRMID))
}

// All returns every record, oldest post first.
func (s *Store) All() ([]Record, error) {
	names, err := filepath.Glob(filepath.Join(s.dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, n := range names {
		r, err := s.Get(strings.TrimSuffix(filepath.Base(n), ".json"))
		if err != nil || r == nil {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PostedAt < out[j].PostedAt })
	return out, nil
}

// ByThread finds the record whose Slack message a reply is threaded under.
func (s *Store) ByThread(ts string) (*Record, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].SlackTS == ts {
			return &all[i], nil
		}
	}
	return nil, nil
}

// Undigested returns deferred records that no digest has covered yet, oldest first.
func (s *Store) Undigested() ([]Record, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, r := range all {
		if r.Deferred && !r.Digested {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt < out[j].UpdatedAt })
	return out, nil
}

// PostedSince counts individual (not collapsed) posts since t, for the storm cap.
func (s *Store) PostedSince(t time.Time) (int, error) {
	all, err := s.All()
	if err != nil {
		return 0, err
	}
	n := 0
	cut := t.UTC().Format(time.RFC3339)
	for _, r := range all {
		if r.SlackTS != "" && !r.Collapsed && r.PostedAt >= cut {
			n++
		}
	}
	return n, nil
}
