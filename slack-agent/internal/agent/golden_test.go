package agent

import (
	"context"
	"testing"
	"time"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/dcrequest"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/irm"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/slackio"
)

// Golden strings pin the exact operator-facing text today, so moving IRM
// behind a provider boundary can be shown to change none of it.

func goldenEvent() event.Event {
	draft := "/drafts/S1.md"
	return event.Event{
		ID: "S1", Ref: "501", URL: "https://slack.example/archives/C1/p501",
		Alertname: "SnapX", Severity: "critical", Cluster: "c30", Node: "gpu-9",
		StartsAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
		Fault:    "real", Verdict: "hold", FailureMode: "hardware", DCDraft: &draft,
		Brief: "BRIEF SnapX on c30", TS: now.Format(time.RFC3339),
	}
}

func assertGolden(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s changed\n got: %q\nwant: %q", name, got, want)
	}
}

func TestRenderGolden(t *testing.T) {
	assertGolden(t, "Render", Render(goldenEvent()), "*IRM #501 SnapX* (critical) c30 / gpu-9\n"+
		"verdict hold · fault real · failure mode hardware · DC draft pending\n"+
		"https://slack.example/archives/C1/p501\n"+
		"```BRIEF SnapX on c30```")
}

func TestBuildPromptGolden(t *testing.T) {
	ev := goldenEvent()
	assertGolden(t, "BuildPrompt", dcrequest.BuildPrompt(ev), "Run /file-dc-request for https://slack.example/archives/C1/p501 (cluster c30, node gpu-9, alert SnapX) in draft-only mode.\n"+
		"Rules for this unattended run:\n"+
		"- Do not cordon, label, taint or drain anything, and do not change any system. If the skill says to cordon, write that the human must decide on the cordon in the request text instead.\n"+
		"- First look in the provider's Slack channel for this cluster for a request or ticket about node gpu-9 in the last 14 days. If one exists, output exactly one line \"RESULT: ALREADY_SENT <link or ticket>\" and stop.\n"+
		"- Otherwise gather the evidence the skill asks for, write the request in the skill's format, and create it as a Slack draft in the provider channel with slack_send_message_draft. Then output exactly one line \"RESULT: DRAFTED <channel name>\".\n"+
		"- If you cannot finish for any reason, output exactly one line \"RESULT: FAILED <short reason>\".\n"+
		"The RESULT line must be the last line of your reply.")
	ev.URL = ""
	assertGolden(t, "BuildPrompt without URL", dcrequest.BuildPrompt(ev), "Run /file-dc-request for IRM alert group #501 (cluster c30, node gpu-9, alert SnapX) in draft-only mode.\n"+
		"Rules for this unattended run:\n"+
		"- Do not cordon, label, taint or drain anything, and do not change any system. If the skill says to cordon, write that the human must decide on the cordon in the request text instead.\n"+
		"- First look in the provider's Slack channel for this cluster for a request or ticket about node gpu-9 in the last 14 days. If one exists, output exactly one line \"RESULT: ALREADY_SENT <link or ticket>\" and stop.\n"+
		"- Otherwise gather the evidence the skill asks for, write the request in the skill's format, and create it as a Slack draft in the provider channel with slack_send_message_draft. Then output exactly one line \"RESULT: DRAFTED <channel name>\".\n"+
		"- If you cannot finish for any reason, output exactly one line \"RESULT: FAILED <short reason>\".\n"+
		"The RESULT line must be the last line of your reply.")
}

// alreadyAckedIRM reports the ack race: someone acked between the gate's
// lookup and our call.
type alreadyAckedIRM struct{ *fakeIRM }

func (alreadyAckedIRM) Acknowledge(context.Context, string) error { return irm.ErrAlreadyAcked }

func TestAckNotesGolden(t *testing.T) {
	ctx := context.Background()
	reply := func(h *harness) string { return h.poster.lastReply() }
	// The first reply is the ack note; the DC draft reply that follows (the
	// fixture carries a draft path) would otherwise hide it.
	tickOne := func(h *harness, e event.Event) string {
		h.put(e)
		h.tick()
		return h.poster.replies[0]
	}

	h := newHarness(t, myShift)
	assertGolden(t, "own-shift ack", tickOne(h, goldenEvent()), "ts1: acked IRM #501: your shift, brief delivered")

	h = newHarness(t, myShift)
	h.irm.states = map[string]string{"S1": "acknowledged"}
	assertGolden(t, "already acked", tickOne(h, goldenEvent()), "ts1: not acked: already acknowledged")

	h = newHarness(t, myShift)
	h.a.IRM = alreadyAckedIRM{h.irm}
	assertGolden(t, "ack race", tickOne(h, goldenEvent()), "ts1: IRM #501 was already acknowledged")

	h = newHarness(t, myShift)
	e := goldenEvent()
	e.Verdict = "page"
	assertGolden(t, "page verdict", tickOne(h, e), "ts1: not acked: verdict page, IRM keeps paging you")

	h = newHarness(t, theirShift)
	tickOne(h, goldenEvent())
	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: "ts1", Text: "ack"})
	assertGolden(t, "manual ack", reply(h), "ts1: Acked IRM #501 on your instruction.")
	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: "ts1", Text: "unack"})
	assertGolden(t, "manual unack", reply(h), "ts1: Unacked IRM #501; it escalates again.")
}
