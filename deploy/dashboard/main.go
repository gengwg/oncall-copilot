// dashboard serves the on-call copilot's incident feed: a list of incident
// reports written by the agent, plus a health endpoint. Read-only.
//
// With -render it writes the same pages as static files instead, which is
// what the GitHub Pages workflow publishes.
package main

import (
	"flag"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	addr   = flag.String("addr", ":8080", "listen address")
	incDir = flag.String("incidents", "/data/incidents", "incident reports dir")
	render = flag.String("render", "", "write a static copy of the site to this dir and exit")
)

type incident struct {
	Name    string
	Title   string
	ModTime time.Time
	Body    string
}

func main() {
	flag.Parse()
	if *render != "" {
		if err := renderStatic(*render); err != nil {
			slog.Error("render failed", "err", err)
			os.Exit(1)
		}
		return
	}
	http.HandleFunc("GET /", handleIndex)
	http.HandleFunc("GET /incident/", handleIncident)
	http.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	slog.Info("dashboard listening", "addr", *addr, "incidents", *incDir)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func load() []incident {
	ents, _ := os.ReadDir(*incDir)
	var out []incident
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(*incDir, e.Name()))
		if err != nil {
			continue
		}
		fi, _ := e.Info()
		title := strings.TrimSuffix(e.Name(), ".md")
		out = append(out, incident{Name: e.Name(), Title: title, ModTime: fi.ModTime(), Body: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out
}

const page = `<!doctype html><html><head><meta charset="utf-8"><title>OnCall Copilot — Incidents</title>
<meta http-equiv="refresh" content="20">
<style>
body{font-family:system-ui,sans-serif;max-width:900px;margin:2rem auto;padding:0 1rem;background:#0b0e14;color:#e6e9ef}
h1{font-size:1.4rem} a{color:#6db3f2;text-decoration:none}
.card{border:1px solid #2a3240;border-radius:10px;padding:1rem 1.25rem;margin:0.9rem 0;background:#12161f}
.sev-critical{border-left:5px solid #ff5f56}.sev-warning{border-left:5px solid #ffbd2e}
.meta{color:#8b95a7;font-size:0.85rem;margin-bottom:0.5rem}
pre{white-space:pre-wrap;word-wrap:break-word;background:#0b0e14;padding:0.6rem;border-radius:6px;font-size:0.82rem}
.badge{display:inline-block;padding:0.1rem 0.5rem;border-radius:6px;font-size:0.75rem;background:#1d2530;margin-right:0.4rem}
</style></head><body>
<h1>OnCall Copilot — Incident Feed</h1>
<p class="meta">Alerts investigated by the agent (Nemotron 3 Ultra via Nebius Token Factory). Auto-refresh 20s.</p>
%s
</body></html>`

func sevClass(body string) string {
	if strings.Contains(strings.ToLower(body), "critical") {
		return "sev-critical"
	}
	return "sev-warning"
}

// indexHTML lists the incidents; link turns a report file name into its href,
// which differs between the served site and the static one.
func indexHTML(inc []incident, link func(name string) string) string {
	var sb strings.Builder
	if len(inc) == 0 {
		sb.WriteString(`<div class="card">No incidents yet. The copilot is watching.</div>`)
	}
	for _, i := range inc {
		sb.WriteString(fmt.Sprintf(`<div class="card %s"><div class="meta"><span class="badge">%s</span>%s</div><a href="%s">%s</a></div>`,
			sevClass(i.Body), html.EscapeString(i.Title), i.ModTime.Format("15:04:05"), link(i.Name), html.EscapeString(i.Title)))
	}
	return fmt.Sprintf(page, sb.String())
}

func incidentHTML(body, back string) string {
	return fmt.Sprintf(page, fmt.Sprintf(`<div class="card"><div class="meta"><a href="%s">&larr; back</a></div><pre>%s</pre></div>`, back, html.EscapeString(body)))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML(load(), func(name string) string { return "/incident/" + html.EscapeString(name) }))
}

func handleIncident(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(strings.TrimPrefix(r.URL.Path, "/incident/"))
	b, err := os.ReadFile(filepath.Join(*incDir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, incidentHTML(string(b), "/"))
}

// renderStatic writes index.html and incident/<title>.html under dir, with
// relative links so the site works under any path prefix (GitHub Pages serves
// a project site at /<repo>/).
func renderStatic(dir string) error {
	inc := load()
	if err := os.MkdirAll(filepath.Join(dir, "incident"), 0o755); err != nil {
		return err
	}
	index := indexHTML(inc, func(name string) string {
		return "incident/" + html.EscapeString(strings.TrimSuffix(name, ".md")) + ".html"
	})
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o644); err != nil {
		return err
	}
	for _, i := range inc {
		p := filepath.Join(dir, "incident", i.Title+".html")
		if err := os.WriteFile(p, []byte(incidentHTML(i.Body, "../index.html")), 0o644); err != nil {
			return err
		}
	}
	slog.Info("rendered", "dir", dir, "incidents", len(inc))
	return nil
}
