package notes

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKeyMatchesTheScriptsSanitizing(t *testing.T) {
	for _, c := range []struct{ cluster, node, want string }{
		{"lat-uc1-prod-1", "gpu-worker-open-racer", "lat-uc1-prod-1@gpu-worker-open-racer"},
		{"c1", "", "c1@"},
		{"../c", "a b/c", ".._c@a_b_c"},
		{"c1", "nödе", "c1@n__d__"},
	} {
		if got := Key(c.cluster, c.node); got != c.want {
			t.Errorf("Key(%q, %q) = %q, want %q", c.cluster, c.node, got, c.want)
		}
	}
}

func TestAppendAndList(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	at := time.Date(2026, 11, 4, 2, 0, 0, 0, time.UTC)
	if err := s.Append("c1", "n1", Note{TS: at, Ref: "IRM #5611", Alertname: "Node Disk Pressure",
		Text: "stargz store fills /\n\tnothing prunes it"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append("c1", "n2", Note{TS: at, Ref: "IRM #1", Alertname: "x", Text: "other node"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.List("c1", "n1")
	if err != nil || len(got) != 1 {
		t.Fatalf("List = %v, %v", got, err)
	}
	if got[0].Text != "stargz store fills / nothing prunes it" || got[0].Ref != "IRM #5611" || !got[0].TS.Equal(at) {
		t.Fatalf("note = %+v", got[0])
	}
	b, _ := os.ReadFile(filepath.Join(s.Dir, "c1@n1.tsv"))
	if want := "2026-11-04T02:00:00Z\tIRM #5611\tNode Disk Pressure\tstargz store fills / nothing prunes it\n"; string(b) != want {
		t.Fatalf("file = %q, want %q", b, want)
	}
	st, _ := os.Stat(filepath.Join(s.Dir, "c1@n1.tsv"))
	if st.Mode().Perm()&0o040 == 0 {
		t.Fatalf("mode %v: the poller reads these through the group", st.Mode())
	}
}

func TestLongTextIsCut(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.Append("c", "n", Note{TS: time.Now(), Text: strings.Repeat("é", MaxText)}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List("c", "n")
	if len(got) != 1 || len(got[0].Text) > MaxText || !strings.HasPrefix(got[0].Text, "éé") {
		t.Fatalf("got %d notes, text length %d", len(got), len(got[0].Text))
	}
}

func TestOffAndMissing(t *testing.T) {
	if err := (Store{}).Append("c", "n", Note{}); !errors.Is(err, ErrOff) {
		t.Fatalf("Append with no dir: %v", err)
	}
	if got, err := (Store{Dir: t.TempDir()}).List("c", "n"); err != nil || got != nil {
		t.Fatalf("List with no file = %v, %v", got, err)
	}
	if err := (Store{Dir: filepath.Join(t.TempDir(), "absent")}).Append("c", "n", Note{}); err == nil {
		t.Fatal("Append into a missing dir must fail, not create it")
	}
}
