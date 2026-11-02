// Package pager is the vocabulary the agent shares with whichever paging
// service it acks in.
package pager

import "errors"

// Alert is a pager's alert group, reduced to what the agent uses.
type Alert struct {
	ID, Ref, URL, State string // State "firing" means still paging
	Labels              map[string]string
	StartsAt            string // RFC3339 or ""
}

var (
	ErrNotFound     = errors.New("pager: alert not found")
	ErrAlreadyAcked = errors.New("pager: already acknowledged")
)

// Label is how operator-facing text names a pager.
func Label(name string) string {
	if name == "" || name == "irm" {
		return "IRM"
	}
	return name
}
