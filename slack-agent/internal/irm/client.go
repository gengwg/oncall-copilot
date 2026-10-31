// Package irm is the agent's narrow view of the Grafana IRM (OnCall) API:
// three reads and the two writes it is allowed to make. The allowlist is a
// transport, so no code path in this binary can reach resolve, silence or
// delete even by mistake.
package irm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/oncall"
)

// ErrNotAllowed is returned by the transport for any write outside the allowlist.
var ErrNotAllowed = errors.New("irm: request not in the write allowlist")

// ErrAlreadyAcked means the group was acknowledged before we got to it. Callers
// treat it as success: the escalation is already stopped and someone saw it.
var ErrAlreadyAcked = errors.New("irm: already acknowledged")

var writeAllow = regexp.MustCompile(`^/api/v1/alert_groups/[^/]+/(acknowledge|unacknowledge)/?$`)

// allowlist lets GETs through and only the two acknowledge paths otherwise.
type allowlist struct {
	basePath string
	next     http.RoundTripper
}

func (a allowlist) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		p := strings.TrimPrefix(r.URL.Path, a.basePath)
		if !writeAllow.MatchString(p) {
			return nil, fmt.Errorf("%w: %s %s", ErrNotAllowed, r.Method, r.URL.Path)
		}
	}
	return a.next.RoundTrip(r)
}

// NewTransport wraps next with the allowlist for an API rooted at base.
func NewTransport(base string, next http.RoundTripper) (http.RoundTripper, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	if next == nil {
		next = http.DefaultTransport
	}
	return allowlist{basePath: strings.TrimSuffix(u.Path, "/"), next: next}, nil
}

type Client struct {
	base       string
	token      string
	grafanaURL string
	http       *http.Client
}

// New builds a client. A Grafana service-account token (glsa_...) is sent as a
// Bearer with X-Grafana-URL; a personal OnCall API token is sent bare.
func New(base, token, grafanaURL string) (*Client, error) {
	tr, err := NewTransport(base, nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		base: strings.TrimSuffix(base, "/"), token: token, grafanaURL: grafanaURL,
		http: &http.Client{Transport: tr, Timeout: 30 * time.Second},
	}, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if strings.HasPrefix(c.token, "glsa_") {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		req.Header.Set("Authorization", c.token)
	}
	if c.grafanaURL != "" {
		req.Header.Set("X-Grafana-URL", c.grafanaURL)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, err
}

type Group struct {
	ID         string `json:"id"`
	Number     int    `json:"alert_group_number"`
	State      string `json:"state"`
	Permalinks struct {
		Slack string `json:"slack"`
		Web   string `json:"web"`
	} `json:"permalinks"`
}

func (c *Client) Group(ctx context.Context, id string) (Group, error) {
	code, b, err := c.do(ctx, http.MethodGet, "/api/v1/alert_groups/"+url.PathEscape(id), nil)
	if err != nil {
		return Group{}, err
	}
	if code != http.StatusOK {
		return Group{}, fmt.Errorf("irm: alert group %s: HTTP %d", id, code)
	}
	var g Group
	return g, json.Unmarshal(b, &g)
}

// FinalShifts lists who covers the schedule between the two dates (inclusive).
func (c *Client) FinalShifts(ctx context.Context, scheduleID string, from, to time.Time) ([]oncall.Shift, error) {
	q := url.Values{"start_date": {from.UTC().Format("2006-01-02")}, "end_date": {to.UTC().Format("2006-01-02")}}
	code, b, err := c.do(ctx, http.MethodGet, "/api/v1/schedules/"+url.PathEscape(scheduleID)+"/final_shifts?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("irm: final_shifts: HTTP %d", code)
	}
	var out struct {
		Results []oncall.Shift `json:"results"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

func (c *Client) action(ctx context.Context, id, verb string) error {
	code, b, err := c.do(ctx, http.MethodPost, "/api/v1/alert_groups/"+url.PathEscape(id)+"/"+verb, nil)
	if err != nil {
		return err
	}
	switch {
	case code == http.StatusOK:
		return nil
	case code == http.StatusBadRequest && strings.Contains(strings.ToLower(string(b)), "acknowledged"):
		return ErrAlreadyAcked
	default:
		return fmt.Errorf("irm: %s %s: HTTP %d: %s", verb, id, code, strings.TrimSpace(string(b)))
	}
}

func (c *Client) Acknowledge(ctx context.Context, id string) error {
	return c.action(ctx, id, "acknowledge")
}
func (c *Client) Unacknowledge(ctx context.Context, id string) error {
	return c.action(ctx, id, "unacknowledge")
}
