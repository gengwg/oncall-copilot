package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func withToken(t *testing.T, v string) {
	t.Helper()
	orig := token
	tok := v
	token = &tok
	t.Cleanup(func() { token = orig })
}

func resetQueue(t *testing.T) {
	t.Helper()
	mu.Lock()
	queue = nil
	mu.Unlock()
}

const webhook = `{"status":"firing","alerts":[{"status":"firing",
	"labels":{"alertname":"DemoHighErrorRate","severity":"critical","service":"demo-app"},
	"annotations":{"summary":"error ratio above 10%"},
	"startsAt":"2026-09-12T00:00:00Z"}]}`

func postAlert(t *testing.T, tok string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/alert", strings.NewReader(webhook))
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	handleAlert(w, r)
	return w
}

func drain(t *testing.T, tok string) (*httptest.ResponseRecorder, []line) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/alerts", nil)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	auth(handleDrain)(w, r)

	var got []line
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("drain returned invalid JSON: %v", err)
		}
	}
	return w, got
}

func authcheck(t *testing.T, tok string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/authcheck", nil)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	auth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })(w, r)
	return w
}

func TestAlertQueuesAndDrains(t *testing.T) {
	withToken(t, "secret")
	resetQueue(t)

	if w := postAlert(t, "secret"); w.Code != http.StatusAccepted {
		t.Fatalf("POST /alert: got %d, want 202", w.Code)
	}

	w, got := drain(t, "secret")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /alerts: got %d, want 200", w.Code)
	}
	if len(got) != 1 {
		t.Fatalf("drained %d alerts, want 1", len(got))
	}
	if got[0].AlertName != "DemoHighErrorRate" || got[0].Service != "demo-app" {
		t.Fatalf("drained %+v, want alertname DemoHighErrorRate on demo-app", got[0])
	}

	// The drain is destructive by design.
	if _, again := drain(t, "secret"); len(again) != 0 {
		t.Fatalf("second drain returned %d alerts, want 0", len(again))
	}
}

func TestDrainRequiresToken(t *testing.T) {
	withToken(t, "secret")
	resetQueue(t)
	postAlert(t, "secret")

	w, _ := drain(t, "wrong")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /alerts with a bad token: got %d, want 401", w.Code)
	}

	// A rejected drain must not consume the queue, or a token mismatch would
	// silently eat every alert it rejects.
	if _, got := drain(t, "secret"); len(got) != 1 {
		t.Fatalf("after a rejected drain the queue holds %d alerts, want 1", len(got))
	}
}

// /authcheck exists so a supervisor can detect token drift without destroying
// the queue — /healthz is unauthenticated and /alerts drains.
func TestAuthcheckIsNonDestructive(t *testing.T) {
	withToken(t, "secret")
	resetQueue(t)
	postAlert(t, "secret")

	if w := authcheck(t, "secret"); w.Code != http.StatusOK {
		t.Fatalf("GET /authcheck with the right token: got %d, want 200", w.Code)
	}
	if w := authcheck(t, "stale"); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /authcheck with a stale token: got %d, want 401", w.Code)
	}

	if _, got := drain(t, "secret"); len(got) != 1 {
		t.Fatalf("/authcheck consumed the queue: %d alerts left, want 1", len(got))
	}
}

func TestPostAlertRequiresToken(t *testing.T) {
	withToken(t, "secret")
	resetQueue(t)

	if w := postAlert(t, "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatalf("POST /alert with a bad token: got %d, want 401", w.Code)
	}
	if _, got := drain(t, "secret"); len(got) != 0 {
		t.Fatalf("rejected POST still queued %d alerts, want 0", len(got))
	}
}

// An empty -token disables auth entirely. demo-services.sh guards against
// starting the relay that way; this pins the behavior it is guarding against.
func TestEmptyTokenDisablesAuth(t *testing.T) {
	withToken(t, "")
	resetQueue(t)

	if w := postAlert(t, ""); w.Code != http.StatusAccepted {
		t.Fatalf("POST /alert with no auth: got %d, want 202", w.Code)
	}
	if w, got := drain(t, ""); w.Code != http.StatusOK || len(got) != 1 {
		t.Fatalf("GET /alerts with no auth: got %d with %d alerts, want 200 with 1", w.Code, len(got))
	}
}

func TestDrainReturnsEmptyArrayNotNull(t *testing.T) {
	withToken(t, "secret")
	resetQueue(t)

	w, _ := drain(t, "secret")
	if body := strings.TrimSpace(w.Body.String()); body != "[]" {
		t.Fatalf("empty drain returned %q, want [] (investigate.sh feeds this to jq)", body)
	}
}
