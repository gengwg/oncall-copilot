package ledger

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecent(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "dc.tsv"))
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	if _, ok := l.Recent("c1", "n1", now.Add(-14*24*time.Hour)); ok {
		t.Fatal("empty ledger must have nothing recent")
	}
	must := func(e Entry) {
		if err := l.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	must(Entry{TS: now.Add(-20 * 24 * time.Hour), Cluster: "c1", Node: "n1", IRMID: "old", Status: "drafted"})
	must(Entry{TS: now.Add(-3 * 24 * time.Hour), Cluster: "c1", Node: "n1", IRMID: "fail", Status: "failed"})
	must(Entry{TS: now.Add(-2 * 24 * time.Hour), Cluster: "c1", Node: "n2", IRMID: "other", Status: "drafted"})

	if e, ok := l.Recent("c1", "n1", now.Add(-14*24*time.Hour)); ok {
		t.Fatalf("an old draft and a recent failure must not count, got %+v", e)
	}
	must(Entry{TS: now.Add(-1 * 24 * time.Hour), Cluster: "c1", Node: "n1", IRMID: "new", Status: "ignored"})
	e, ok := l.Recent("c1", "n1", now.Add(-14*24*time.Hour))
	if !ok || e.IRMID != "new" || e.Status != "ignored" {
		t.Fatalf("a recent ignore settles it, got %+v ok=%v", e, ok)
	}
	if _, ok := l.Recent("c2", "n1", now.Add(-14*24*time.Hour)); ok {
		t.Fatal("same node name on another cluster is other hardware")
	}
}
