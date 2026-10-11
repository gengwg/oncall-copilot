package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The static site must work under a path prefix, so every link is relative.
func TestRenderStaticWritesRelativeSite(t *testing.T) {
	src := t.TempDir()
	for _, n := range []string{"2026-10-01-DemoHighLatency.md", "2026-10-02-DemoHighErrorRate.md"} {
		if err := os.WriteFile(filepath.Join(src, n), []byte("# "+n+"\n\n- **Severity**: critical\n<b>not html</b>\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orig := *incDir
	*incDir = src
	t.Cleanup(func() { *incDir = orig })

	out := t.TempDir()
	if err := renderStatic(out); err != nil {
		t.Fatal(err)
	}

	index, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`href="incident/2026-10-01-DemoHighLatency.html"`,
		`href="incident/2026-10-02-DemoHighErrorRate.html"`,
	} {
		if !strings.Contains(string(index), want) {
			t.Errorf("index.html is missing %s", want)
		}
	}
	if strings.Contains(string(index), `href="/`) {
		t.Error("index.html has an absolute link; it would break under /<repo>/ on GitHub Pages")
	}

	page, err := os.ReadFile(filepath.Join(out, "incident", "2026-10-02-DemoHighErrorRate.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `href="../index.html"`) {
		t.Error("incident page back link is not relative")
	}
	if !strings.Contains(string(page), "&lt;b&gt;not html&lt;/b&gt;") {
		t.Error("report body is not HTML-escaped")
	}
}

func TestLoadOrdersNewestFirstAndByNameOnTies(t *testing.T) {
	src := t.TempDir()
	same := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for n, mt := range map[string]time.Time{
		"2026-09-30-DemoMemoryGrowth.md":  same.Add(time.Hour), // newest mtime, oldest name
		"2026-10-01-DemoHighLatency.md":   same,
		"2026-10-02-DemoHighErrorRate.md": same,
	} {
		p := filepath.Join(src, n)
		if err := os.WriteFile(p, []byte("# "+n), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	orig := *incDir
	*incDir = src
	t.Cleanup(func() { *incDir = orig })

	var got []string
	for _, i := range load() {
		got = append(got, i.Name)
	}
	want := []string{"2026-09-30-DemoMemoryGrowth.md", "2026-10-02-DemoHighErrorRate.md", "2026-10-01-DemoHighLatency.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}
