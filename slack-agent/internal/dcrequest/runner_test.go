package dcrequest

import (
	"strings"
	"testing"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
)

func TestParseResult(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		status Status
		detail string
	}{
		{"drafted from json", `{"result":"evidence...\nRESULT: DRAFTED #ext-cirrascale\n","is_error":false,"total_cost_usd":12.5}`, Drafted, "#ext-cirrascale"},
		{"already sent", `{"result":"RESULT: ALREADY_SENT https://slack/p1"}`, AlreadySent, "https://slack/p1"},
		{"failed with reason", `{"result":"RESULT: FAILED - ssh to node refused"}`, Failed, "ssh to node refused"},
		{"last result line wins", `{"result":"RESULT: FAILED x\nretrying\nRESULT: DRAFTED chan"}`, Drafted, "chan"},
		{"no result line", `{"result":"I could not find the channel."}`, Failed, "no RESULT line: I could not find the channel."},
		{"not json", "garbage from a crash", Failed, "no RESULT line: garbage from a crash"},
	}
	for _, c := range cases {
		got := ParseResult([]byte(c.in))
		if got.Status != c.status || got.Detail != c.detail {
			t.Errorf("%s: got %+v", c.name, got)
		}
	}
	if got := ParseResult([]byte(`{"result":"RESULT: DRAFTED x","total_cost_usd":12.5}`)); got.CostUSD != 12.5 {
		t.Errorf("cost not read: %+v", got)
	}
}

func TestBuildPromptAndArgs(t *testing.T) {
	ev := event.Event{IRMURL: "https://slack/p9", Cluster: "cheerful-tetra", Node: "gpu-7", Alertname: "GPU Xid Critical"}
	p := BuildPrompt(ev)
	for _, want := range []string{"/file-dc-request", "https://slack/p9", "cheerful-tetra", "gpu-7", "Do not cordon", "RESULT: ALREADY_SENT", "RESULT: DRAFTED", "RESULT: FAILED"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	args := strings.Join(Args(p, ""), " ")
	if strings.Contains(args, "--model") {
		t.Error("no --model flag unless a model is configured")
	}
	if m := strings.Join(Args(p, "claude-sonnet-5-5"), " "); !strings.HasSuffix(m, "--model claude-sonnet-5-5") {
		t.Errorf("model flag missing: %s", m)
	}
	for _, want := range []string{"--permission-mode dontAsk", "--output-format json", "slack_send_message_draft", "Bash(kubectl * cordon *)", "mcp__claude_ai_Slack__slack_send_message,"} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q", want)
		}
	}
	if strings.Contains(args, "dangerously") {
		t.Error("the runner must never skip permissions")
	}
}
