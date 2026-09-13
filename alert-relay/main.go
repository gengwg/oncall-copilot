// alert-relay receives Alertmanager webhook notifications, prints each alert
// as one JSON line on stdout, and appends it to a queue that consumers drain
// via GET /alerts. OpenClaw polls /alerts and fires an agent turn per batch.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// Alertmanager v2 webhook payload (subset).
type webhookPayload struct {
	Status string `json:"status"`
	Alerts []struct {
		Status      string            `json:"status"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
		StartsAt    time.Time         `json:"startsAt"`
		EndsAt      time.Time         `json:"endsAt"`
	} `json:"alerts"`
}

// line is the normalized JSONL record the agent consumes.
type line struct {
	ReceivedAt  time.Time         `json:"received_at"`
	Status      string            `json:"status"`
	AlertName   string            `json:"alertname"`
	Severity    string            `json:"severity"`
	Service     string            `json:"service"`
	Summary     string            `json:"summary"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	StartsAt    time.Time         `json:"starts_at"`
}

var (
	addr  = flag.String("addr", "127.0.0.1:9099", "listen address")
	token = flag.String("token", "", "bearer token required on POST /alert (empty = no auth)")
	mu    sync.Mutex // guards queue + stdout
	queue []line
)

func main() {
	flag.Parse()
	http.HandleFunc("POST /alert", handleAlert)
	http.HandleFunc("GET /alerts", auth(handleDrain))
	http.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	// /authcheck is /alerts' auth without the drain, so a supervisor can tell
	// "relay is up" from "relay is up but running a different token" without
	// destroying the queue.
	http.HandleFunc("GET /authcheck", auth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	slog.Info("alert-relay listening", "addr", *addr)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if *token != "" && r.Header.Get("Authorization") != "Bearer "+*token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// handleDrain returns queued alerts as a JSON array and clears the queue.
func handleDrain(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	batch := queue
	queue = nil
	mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if batch == nil {
		batch = []line{}
	}
	_ = json.NewEncoder(w).Encode(batch)
}

func handleAlert(w http.ResponseWriter, r *http.Request) {
	if *token != "" && r.Header.Get("Authorization") != "Bearer "+*token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	for _, a := range p.Alerts {
		rec := line{
			ReceivedAt:  time.Now().UTC(),
			Status:      a.Status,
			AlertName:   a.Labels["alertname"],
			Severity:    a.Labels["severity"],
			Service:     firstNonEmpty(a.Labels["service"], a.Labels["job"]),
			Summary:     a.Annotations["summary"],
			Description: a.Annotations["description"],
			Labels:      a.Labels,
			StartsAt:    a.StartsAt,
		}
		b, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		fmt.Println(string(b))
		queue = append(queue, rec)
	}
	w.WriteHeader(http.StatusAccepted)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
