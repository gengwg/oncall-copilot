package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
