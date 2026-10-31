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

// ErrNotFound means no recent alert group matched the reference.
var ErrNotFound = errors.New("irm: alert group not found")

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
	LastAlert struct {
		Payload struct {
			Alerts []struct {
				Labels   map[string]string `json:"labels"`
				StartsAt string            `json:"startsAt"`
			} `json:"alerts"`
		} `json:"payload"`
	} `json:"last_alert"`
}

// Labels returns the first alert's labels, or an empty map.
func (g Group) Labels() map[string]string {
	if len(g.LastAlert.Payload.Alerts) == 0 {
		return map[string]string{}
	}
	return g.LastAlert.Payload.Alerts[0].Labels
}

// StartsAt returns the first alert's firing time, or "".
func (g Group) StartsAt() string {
	if len(g.LastAlert.Payload.Alerts) == 0 {
		return ""
	}
	return g.LastAlert.Payload.Alerts[0].StartsAt
}

var (
	refNumber    = regexp.MustCompile(`^#?(\d{1,7})$`)
	refPermalink = regexp.MustCompile(`archives/([A-Z0-9]+)/p(\d+)`)
)

// FindGroup resolves "#5474", "5474" or a Slack permalink to the alert's card
// by paging through recent alert groups, newest first. The list API has no
// filter for either, so this stops after findPages pages.
func (c *Client) FindGroup(ctx context.Context, ref string) (Group, error) {
	ref = strings.TrimSpace(ref)
	var match func(Group) bool
	if m := refNumber.FindStringSubmatch(ref); m != nil {
		n := m[1]
		match = func(g Group) bool { return fmt.Sprint(g.Number) == n }
	} else if m := refPermalink.FindStringSubmatch(ref); m != nil {
		frag := "archives/" + m[1] + "/p" + m[2]
		match = func(g Group) bool { return strings.Contains(g.Permalinks.Slack, frag) }
	} else {
		return Group{}, fmt.Errorf("%w: %q is not an alert number or Slack link", ErrNotFound, ref)
	}
	for page := 1; page <= findPages; page++ {
		code, b, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/alert_groups/?perpage=100&page=%d", page), nil)
		if err != nil {
			return Group{}, err
		}
		if code != http.StatusOK {
			return Group{}, fmt.Errorf("irm: alert groups page %d: HTTP %d", page, code)
		}
		var out struct {
			Results []Group `json:"results"`
			Next    *string `json:"next"`
		}
		if err := json.Unmarshal(b, &out); err != nil {
			return Group{}, err
		}
		for _, g := range out.Results {
			if match(g) {
				return g, nil
			}
		}
		if out.Next == nil || len(out.Results) == 0 {
			break
		}
	}
	return Group{}, fmt.Errorf("%w: %s in the last %d groups", ErrNotFound, ref, findPages*100)
}

const findPages = 10

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
