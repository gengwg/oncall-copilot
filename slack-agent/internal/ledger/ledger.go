// Package ledger records DC requests per node so the same fault is not filed
// twice. The skill itself has no "already sent" check.
package ledger

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	TS      time.Time
	Cluster string
	Node    string
	IRMID   string
	Status  string // drafted | sent | already_sent | ignored | failed
}

// Ledger is an append-only TSV: ts, cluster, node, irm_id, status.
type Ledger struct {
	path string
	mu   sync.Mutex
}

func New(path string) *Ledger { return &Ledger{path: path} }

func (l *Ledger) Append(e Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\t%s\t%s\t%s\t%s\n",
		e.TS.UTC().Format(time.RFC3339), e.Cluster, e.Node, e.IRMID, e.Status)
	return err
}

func (l *Ledger) All() ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), "\t")
		if len(p) != 5 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, p[0])
		if err != nil {
			continue
		}
		out = append(out, Entry{TS: ts, Cluster: p[1], Node: p[2], IRMID: p[3], Status: p[4]})
	}
	return out, sc.Err()
}

// Recent returns the latest entry for the node since the cutoff that settles
// the question: a draft, a sent request, or a human's "ignore". A failed run
// does not count, so the next alert tries again.
func (l *Ledger) Recent(cluster, node string, since time.Time) (Entry, bool) {
	all, err := l.All()
	if err != nil {
		return Entry{}, false
	}
	var found Entry
	ok := false
	for _, e := range all {
		if e.Cluster != cluster || e.Node != node || e.TS.Before(since) {
			continue
		}
		switch e.Status {
		case "drafted", "sent", "already_sent", "ignored":
			found, ok = e, true
		}
	}
	return found, ok
}
