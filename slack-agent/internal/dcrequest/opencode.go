package dcrequest

import (
	"bufio"
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
)

// spendLimit matches Claude Code's refusal once the owner's usage or spend
// limit is used up, e.g. "You've hit your weekly limit · resets 2am".
var spendLimit = regexp.MustCompile(`(?i)hit your (individual |weekly |daily |monthly )?(spend|usage|weekly|daily|monthly)? ?limit|spend limit|usage limit`)

// SpendLimited reports whether a failed run's reason is the Claude limit.
func SpendLimited(r Result) bool { return r.Status == Failed && spendLimit.MatchString(r.Detail) }

// FallbackRunner runs Primary and, only when Primary fails on its spend or
// usage limit, Fallback on the same event. Any other failure stands: a skill
// that could not find the channel or an ssh that was refused would fail the
// same way on another model, and the owner should see why.
type FallbackRunner struct {
	Primary  Runner
	Fallback Runner // nil: no fallback
}

func (f FallbackRunner) FileDCRequest(ctx context.Context, ev event.Event) (Result, error) {
	res, err := f.Primary.FileDCRequest(ctx, ev)
	if err != nil || f.Fallback == nil || !SpendLimited(res) {
		return res, err
	}
	fb, ferr := f.Fallback.FileDCRequest(ctx, ev)
	if ferr != nil {
		fb = Result{Status: Failed, Detail: ferr.Error()}
	}
	fb.Via = "OpenCode"
	if fb.Status == Failed {
		fb.Detail = res.Detail + "; OpenCode fallback: " + fb.Detail
	}
	return fb, nil
}

// OpenCodeRunner runs the same skill and prompt through `opencode run`, under
// its own agent whose permissions the runner sets, not the host's config.
type OpenCodeRunner struct {
	Bin     string
	Dir     string
	Model   string // provider/model; "" is OpenCode's default
	Timeout time.Duration
}

// OpenCodeAgent is "dc-draft": the claude runner's rules in OpenCode terms.
// Everything is denied first and the last matching rule wins, so only what is
// listed after "*" runs: read, look, draft. Sending, scheduling and reacting
// in Slack, editing files, and kubectl writes stay denied. MCP tools are
// matched by name with any server prefix. JSON key order matters here, so the
// config is written out, not marshalled from a map.
const OpenCodeAgent = `{"agent":{"dc-draft":{"mode":"primary",` +
	`"description":"Unattended DC request draft: read, look, draft only",` +
	`"permission":{"*":"deny",` +
	`"read":"allow","grep":"allow","glob":"allow","list":"allow","skill":"allow","webfetch":"allow",` +
	`"external_directory":"allow","edit":"deny","task":"deny","question":"deny",` +
	`"bash":{"*":"deny",` +
	`"kubectl --context=* get *":"allow","kubectl --context=* describe *":"allow",` +
	`"ssh *":"allow","curl *":"allow","git -C * show *":"allow","git -C * ls-tree *":"allow",` +
	`"kubectl * cordon *":"deny","kubectl * uncordon *":"deny","kubectl * label *":"deny",` +
	`"kubectl * taint *":"deny","kubectl * drain *":"deny","kubectl * delete *":"deny",` +
	`"kubectl * apply *":"deny","kubectl * patch *":"deny","kubectl * edit *":"deny"},` +
	`"*slack_read_channel":"allow","*slack_read_thread":"allow",` +
	`"*slack_search_users":"allow","*slack_search_channels":"allow","*slack_search_public_and_private":"allow",` +
	`"*slack_send_message_draft":"allow",` +
	`"*query_prometheus":"allow","*get_alert_group":"allow"}}}}`

// OpenCodeArgs is the fixed flag set (OpenCode 2). No --auto: nothing is
// approved that the agent's rules do not allow. --standalone: without it the
// run goes to the host's background service, which never sees
// OPENCODE_CONFIG_CONTENT and so has no dc-draft agent.
func OpenCodeArgs(prompt, model string) []string {
	a := []string{"run", "--standalone", "--format", "json", "--agent", "dc-draft"}
	if model != "" {
		a = append(a, "--model", model)
	}
	return append(a, prompt)
}

// OpenCodeEnv is the environment for the run: the host's, plus the agent.
func OpenCodeEnv(base []string) []string {
	return append(base, "OPENCODE_CONFIG_CONTENT="+OpenCodeAgent)
}

// openCodePrompt points OpenCode at the skill by its tool; the slash command is
// Claude Code's.
func openCodePrompt(ev event.Event, now time.Time) string {
	return "Load the file-dc-request skill with the skill tool and follow it.\n" + BuildPrompt(ev, now)
}

func (r OpenCodeRunner) FileDCRequest(ctx context.Context, ev event.Event) (Result, error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Bin, OpenCodeArgs(openCodePrompt(ev, time.Now()), r.Model)...)
	cmd.Dir = r.Dir
	cmd.Stdin = nil
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Env = OpenCodeEnv(os.Environ())
	err := cmd.Run()
	res := ParseOpenCode(stdout.Bytes())
	switch {
	case ctx.Err() != nil:
		return Result{Status: Failed, Detail: "timed out after " + timeout.String(), CostUSD: res.CostUSD}, nil
	case err != nil && res.Status == Failed:
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = strings.TrimPrefix(res.Detail, "no RESULT line: ")
		}
		if len(reason) > 300 {
			reason = reason[:300]
		}
		return Result{Status: Failed, Detail: fmt.Sprintf("opencode exited: %v; %s", err, reason), CostUSD: res.CostUSD}, nil
	}
	return res, nil
}

// ParseOpenCode reads `opencode run --format json` output, one event per
// line: the reply is the text events in order, the cost the sum of the steps'.
// An error event with no RESULT line is the failure reason.
func ParseOpenCode(out []byte) Result {
	var text strings.Builder
	var cost float64
	var lastErr string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var e struct {
			Type string `json:"type"`
			Part struct {
				Text string  `json:"text"`
				Cost float64 `json:"cost"`
			} `json:"part"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "text":
			text.WriteString(e.Part.Text)
			text.WriteString("\n")
		case "step_finish":
			cost += e.Part.Cost
		case "error":
			lastErr = string(e.Error)
		}
	}
	res := parseResultText(text.String())
	if res.Status == Failed && strings.HasPrefix(res.Detail, "no RESULT line") && lastErr != "" {
		res.Detail = "error: " + lastErr
	}
	res.CostUSD = cost
	return res
}
