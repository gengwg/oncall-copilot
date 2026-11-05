package dcrequest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
)

type stubRunner struct {
	res   Result
	err   error
	calls int
}

func (s *stubRunner) FileDCRequest(context.Context, event.Event) (Result, error) {
	s.calls++
	return s.res, s.err
}

func TestSpendLimited(t *testing.T) {
	for _, d := range []string{
		"claude exited: exit status 1; You've hit your weekly limit · resets 2am (America/Los_Angeles)",
		"claude exited: exit status 1; You've hit your individual spend limit · your weekly limit resets 2am",
		"Claude AI usage limit reached",
	} {
		if !SpendLimited(Result{Status: Failed, Detail: d}) {
			t.Errorf("not read as the limit: %q", d)
		}
	}
	for _, r := range []Result{
		{Status: Failed, Detail: "claude exited: exit status 1; ssh: connect to host refused"},
		{Status: Failed, Detail: "no RESULT line: could not find the channel"},
		{Status: Drafted, Detail: "spend limit"},
	} {
		if SpendLimited(r) {
			t.Errorf("read as the limit: %+v", r)
		}
	}
}

func TestFallbackOnlyOnTheSpendLimit(t *testing.T) {
	ctx := context.Background()
	limit := Result{Status: Failed, Detail: "claude exited: exit status 1; You've hit your weekly limit · resets 2am"}

	p, f := &stubRunner{res: limit}, &stubRunner{res: Result{Status: Drafted, Detail: "#ext-x", CostUSD: 0.3}}
	got, err := FallbackRunner{Primary: p, Fallback: f}.FileDCRequest(ctx, event.Event{})
	if err != nil || f.calls != 1 || got.Status != Drafted || got.Via != "OpenCode" || got.CostUSD != 0.3 {
		t.Fatalf("limit -> fallback drafts: %+v %v calls=%d", got, err, f.calls)
	}

	p, f = &stubRunner{res: limit}, &stubRunner{res: Result{Status: Failed, Detail: "no Slack MCP"}}
	got, _ = FallbackRunner{Primary: p, Fallback: f}.FileDCRequest(ctx, event.Event{})
	if got.Status != Failed || !strings.Contains(got.Detail, "weekly limit") || !strings.Contains(got.Detail, "OpenCode fallback: no Slack MCP") {
		t.Fatalf("both failed -> both reasons: %+v", got)
	}

	p, f = &stubRunner{res: limit}, &stubRunner{err: errors.New("boom")}
	got, _ = FallbackRunner{Primary: p, Fallback: f}.FileDCRequest(ctx, event.Event{})
	if got.Status != Failed || !strings.Contains(got.Detail, "OpenCode fallback: boom") {
		t.Fatalf("fallback error -> a failure with both reasons: %+v", got)
	}

	for _, r := range []Result{
		{Status: Failed, Detail: "claude exited: exit status 1; ssh refused"},
		{Status: AlreadySent, Detail: "https://slack/p1"},
		{Status: Drafted, Detail: "#ext-x"},
	} {
		p, f = &stubRunner{res: r}, &stubRunner{res: Result{Status: Drafted}}
		got, _ = FallbackRunner{Primary: p, Fallback: f}.FileDCRequest(ctx, event.Event{})
		if f.calls != 0 || got != r {
			t.Errorf("%+v must stand without a fallback run, got %+v calls=%d", r, got, f.calls)
		}
	}

	p = &stubRunner{res: limit}
	if got, _ := (FallbackRunner{Primary: p}).FileDCRequest(ctx, event.Event{}); got != limit {
		t.Fatalf("no fallback configured -> the limit failure: %+v", got)
	}
}

func TestOpenCodeAgentDeniesFirstAndKeepsTheDenyList(t *testing.T) {
	var cfg struct {
		Agent map[string]struct {
			Permission json.RawMessage `json:"permission"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(OpenCodeAgent), &cfg); err != nil {
		t.Fatalf("agent config is not JSON: %v", err)
	}
	perm := string(cfg.Agent["dc-draft"].Permission)
	if !strings.HasPrefix(perm, `{"*":"deny",`) {
		t.Fatalf("the catch-all deny must come first (last match wins): %s", perm)
	}
	if !strings.Contains(perm, `"bash":{"*":"deny",`) {
		t.Fatalf("bash must deny by default: %s", perm)
	}
	for _, want := range []string{`"edit":"deny"`, `"kubectl * cordon *":"deny"`, `"kubectl * drain *":"deny"`, `"*slack_send_message_draft":"allow"`} {
		if !strings.Contains(perm, want) {
			t.Errorf("permission lacks %s", want)
		}
	}
	for _, never := range []string{`"*slack_send_message":"allow"`, `slack_schedule_message":"allow"`, `slack_add_reaction":"allow"`, `"*":"allow"`} {
		if strings.Contains(perm, never) {
			t.Errorf("permission allows %s", never)
		}
	}
	// Every Claude deny for kubectl has an OpenCode twin.
	for _, d := range disallowedTools {
		if strings.HasPrefix(d, "Bash(kubectl") {
			pat := strings.TrimSuffix(strings.TrimPrefix(d, "Bash("), ")")
			if !strings.Contains(perm, `"`+pat+`":"deny"`) {
				t.Errorf("OpenCode lacks the deny %q", pat)
			}
		}
	}
	args := strings.Join(OpenCodeArgs("p", ""), " ")
	if args != "run --standalone --format json --agent dc-draft p" {
		t.Errorf("args: %s", args)
	}
	if strings.Contains(strings.Join(OpenCodeArgs("p", "x/y"), " "), "--auto") {
		t.Error("never --auto")
	}
	env := OpenCodeEnv([]string{"HOME=/h"})
	if env[len(env)-1] != "OPENCODE_CONFIG_CONTENT="+OpenCodeAgent {
		t.Errorf("env: %v", env)
	}
	if p := openCodePrompt(event.Event{Cluster: "c", Node: "n"}, time.Now()); !strings.HasPrefix(p, "Load the file-dc-request skill") || !strings.Contains(p, "RESULT: DRAFTED") {
		t.Errorf("prompt: %s", p)
	}
}

func TestOpenCodeExitResultTrustsCompletedResultWhenTimedOut(t *testing.T) {
	stdout := []byte(`{"type":"text","part":{"text":"RESULT: DRAFTED #ext-x","cost":0.2}}`)
	got := openCodeExitResult(stdout, nil, context.DeadlineExceeded, true, time.Minute)
	if got.Status != Drafted || got.Detail != "#ext-x" {
		t.Fatalf("completed result overwritten by timeout: %+v", got)
	}
}

func TestParseOpenCode(t *testing.T) {
	out := `{"type":"step_start","part":{}}
{"type":"tool_use","part":{"tool":"slack_slack_send_message_draft","state":{"output":"RESULT: FAILED fake, from a tool"}}}
{"type":"text","part":{"text":"Drafted the request.\nRESULT: DRAFTED #ext-site"}}
{"type":"step_finish","part":{"cost":0.12}}
{"type":"step_finish","part":{"cost":0.05}}
not json
`
	got := ParseOpenCode([]byte(out))
	if got.Status != Drafted || got.Detail != "#ext-site" || got.CostUSD < 0.169 || got.CostUSD > 0.171 {
		t.Fatalf("got %+v", got)
	}
	got = ParseOpenCode([]byte(`{"type":"error","error":{"name":"ProviderAuthError","data":{"message":"no key"}}}`))
	if got.Status != Failed || !strings.Contains(got.Detail, "ProviderAuthError") {
		t.Fatalf("error event -> reason: %+v", got)
	}
	if got := ParseOpenCode(nil); got.Status != Failed {
		t.Fatalf("empty -> failed: %+v", got)
	}
}
