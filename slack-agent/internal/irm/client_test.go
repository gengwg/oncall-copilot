package irm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newServer(t *testing.T) (*httptest.Server, *[]string) {
	var seen []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization")+" gurl="+r.Header.Get("X-Grafana-URL"))
		switch {
		case r.Method == "GET" && r.URL.Path == "/oncall/api/v1/alert_groups/G1":
			w.Write([]byte(`{"id":"G1","alert_group_number":41,"state":"firing","permalinks":{"slack":"https://s/p41"}}`))
		case r.Method == "GET" && r.URL.Path == "/oncall/api/v1/alert_groups/" && r.URL.Query().Get("page") == "1":
			w.Write([]byte(`{"next":"p2","results":[{"id":"G7","alert_group_number":7,"state":"firing","permalinks":{"slack":"https://x.slack.com/archives/C1/p100"}}]}`))
		case r.Method == "GET" && r.URL.Path == "/oncall/api/v1/alert_groups/" && r.URL.Query().Get("page") == "2":
			w.Write([]byte(`{"next":null,"results":[{"id":"G5474","alert_group_number":5474,"state":"firing","permalinks":{"slack":"https://x.slack.com/archives/C1/p200"},"last_alert":{"payload":{"alerts":[{"labels":{"alertname":"GPU Xid Critical","cluster":"c1","node":"n1"},"startsAt":"2026-10-30T20:00:00Z"}]}}}]}`))
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/oncall/api/v1/schedules/S1/final_shifts"):
			w.Write([]byte(`{"results":[{"user_pk":"ME","user_email":"me@x","shift_start":"2026-10-30T15:00:00Z","shift_end":"2026-10-31T15:00:00Z"}]}`))
		case r.Method == "POST" && r.URL.Path == "/oncall/api/v1/alert_groups/G1/acknowledge":
			w.Write([]byte(`{"id":"G1","state":"acknowledged"}`))
		case r.Method == "POST" && r.URL.Path == "/oncall/api/v1/alert_groups/G2/acknowledge":
			w.WriteHeader(400)
			w.Write([]byte(`{"detail":"Can't acknowledge an acknowledged alert group"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	return s, &seen
}

func TestReadsAndAcks(t *testing.T) {
	s, seen := newServer(t)
	c, err := New(s.URL+"/oncall", "personal-token", "https://stack.example")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	g, err := c.Group(ctx, "G1")
	if err != nil || g.State != "firing" || g.Number != 41 {
		t.Fatalf("group: %+v %v", g, err)
	}
	sh, err := c.FinalShifts(ctx, "S1", time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(sh) != 1 || sh[0].UserPK != "ME" || !sh[0].End.Equal(time.Date(2026, 10, 31, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("shifts: %+v %v", sh, err)
	}
	if err := c.Acknowledge(ctx, "G1"); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if err := c.Acknowledge(ctx, "G2"); !errors.Is(err, ErrAlreadyAcked) {
		t.Fatalf("second ack must read as already acked, got %v", err)
	}
	for _, line := range *seen {
		if !strings.Contains(line, "auth=personal-token ") || !strings.Contains(line, "gurl=https://stack.example") {
			t.Errorf("personal token must be sent bare with X-Grafana-URL: %s", line)
		}
	}
	if q := (*seen)[1]; !strings.Contains(q, "start_date=2026-10-30") || !strings.Contains(q, "end_date=2026-11-01") {
		t.Errorf("final_shifts query: %s", (*seen)[1])
	}
}

func TestServiceAccountTokenIsBearer(t *testing.T) {
	s, seen := newServer(t)
	c, _ := New(s.URL+"/oncall", "glsa_abc", "")
	_, _ = c.Group(context.Background(), "G1")
	if !strings.Contains((*seen)[0], "auth=Bearer glsa_abc") {
		t.Errorf("got %s", (*seen)[0])
	}
}

func TestAllowlistBlocksOtherWrites(t *testing.T) {
	s, seen := newServer(t)
	tr, err := NewTransport(s.URL+"/oncall", nil)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Transport: tr}
	for _, p := range []string{"/oncall/api/v1/alert_groups/G1/resolve", "/oncall/api/v1/alert_groups/G1/silence", "/oncall/api/v1/alert_groups/G1", "/oncall/api/v1/resolution_notes/", "/oncall/api/v1/alert_groups/G1/acknowledge/extra"} {
		for _, m := range []string{"POST", "DELETE", "PUT"} {
			req, _ := http.NewRequest(m, s.URL+p, nil)
			_, err := hc.Do(req)
			if !errors.Is(err, ErrNotAllowed) {
				t.Errorf("%s %s must be refused by the transport, got %v", m, p, err)
			}
		}
	}
	if len(*seen) != 0 {
		t.Fatalf("refused requests must never reach the server: %v", *seen)
	}
	req, _ := http.NewRequest("POST", s.URL+"/oncall/api/v1/alert_groups/G1/unacknowledge", nil)
	if _, err := hc.Do(req); err != nil {
		t.Fatalf("unacknowledge is allowed: %v", err)
	}
	req, _ = http.NewRequest("GET", s.URL+"/oncall/api/v1/alert_groups/G1", nil)
	if _, err := hc.Do(req); err != nil {
		t.Fatalf("reads are allowed: %v", err)
	}
}

func TestFindGroup(t *testing.T) {
	s, _ := newServer(t)
	c, _ := New(s.URL+"/oncall", "tok", "")
	ctx := context.Background()
	g, err := c.FindGroup(ctx, "#5474")
	if err != nil || g.ID != "G5474" || g.Labels()["node"] != "n1" || g.StartsAt() != "2026-10-30T20:00:00Z" {
		t.Fatalf("by number: %+v %v", g, err)
	}
	g, err = c.FindGroup(ctx, "https://aranyaglobal.slack.com/archives/C1/p100")
	if err != nil || g.ID != "G7" {
		t.Fatalf("by permalink: %+v %v", g, err)
	}
	if _, err := c.FindGroup(ctx, "#42"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown number: %v", err)
	}
	if _, err := c.FindGroup(ctx, "banana"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("garbage ref: %v", err)
	}
}
