package outbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanErrorsWhenTheDirIsGone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "A.json"), []byte(`{"irm_id":"A","ts":"2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "B.json.tmp"), []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	evs, err := Scan(dir)
	if err != nil || len(evs) != 1 || evs[0].ID != "A" {
		t.Fatalf("got %v %v", evs, err)
	}
	if _, err := Scan(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing or unreadable outbox must be an error, not an empty scan")
	}
}

func TestScanReadsLegacyAndNewFilesSortedByID(t *testing.T) {
	dir := t.TempDir()
	// Same ts so the ID tie-break decides the order.
	files := map[string]string{
		"b.json": `{"irm_id":"B","irm_number":2,"irm_url":"ub","ts":"1"}`,
		"a.json": `{"pager":"irm","id":"A","ref":"1","url":"ua","irm_id":"A","irm_number":1,"ts":"1"}`,
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := Scan(dir)
	if err != nil || len(evs) != 2 {
		t.Fatalf("got %v %v", evs, err)
	}
	if evs[0].ID != "A" || evs[0].Ref != "1" || evs[1].ID != "B" || evs[1].Ref != "2" || evs[1].URL != "ub" {
		t.Fatalf("got %+v", evs)
	}
}
