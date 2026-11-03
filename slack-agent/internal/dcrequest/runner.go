// Package dcrequest runs the metarepo's /file-dc-request skill headless. The
// skill creates the Slack draft as the person, through their claude.ai Slack
// connector, which is the only way to put a draft in their compose box.
package dcrequest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/pager"
)

type Status string

const (
	Drafted Status = "drafted"
	// FollowupDrafted: the node already had a quiet ticket thread; the draft
	// is a reply in that thread, not a new request.
	FollowupDrafted Status = "followup_drafted"
	AlreadySent     Status = "already_sent"
	Failed          Status = "failed"
)

type Result struct {
	Status  Status
	Detail  string  // channel, link or reason, from the RESULT line
	CostUSD float64 // what claude reported for the run; 0 when unknown
}

type Runner interface {
	FileDCRequest(ctx context.Context, ev event.Event) (Result, error)
}

// ExecRunner shells out to claude -p in the metarepo so its skills load.
// Model, when set, picks a cheaper model for this run than the host default.
type ExecRunner struct {
	Bin     string
	Dir     string
	Model   string
	Timeout time.Duration
}

// Tool rules for the unattended run: read, look, draft. Never cordon, label,
// taint or drain, and never send a message or mail outright.
var (
	allowedTools = []string{
		"Read", "Grep", "Glob", "WebFetch",
		"Bash(kubectl --context=* get *)", "Bash(kubectl --context=* describe *)",
		"Bash(ssh *)", "Bash(curl *)", "Bash(git -C * show *)", "Bash(git -C * ls-tree *)",
		"mcp__claude_ai_Slack__slack_read_channel", "mcp__claude_ai_Slack__slack_read_thread",
		"mcp__claude_ai_Slack__slack_search_users", "mcp__claude_ai_Slack__slack_search_channels",
		"mcp__claude_ai_Slack__slack_search_public_and_private",
		"mcp__claude_ai_Slack__slack_send_message_draft",
		"mcp__claude_ai_grafana-cloud__query_prometheus", "mcp__claude_ai_grafana-cloud__get_alert_group",
	}
	disallowedTools = []string{
		"Bash(kubectl * cordon *)", "Bash(kubectl * uncordon *)", "Bash(kubectl * label *)",
		"Bash(kubectl * taint *)", "Bash(kubectl * drain *)", "Bash(kubectl * delete *)",
		"Bash(kubectl * apply *)", "Bash(kubectl * patch *)", "Bash(kubectl * edit *)",
		"Edit", "Write", "NotebookEdit",
		"mcp__claude_ai_Slack__slack_send_message", "mcp__claude_ai_Slack__slack_schedule_message",
		"mcp__claude_ai_Slack__slack_add_reaction", "mcp__claude_ai_Gmail__send_message",
		"mcp__claude_ai_Gmail__create_draft",
	}
)

// Args is the fixed flag set, exported so the deploy check can print it.
func Args(prompt, model string) []string {
	a := []string{
		"-p", prompt,
		"--output-format", "json",
		"--permission-mode", "dontAsk",
		"--max-turns", "40",
		"--allowedTools", strings.Join(allowedTools, ","),
		"--disallowedTools", strings.Join(disallowedTools, ","),
	}
	if model != "" {
		a = append(a, "--model", model)
	}
	return a
}

func BuildPrompt(ev event.Event) string {
	link := ev.URL
	if link == "" {
		link = pager.Label(ev.Pager) + " alert group #" + ev.Ref
	}
	return fmt.Sprintf(`Run /file-dc-request for %s (cluster %s, node %s, alert %s) in draft-only mode.
Rules for this unattended run:
- Do not cordon, label, taint or drain anything, and do not change any system. If the skill says to cordon, write that the human must decide on the cordon in the request text instead.
- First look in the provider's Slack channel for this cluster for a request or ticket thread about node %s in the last 60 days. One thread covers a node for its whole life, so an old thread counts.
- If a thread exists and the newest message in the thread is within the last 24 hours, output exactly one line "RESULT: ALREADY_SENT <link or ticket>" and stop. Do not draft anything.
- If a thread exists and its newest message is older than 24 hours, gather fresh evidence as the skill asks and write one follow-up reply in that thread, then create it with slack_send_message_draft using the provider channel id and thread_ts set to the thread's parent message ts. Then output exactly one line "RESULT: FOLLOWUP_DRAFTED <thread link>". If the draft fails (for example attached_draft_exists, or the channel is not available), output "RESULT: FAILED <reason>" instead.
- The follow-up is one short message addressed to the provider contact already in the thread. It gives the ticket id, the node's serial if known, hostname and IP if known, what changed since the last message (new evidence, such as the Xid still latched, remapped rows rising, a reboot that did not hold, and how long since the last update), one concrete ask (a date, an RMA, a reopen, or a specific check), and the node state ("cordoned with no customer workloads, so it can be taken any time" when true). It ends "Thank you." Example: "Hi Michael, checking in on 12103 for <serial> / <node> (<IP>). It has been a week since the reopen. Is there a date for the GPU 0 replacement, or anything you need from us? The node is still NotReady and cordoned with no customer workloads, so the swap can happen any time. Thank you."
- If there is no thread, gather the evidence the skill asks for, write the request in the skill's format, and create it as a Slack draft in the provider channel with slack_send_message_draft. Then output exactly one line "RESULT: DRAFTED <channel name>".
- If you cannot finish for any reason, output exactly one line "RESULT: FAILED <short reason>".
The RESULT line must be the last line of your reply.`, link, ev.Cluster, ev.Node, ev.Alertname, ev.Node)
}

var resultLine = regexp.MustCompile(`(?m)^\s*RESULT:\s*(DRAFTED|FOLLOWUP_DRAFTED|ALREADY_SENT|FAILED)\b[ :-]*(.*)$`)

// ParseResult reads claude's JSON output and finds the RESULT line. No RESULT
// line is a failure with the tail of the reply as the reason.
func ParseResult(out []byte) Result {
	var o struct {
		Result  string  `json:"result"`
		IsError bool    `json:"is_error"`
		Cost    float64 `json:"total_cost_usd"`
	}
	text := string(out)
	if err := json.Unmarshal(out, &o); err == nil && o.Result != "" {
		text = o.Result
	}
	res := parseResultText(text)
	res.CostUSD = o.Cost
	return res
}

func parseResultText(text string) Result {
	m := resultLine.FindAllStringSubmatch(text, -1)
	if len(m) == 0 {
		tail := strings.TrimSpace(text)
		if len(tail) > 300 {
			tail = "…" + tail[len(tail)-300:]
		}
		return Result{Status: Failed, Detail: "no RESULT line: " + tail}
	}
	last := m[len(m)-1]
	st := map[string]Status{"DRAFTED": Drafted, "FOLLOWUP_DRAFTED": FollowupDrafted, "ALREADY_SENT": AlreadySent, "FAILED": Failed}[last[1]]
	return Result{Status: st, Detail: strings.TrimSpace(last[2])}
}

func (r ExecRunner) FileDCRequest(ctx context.Context, ev event.Event) (Result, error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Bin, Args(BuildPrompt(ev), r.Model)...)
	cmd.Dir = r.Dir
	cmd.Stdin = nil // claude waits on a tty-less stdin otherwise
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Env = os.Environ()
	err := cmd.Run()
	res := ParseResult(stdout.Bytes())
	if err != nil {
		reason := strings.TrimSpace(stderr.String())
		if len(reason) > 300 {
			reason = reason[:300]
		}
		if ctx.Err() != nil {
			reason = "timed out after " + timeout.String()
		}
		if res.Status != Failed {
			// A RESULT line we trust beats an exit code; but say so.
			return res, nil
		}
		return Result{Status: Failed, Detail: fmt.Sprintf("claude exited: %v; %s", err, reason), CostUSD: res.CostUSD}, nil
	}
	return res, nil
}
