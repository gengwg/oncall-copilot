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
	assertGolden(t, "BuildPrompt", dcrequest.BuildPrompt(ev, now), "Run /file-dc-request for https://slack.example/archives/C1/p501 (cluster c30, node gpu-9, alert SnapX) in draft-only mode.\n"+
		"Rules for this unattended run:\n"+
		"- Do not cordon, label, taint or drain anything, and do not change any system. If the skill says to cordon, write that the human must decide on the cordon in the request text instead.\n"+
		"- First look in the provider's Slack channel for this cluster for the ticket thread for node gpu-9, going back 60 days. One thread covers a node for its whole life, so an old thread counts. The thread is the one whose PARENT message is the request or ticket for exactly node gpu-9: the whole hostname must match, not a prefix (gpu-9 is not gpu-90). A thread where the node appears only in replies does not count. The one exception is a shared-component thread whose parent lists the node among its affected nodes. If several threads qualify, use the newest.\n"+
		"- Current time: 2026-10-30T20:00:00Z. A thread is quiet only if its newest message ts (Slack ts, epoch seconds) is below 1793304000, which is 24 hours ago. Compare against that number; do not estimate the time yourself.\n"+
		"- If a thread exists and its newest message ts is 1793304000 or later, output exactly one line \"RESULT: ALREADY_SENT <link or ticket>\" and stop. Do not draft anything.\n"+
		"- If a thread exists and its newest message ts is below 1793304000, gather fresh evidence as the skill asks, compose one follow-up and save it only as a draft reply with slack_send_message_draft (channel_id, thread_ts = the thread parent's ts). Then output exactly one line \"RESULT: FOLLOWUP_DRAFTED <link>\", where <link> is the permalink of the thread's parent message (the draft call returns a channel link, not the thread). If the draft fails (for example attached_draft_exists, or the channel is not available), output \"RESULT: FAILED <reason>\" instead.\n"+
		"- The follow-up is one short message addressed to the provider contact already in the thread. It gives the ticket id, the node's serial if known, hostname and IP if known, what changed since the last message (new evidence, such as the Xid still latched, remapped rows rising, a reboot that did not hold, and how long since the last update), one concrete ask (a date, an RMA, a reopen, or a specific check), and the node state (\"cordoned with no customer workloads, so it can be taken any time\" when true). It ends \"Thank you.\" Example: \"Hi <contact>, checking in on <ticket> for <serial> / <node> (<IP>). It has been a week since the reopen. Is there a date for the GPU 0 replacement, or anything you need from us? The node is still NotReady and cordoned with no customer workloads, so the swap can happen any time. Thank you.\"\n"+
		"- If there is no thread, gather the evidence the skill asks for, write the request in the skill's format, and create it as a Slack draft in the provider channel with slack_send_message_draft. Then output exactly one line \"RESULT: DRAFTED <channel name>\".\n"+
		"- If you cannot finish for any reason, output exactly one line \"RESULT: FAILED <short reason>\".\n"+
		"The RESULT line must be the last line of your reply.")
	ev.URL = ""
	assertGolden(t, "BuildPrompt without URL", dcrequest.BuildPrompt(ev, now), "Run /file-dc-request for IRM alert group #501 (cluster c30, node gpu-9, alert SnapX) in draft-only mode.\n"+
		"Rules for this unattended run:\n"+
		"- Do not cordon, label, taint or drain anything, and do not change any system. If the skill says to cordon, write that the human must decide on the cordon in the request text instead.\n"+
		"- First look in the provider's Slack channel for this cluster for the ticket thread for node gpu-9, going back 60 days. One thread covers a node for its whole life, so an old thread counts. The thread is the one whose PARENT message is the request or ticket for exactly node gpu-9: the whole hostname must match, not a prefix (gpu-9 is not gpu-90). A thread where the node appears only in replies does not count. The one exception is a shared-component thread whose parent lists the node among its affected nodes. If several threads qualify, use the newest.\n"+
		"- Current time: 2026-10-30T20:00:00Z. A thread is quiet only if its newest message ts (Slack ts, epoch seconds) is below 1793304000, which is 24 hours ago. Compare against that number; do not estimate the time yourself.\n"+
		"- If a thread exists and its newest message ts is 1793304000 or later, output exactly one line \"RESULT: ALREADY_SENT <link or ticket>\" and stop. Do not draft anything.\n"+
		"- If a thread exists and its newest message ts is below 1793304000, gather fresh evidence as the skill asks, compose one follow-up and save it only as a draft reply with slack_send_message_draft (channel_id, thread_ts = the thread parent's ts). Then output exactly one line \"RESULT: FOLLOWUP_DRAFTED <link>\", where <link> is the permalink of the thread's parent message (the draft call returns a channel link, not the thread). If the draft fails (for example attached_draft_exists, or the channel is not available), output \"RESULT: FAILED <reason>\" instead.\n"+
		"- The follow-up is one short message addressed to the provider contact already in the thread. It gives the ticket id, the node's serial if known, hostname and IP if known, what changed since the last message (new evidence, such as the Xid still latched, remapped rows rising, a reboot that did not hold, and how long since the last update), one concrete ask (a date, an RMA, a reopen, or a specific check), and the node state (\"cordoned with no customer workloads, so it can be taken any time\" when true). It ends \"Thank you.\" Example: \"Hi <contact>, checking in on <ticket> for <serial> / <node> (<IP>). It has been a week since the reopen. Is there a date for the GPU 0 replacement, or anything you need from us? The node is still NotReady and cordoned with no customer workloads, so the swap can happen any time. Thank you.\"\n"+
		"- If there is no thread, gather the evidence the skill asks for, write the request in the skill's format, and create it as a Slack draft in the provider channel with slack_send_message_draft. Then output exactly one line \"RESULT: DRAFTED <channel name>\".\n"+
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
	h.a.Pager = alreadyAckedIRM{h.irm}
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
