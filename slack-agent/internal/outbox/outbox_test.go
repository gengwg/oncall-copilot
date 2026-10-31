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
	if err != nil || len(evs) != 1 || evs[0].IRMID != "A" {
		t.Fatalf("got %v %v", evs, err)
	}
	if _, err := Scan(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing or unreadable outbox must be an error, not an empty scan")
	}
}
