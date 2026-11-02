package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/dcrequest"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/irm"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/ledger"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/oncall"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/pager"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/slackio"
)

// --- fakes

type fakePoster struct {
	fail    bool
	n       int
	posts   []string
	replies []string
}

func (p *fakePoster) PostDM(_ context.Context, text string) (string, error) {
	if p.fail {
		return "", errors.New("slack down")
	}
	p.n++
	p.posts = append(p.posts, text)
	return fmt.Sprintf("ts%d", p.n), nil
}
func (p *fakePoster) Reply(_ context.Context, ts, text string) error {
	p.replies = append(p.replies, ts+": "+text)
	return nil
}
func (p *fakePoster) lastReply() string {
	if len(p.replies) == 0 {
		return ""
	}
	return p.replies[len(p.replies)-1]
}

type fakeIRM struct {
	states   map[string]string
	groupErr error
	acks     []string
	unacks   []string
	findable map[string]irm.Group // ref -> group
}

func (f *fakeIRM) Find(_ context.Context, ref string) (pager.Alert, error) {
	if g, ok := f.findable[ref]; ok {
		return g.Alert(), nil
	}
	return pager.Alert{}, irm.ErrNotFound
}

func (f *fakeIRM) Get(_ context.Context, id string) (pager.Alert, error) {
	if f.groupErr != nil {
		return pager.Alert{}, f.groupErr
	}
	st, ok := f.states[id]
	if !ok {
		st = "firing"
	}
	return pager.Alert{ID: id, State: st}, nil
}
func (f *fakeIRM) Acknowledge(_ context.Context, id string) error {
	f.acks = append(f.acks, id)
	if f.states == nil {
		f.states = map[string]string{}
	}
	f.states[id] = "acknowledged"
	return nil
}
func (f *fakeIRM) Unacknowledge(_ context.Context, id string) error {
	f.unacks = append(f.unacks, id)
	return nil
}

type fakeShifts struct{ table []oncall.Shift }

func (s fakeShifts) Mine(at time.Time) (oncall.Shift, bool) {
	sh, ok := oncall.At(s.table, at)
	if !ok || sh.UserPK != "ME" {
		return oncall.Shift{}, false
	}
	return sh, true
}
func (s fakeShifts) Current(at time.Time) (oncall.Shift, bool) { return oncall.At(s.table, at) }

type fakeRunner struct {
	res   dcrequest.Result
	calls []string
}

func (r *fakeRunner) FileDCRequest(_ context.Context, ev event.Event) (dcrequest.Result, error) {
	r.calls = append(r.calls, ev.Node)
	return r.res, nil
}

// --- fixtures

var (
	handoff = time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC) // 08:00 PDT
	now     = handoff.Add(5 * time.Hour)
	myShift = []oncall.Shift{
		{UserPK: "TINA", UserEmail: "tina@x", Start: handoff.Add(-24 * time.Hour), End: handoff},
		{UserPK: "ME", UserEmail: "me@x", Start: handoff, End: handoff.Add(24 * time.Hour)},
	}
	theirShift = []oncall.Shift{{UserPK: "TINA", UserEmail: "tina@x", Start: handoff, End: handoff.Add(24 * time.Hour)}}
)

type harness struct {
	t      *testing.T
	a      *Agent
	poster *fakePoster
	irm    *fakeIRM
	runner *fakeRunner
	outbox string
}

func newHarness(t *testing.T, shifts []oncall.Shift) *harness {
	t.Helper()
	dir := t.TempDir()
	store, err := event.NewStore(filepath.Join(dir, "events"))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "outbox")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, poster: &fakePoster{}, irm: &fakeIRM{}, runner: &fakeRunner{res: dcrequest.Result{Status: dcrequest.Drafted, Detail: "#ext-x", CostUSD: 7.25}}, outbox: out}
	h.a = &Agent{
		Outbox: out, Store: store, Poster: h.poster, Pager: h.irm, Shifts: fakeShifts{shifts},
		Ledger: ledger.New(filepath.Join(dir, "dc.tsv")), Runner: h.runner,
		CollapseMax: 6, Now: func() time.Time { return now },
		DigestMarker: filepath.Join(dir, "digest-last"),
	}
	return h
}

func (h *harness) put(ev event.Event) {
	h.t.Helper()
	b, _ := json.Marshal(ev)
	if err := os.WriteFile(filepath.Join(h.outbox, ev.ID+".json"), b, 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) tick() {
	h.t.Helper()
	if err := h.a.Tick(context.Background()); err != nil {
		h.t.Fatalf("tick: %v", err)
	}
	h.a.DrainDC(context.Background())
}

func (h *harness) rec(id string) *event.Record {
	h.t.Helper()
	r, err := h.a.Store.Get(id)
	if err != nil || r == nil {
		h.t.Fatalf("record %s: %v %v", id, r, err)
	}
	return r
}

func ev(id string, num int, sev string, startsAt time.Time) event.Event {
	return event.Event{ID: id, Ref: strconv.Itoa(num), URL: "https://slack/p" + id, Alertname: "GPU Xid Critical",
		Severity: sev, Cluster: "c1", Node: "n-" + id, StartsAt: startsAt.Format(time.RFC3339),
		Fault: "real", Verdict: "hold", FailureMode: "hardware", Brief: "ALERT: GPU Xid Critical (" + sev + ")", TS: now.Format(time.RFC3339)}
}

func withDraft(e event.Event) event.Event {
	p := "/drafts/" + e.ID + ".md"
	e.DCDraft = &p
	return e
}

// --- ack gate

func TestAckOnMyShiftAfterDelivery(t *testing.T) {
	h := newHarness(t, myShift)
	h.put(ev("G1", 1, "critical", now.Add(-10*time.Minute)))
	h.tick()
	h.tick() // a second scan must not ack twice or post twice
	if len(h.poster.posts) != 1 || len(h.irm.acks) != 1 || h.irm.acks[0] != "G1" {
		t.Fatalf("want one post and one ack, got posts=%d acks=%v", len(h.poster.posts), h.irm.acks)
	}
	r := h.rec("G1")
	if r.SlackTS != "ts1" || r.AckedAt == "" || !r.AckDecided {
		t.Fatalf("record: %+v", r)
	}
	if !strings.Contains(h.poster.lastReply(), "ts1: acked IRM #1") {
		t.Fatalf("ack must be reported in the brief's thread: %q", h.poster.lastReply())
	}
	if !strings.Contains(h.poster.posts[0], "IRM #1 GPU Xid Critical") {
		t.Fatalf("post: %q", h.poster.posts[0])
	}
}

func TestNoAckCases(t *testing.T) {
	deg := "model call failed"
	cases := []struct {
		name   string
		shifts []oncall.Shift
		ev     event.Event
		prep   func(h *harness)
		want   string
	}{
		{"someone else's shift", theirShift, ev("G1", 1, "critical", now.Add(-time.Minute)), nil, "on call is tina@x"},
		{"schedule unavailable", nil, ev("G1", 1, "critical", now.Add(-time.Minute)), nil, "schedule unavailable"},
		{"fired on the previous shift", myShift, ev("G1", 1, "warning", handoff.Add(-2*time.Minute)), nil, "before your shift"},
		{"degraded brief", myShift, func() event.Event { e := ev("G1", 1, "critical", now); e.Degraded = &deg; return e }(), nil, "brief degraded"},
		{"page verdict keeps escalating", myShift, func() event.Event { e := ev("G1", 1, "critical", now); e.Verdict = "page"; return e }(), nil, "verdict page, IRM keeps paging"},
		{"unknown verdict is not a hold", myShift, func() event.Event { e := ev("G1", 1, "critical", now); e.Verdict = "unknown"; return e }(), nil, "verdict unknown"},
		{"info does not page", myShift, ev("G1", 1, "info", now), nil, "does not page"},
		{"already acked by a human", myShift, ev("G1", 1, "critical", now), func(h *harness) { h.irm.states = map[string]string{"G1": "acknowledged"} }, "already acknowledged"},
		{"resolved meanwhile", myShift, ev("G1", 1, "critical", now), func(h *harness) { h.irm.states = map[string]string{"G1": "resolved"} }, "already resolved"},
		{"start time unknown", myShift, func() event.Event { e := ev("G1", 1, "critical", now); e.StartsAt = ""; return e }(), nil, "start time unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.shifts)
			if c.prep != nil {
				c.prep(h)
			}
			h.put(c.ev)
			h.tick()
			if len(h.irm.acks) != 0 {
				t.Fatalf("must not ack, acked %v", h.irm.acks)
			}
			if len(h.poster.posts) != 1 {
				t.Fatalf("the brief itself must still be delivered, posts=%d", len(h.poster.posts))
			}
			if !strings.Contains(h.poster.lastReply(), c.want) {
				t.Fatalf("reply %q lacks %q", h.poster.lastReply(), c.want)
			}
			if !h.rec("G1").AckDecided {
				t.Fatal("decision must be recorded so it is not re-litigated")
			}
		})
	}
}

func TestNoDeliveryNoAck(t *testing.T) {
	h := newHarness(t, myShift)
	h.poster.fail = true
	h.put(ev("G1", 1, "critical", now))
	for i := 0; i < 5; i++ {
		h.tick()
	}
	if len(h.irm.acks) != 0 || len(h.poster.replies) != 0 {
		t.Fatalf("nothing may be acked or replied without a delivered brief: acks=%v replies=%v", h.irm.acks, h.poster.replies)
	}
	r := h.rec("G1")
	if !r.PostFailed || r.PostAttempts != 3 {
		t.Fatalf("want 3 attempts then give up, got %+v", r)
	}
	h.poster.fail = false
	h.tick()
	if len(h.poster.posts) != 0 || len(h.irm.acks) != 0 {
		t.Fatal("a given-up event stays given up; it keeps escalating in IRM")
	}
}

func TestIRMLookupFailureRetries(t *testing.T) {
	h := newHarness(t, myShift)
	h.irm.groupErr = errors.New("502")
	h.put(ev("G1", 1, "critical", now))
	h.tick()
	if len(h.irm.acks) != 0 || h.rec("G1").AckDecided || len(h.poster.replies) != 0 {
		t.Fatal("an IRM read failure must defer the decision, not settle it")
	}
	h.irm.groupErr = nil
	h.tick()
	if len(h.irm.acks) != 1 || len(h.poster.posts) != 1 {
		t.Fatalf("after IRM recovers: acks=%v posts=%d", h.irm.acks, len(h.poster.posts))
	}
}

func TestDryRunNeverWrites(t *testing.T) {
	h := newHarness(t, myShift)
	h.a.DryRun = true
	h.put(ev("G1", 1, "critical", now))
	h.tick()
	if len(h.irm.acks) != 0 || !strings.Contains(h.poster.lastReply(), "would ack") {
		t.Fatalf("acks=%v reply=%q", h.irm.acks, h.poster.lastReply())
	}
	h.a.HandleInbound(context.Background(), slackio.Inbound{ThreadTS: "ts1", Text: "ack"})
	if len(h.irm.acks) != 0 {
		t.Fatal("dry run must hold even for an explicit ack")
	}
}

func TestBurstCollapsesAndIsNotAcked(t *testing.T) {
	h := newHarness(t, myShift)
	h.a.CollapseMax = 2
	for i := 1; i <= 3; i++ {
		h.put(ev(fmt.Sprintf("G%d", i), i, "warning", now))
	}
	h.tick()
	if len(h.poster.posts) != 1 || !strings.Contains(h.poster.posts[0], "3 briefs") {
		t.Fatalf("want one summary post, got %d: %v", len(h.poster.posts), h.poster.posts)
	}
	if len(h.irm.acks) != 0 {
		t.Fatalf("collapsed briefs are not individually reported, so never acked: %v", h.irm.acks)
	}
	for i := 1; i <= 3; i++ {
		r := h.rec(fmt.Sprintf("G%d", i))
		if !r.Collapsed || !r.AckDecided || r.SlackTS != "ts1" {
			t.Fatalf("record %d: %+v", i, r)
		}
	}
	h.tick()
	if len(h.poster.posts) != 1 {
		t.Fatal("collapsed events must not be re-posted")
	}
}

// --- DC requests

func TestDCDraftOncePerNode(t *testing.T) {
	h := newHarness(t, theirShift) // DC drafting does not depend on whose shift it is
	h.put(withDraft(ev("G1", 1, "critical", now)))
	h.tick()
	if len(h.runner.calls) != 1 || h.runner.calls[0] != "n-G1" {
		t.Fatalf("runner calls: %v", h.runner.calls)
	}
	if r := h.rec("G1"); r.DCStatus != "drafted" {
		t.Fatalf("record: %+v", r)
	}
	if !strings.Contains(h.poster.lastReply(), "DC request drafted for c1/n-G1 in #ext-x") || !strings.Contains(h.poster.lastReply(), "run cost $7.25") {
		t.Fatalf("reply: %q", h.poster.lastReply())
	}
	h.tick()
	if len(h.runner.calls) != 1 {
		t.Fatal("one attempt per event")
	}
	// Same node, new alert group within 14 days: skip, say why, offer `file`.
	e2 := withDraft(ev("G2", 2, "critical", now))
	e2.Node = "n-G1"
	h.put(e2)
	h.tick()
	if len(h.runner.calls) != 1 || h.rec("G2").DCStatus != "skipped_recent" {
		t.Fatalf("calls=%v rec=%+v", h.runner.calls, h.rec("G2"))
	}
	if !strings.Contains(h.poster.lastReply(), "already drafted") || !strings.Contains(h.poster.lastReply(), "`file`") {
		t.Fatalf("reply: %q", h.poster.lastReply())
	}
	h.a.HandleInbound(context.Background(), slackio.Inbound{ThreadTS: h.rec("G2").SlackTS, Text: "file"})
	h.a.DrainDC(context.Background())
	if len(h.runner.calls) != 2 || h.rec("G2").DCStatus != "drafted" {
		t.Fatalf("`file` must force a run: calls=%v rec=%+v", h.runner.calls, h.rec("G2"))
	}
}

func TestDCFailureAndAlreadySent(t *testing.T) {
	h := newHarness(t, myShift)
	h.runner.res = dcrequest.Result{Status: dcrequest.Failed, Detail: "ssh refused"}
	h.put(withDraft(ev("G1", 1, "critical", now)))
	h.tick()
	if r := h.rec("G1"); r.DCStatus != "failed" || !strings.Contains(h.poster.lastReply(), "ssh refused") || !strings.Contains(h.poster.lastReply(), "/file-dc-request https://slack/pG1") {
		t.Fatalf("rec=%+v reply=%q", r, h.poster.lastReply())
	}
	// A failure does not block the next alert on that node.
	h.runner.res = dcrequest.Result{Status: dcrequest.AlreadySent, Detail: "https://slack/p-old"}
	e2 := withDraft(ev("G2", 2, "critical", now))
	e2.Node = "n-G1"
	h.put(e2)
	h.tick()
	if len(h.runner.calls) != 2 || h.rec("G2").DCStatus != "already_sent" || !strings.Contains(h.poster.lastReply(), "already exists") {
		t.Fatalf("calls=%v rec=%+v reply=%q", h.runner.calls, h.rec("G2"), h.poster.lastReply())
	}
}

func TestIgnoreReplySettlesTheNode(t *testing.T) {
	h := newHarness(t, myShift)
	h.put(withDraft(ev("G1", 1, "critical", now)))
	h.tick()
	h.a.HandleInbound(context.Background(), slackio.Inbound{ThreadTS: "ts1", Text: "ignore"})
	if h.rec("G1").DCStatus != "ignored" {
		t.Fatalf("rec=%+v", h.rec("G1"))
	}
	e2 := withDraft(ev("G2", 2, "critical", now))
	e2.Node = "n-G1"
	h.put(e2)
	h.tick()
	if len(h.runner.calls) != 1 || h.rec("G2").DCStatus != "skipped_recent" {
		t.Fatalf("after ignore the next alert on that node must not draft: calls=%v", h.runner.calls)
	}
}

// --- replies

func TestAckAndUnackReplies(t *testing.T) {
	h := newHarness(t, theirShift)
	h.put(ev("G1", 1, "critical", now))
	h.tick()
	if len(h.irm.acks) != 0 {
		t.Fatal("not my shift")
	}
	ctx := context.Background()
	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: "ts1", Text: "unack"})
	if len(h.irm.unacks) != 0 || !strings.Contains(h.poster.lastReply(), "did not ack") {
		t.Fatalf("unack of a group I never acked must be refused: %v %q", h.irm.unacks, h.poster.lastReply())
	}
	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: "ts1", Text: "ACK"})
	if len(h.irm.acks) != 1 || h.rec("G1").AckedAt == "" {
		t.Fatalf("explicit ack is the human's call: acks=%v", h.irm.acks)
	}
	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: "ts1", Text: "unack"})
	if len(h.irm.unacks) != 1 {
		t.Fatalf("unacks=%v", h.irm.unacks)
	}
	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: "nope", Text: "ack"})
	if len(h.irm.acks) != 1 || !strings.Contains(h.poster.lastReply(), "No alert is attached") {
		t.Fatalf("unknown thread: %q", h.poster.lastReply())
	}
	h.a.HandleInbound(ctx, slackio.Inbound{TS: "t9", Text: "status"})
	if !strings.Contains(h.poster.lastReply(), "On call: tina@x") || !strings.Contains(h.poster.lastReply(), "1 acked by me") {
		t.Fatalf("status: %q", h.poster.lastReply())
	}
	h.a.HandleInbound(ctx, slackio.Inbound{TS: "t9", Text: "what?"})
	if !strings.Contains(h.poster.lastReply(), "Reply in a brief's thread") {
		t.Fatalf("help: %q", h.poster.lastReply())
	}
}

func TestTopLevelCommandWithAlertReference(t *testing.T) {
	h := newHarness(t, theirShift)
	var g irm.Group
	g.ID = "X9"
	g.Number = 5474
	g.State = "firing"
	g.Permalinks.Slack = "https://aranyaglobal.slack.com/archives/C0A9WCF9S56/p1761900000000001"
	g.LastAlert.Payload.Alerts = append(g.LastAlert.Payload.Alerts, struct {
		Labels   map[string]string `json:"labels"`
		StartsAt string            `json:"startsAt"`
	}{Labels: map[string]string{"alertname": "GPU Xid Critical", "cluster": "adapted-deer", "node": "expert-lizard", "severity": "critical"}, StartsAt: "2026-10-30T20:00:00Z"})
	h.irm.findable = map[string]irm.Group{"#5474": g, "https://aranyaglobal.slack.com/archives/C0A9WCF9S56/p1761900000000001": g}
	ctx := context.Background()

	// Slack wraps pasted links in <...>; the command may come first or last.
	h.a.HandleInbound(ctx, slackio.Inbound{TS: "u1", Text: "file <https://aranyaglobal.slack.com/archives/C0A9WCF9S56/p1761900000000001>"})
	h.a.DrainDC(ctx)
	if len(h.runner.calls) != 1 || h.runner.calls[0] != "expert-lizard" {
		t.Fatalf("runner calls: %v", h.runner.calls)
	}
	rec := h.rec("X9")
	if rec.SlackTS != "u1" || rec.DCStatus != "drafted" || rec.AckedAt != "" {
		t.Fatalf("record: %+v", rec)
	}
	if !strings.HasPrefix(h.poster.lastReply(), "u1: DC request drafted for adapted-deer/expert-lizard") {
		t.Fatalf("reply: %q", h.poster.lastReply())
	}
	if len(h.irm.acks) != 0 {
		t.Fatal("attaching by hand must not ack")
	}

	// Follow-up commands work in that thread, and by number as a new message.
	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: "u1", Text: "ack"})
	if len(h.irm.acks) != 1 || h.irm.acks[0] != "X9" {
		t.Fatalf("acks: %v", h.irm.acks)
	}
	h.a.HandleInbound(ctx, slackio.Inbound{TS: "u2", Text: "unack #5474"})
	if len(h.irm.unacks) != 1 {
		t.Fatalf("unacks: %v", h.irm.unacks)
	}

	h.a.HandleInbound(ctx, slackio.Inbound{TS: "u3", Text: "file #9999"})
	if !strings.Contains(h.poster.lastReply(), "u3: Could not find that alert") || len(h.runner.calls) != 1 {
		t.Fatalf("unknown ref: %q calls=%v", h.poster.lastReply(), h.runner.calls)
	}
	h.a.HandleInbound(ctx, slackio.Inbound{TS: "u4", Text: "file"})
	if !strings.Contains(h.poster.lastReply(), "Reply in a brief's thread") {
		t.Fatalf("bare file at top level is help: %q", h.poster.lastReply())
	}
}

func TestSentReplyMarksTheLedger(t *testing.T) {
	h := newHarness(t, myShift)
	h.put(withDraft(ev("G1", 1, "critical", now)))
	h.tick()
	h.a.HandleInbound(context.Background(), slackio.Inbound{ThreadTS: "ts1", Text: "sent"})
	if h.rec("G1").DCStatus != "sent" || !strings.Contains(h.poster.lastReply(), "sent.") {
		t.Fatalf("rec=%+v reply=%q", h.rec("G1"), h.poster.lastReply())
	}
	e, ok := h.a.Ledger.Recent("c1", "n-G1", now.Add(-time.Hour))
	if !ok || e.Status != "sent" {
		t.Fatalf("ledger: %+v ok=%v", e, ok)
	}
}

func TestOffShiftBriefsWaitForTheDigest(t *testing.T) {
	h := newHarness(t, theirShift)
	h.a.DigestAt = "08:05"
	h.a.DigestTZ = time.UTC
	clock := handoff.Add(2 * time.Hour) // 17:00Z, after today's 08:05 digest time
	h.a.Now = func() time.Time { return clock }
	h.put(withDraft(ev("G1", 1, "critical", clock)))
	h.put(ev("G2", 2, "warning", clock))
	h.tick()
	if len(h.poster.posts) != 0 || len(h.irm.acks) != 0 || len(h.runner.calls) != 0 {
		t.Fatalf("off shift: no DM, no ack, no skill run; posts=%d acks=%v runs=%v", len(h.poster.posts), h.irm.acks, h.runner.calls)
	}
	if r := h.rec("G1"); !r.Deferred || !r.AckDecided || r.SlackTS != "" {
		t.Fatalf("record: %+v", r)
	}
	// Deferred after today's 08:05, so they belong to tomorrow's digest; today's
	// ran empty and only wrote the marker.
	h.tick()
	if len(h.poster.posts) != 0 {
		t.Fatal("no second digest the same day")
	}
	clock = clock.Add(24 * time.Hour)
	h.tick()
	if len(h.poster.posts) != 1 || !strings.Contains(h.poster.posts[0], "2 brief(s)") || !strings.Contains(h.poster.posts[0], "reply `file #1`") {
		t.Fatalf("digest: %v", h.poster.posts)
	}
	if !h.rec("G1").Digested || !h.rec("G2").Digested {
		t.Fatal("digested records must be marked")
	}
	h.tick()
	if len(h.poster.posts) != 1 {
		t.Fatal("digested records must not be posted again")
	}
	// A deferred alert can still be acted on by reference, in its own thread.
	h.irm.findable = map[string]irm.Group{"#1": {ID: "G1", Number: 1, State: "firing"}}
	h.a.HandleInbound(context.Background(), slackio.Inbound{TS: "u1", Text: "file #1"})
	h.a.DrainDC(context.Background())
	if len(h.runner.calls) != 1 || h.rec("G1").SlackTS != "u1" || h.rec("G1").DCStatus != "drafted" {
		t.Fatalf("file by reference on a deferred brief: runs=%v rec=%+v", h.runner.calls, h.rec("G1"))
	}
}

func TestOnShiftBriefsStillPostWithDigestOn(t *testing.T) {
	h := newHarness(t, myShift)
	h.a.DigestAt = "08:05"
	h.a.DigestTZ = time.UTC
	h.put(ev("G1", 1, "critical", now.Add(-time.Minute)))
	h.tick()
	if len(h.poster.posts) != 1 || len(h.irm.acks) != 1 {
		t.Fatalf("my shift: posts=%d acks=%v", len(h.poster.posts), h.irm.acks)
	}
}

func TestPagerNameFeedsTheText(t *testing.T) {
	h := newHarness(t, theirShift)
	h.a.PagerName = "fake"
	h.irm.findable = map[string]irm.Group{"#77": {ID: "F77", Number: 77, State: "firing"}}
	ctx := context.Background()
	h.a.HandleInbound(ctx, slackio.Inbound{TS: "f1", Text: "file #77"})
	h.a.DrainDC(ctx)
	if rec := h.rec("F77"); rec.Event.Pager != "fake" || rec.Event.Ref != "77" {
		t.Fatalf("attached event: %+v", rec.Event)
	}
	h.a.DryRun = false
	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: "f1", Text: "ack"})
	if got := h.poster.lastReply(); got != "f1: Acked fake #77 on your instruction." {
		t.Fatalf("ack reply: %q", got)
	}
	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: "f1", Text: "unack"})
	if got := h.poster.lastReply(); got != "f1: Unacked fake #77; it escalates again." {
		t.Fatalf("unack reply: %q", got)
	}
	h.a.HandleInbound(ctx, slackio.Inbound{TS: "f2", Text: "ack #9999"})
	if got := h.poster.lastReply(); got != "f2: Could not find that alert: irm: alert group not found" {
		t.Fatalf("not found reply: %q", got)
	}
}

// Pins the digest DM byte for byte: health reporting must not change it on a
// healthy day.
func TestDigestTextIsPinned(t *testing.T) {
	h := newHarness(t, theirShift)
	h.a.DigestAt = "08:05"
	h.a.DigestTZ = time.UTC
	clock := handoff.Add(2 * time.Hour)
	h.a.Now = func() time.Time { return clock }
	h.put(withDraft(ev("G1", 1, "critical", clock)))
	h.put(ev("G2", 2, "warning", clock))
	h.tick()
	clock = clock.Add(24 * time.Hour)
	h.tick()
	if len(h.poster.posts) != 1 {
		t.Fatalf("posts: %q", h.poster.posts)
	}
	want := "*Overnight digest: 2 brief(s) on other shifts, none acked by me.*\n" +
		"• IRM #1 GPU Xid Critical (critical) c1/n-G1 · verdict hold · fault real · DC draft ready: reply `file #1` · https://slack/pG1\n" +
		"• IRM #2 GPU Xid Critical (warning) c1/n-G2 · verdict hold · fault real · https://slack/pG2\n"
	if h.poster.posts[0] != want {
		t.Fatalf("digest text changed:\n got: %q\nwant: %q", h.poster.posts[0], want)
	}
}

// --- health in the digest

const healthDigestText = "*Overnight digest: 2 brief(s) on other shifts, none acked by me.*\n" +
	"• IRM #1 GPU Xid Critical (critical) c1/n-G1 · verdict hold · fault real · DC draft ready: reply `file #1` · https://slack/pG1\n" +
	"• IRM #2 GPU Xid Critical (warning) c1/n-G2 · verdict hold · fault real · https://slack/pG2\n"

// digestHarness defers two briefs, moves the clock past the next 08:05, and
// leaves the digest to the caller's next tick. state is the poller's dir.
func digestHarness(t *testing.T, withBriefs bool) (*harness, *time.Time, string) {
	t.Helper()
	h := newHarness(t, theirShift)
	h.a.DigestAt = "08:05"
	h.a.DigestTZ = time.UTC
	clock := handoff.Add(2 * time.Hour)
	h.a.Now = func() time.Time { return clock }
	if withBriefs {
		h.put(withDraft(ev("G1", 1, "critical", clock)))
		h.put(ev("G2", 2, "warning", clock))
	}
	h.tick()
	clock = clock.Add(24 * time.Hour)
	dir := t.TempDir()
	h.a.PollerStateDir = dir
	return h, &clock, dir
}

func writeHealth(t *testing.T, dir string, clock time.Time, lastOK time.Time, lines ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "last-ok"), []byte(lastOK.UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if lines != nil {
		if err := os.WriteFile(filepath.Join(dir, "health.log"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func marker(h *harness) string {
	b, _ := os.ReadFile(h.a.DigestMarker)
	return strings.TrimSpace(string(b))
}

func TestDigestUnchangedWhenHealthy(t *testing.T) {
	h, clock, dir := digestHarness(t, true)
	writeHealth(t, dir, *clock, clock.Add(-time.Minute))
	h.a.FailedPings = func() int { return 0 }
	h.tick()
	if len(h.poster.posts) != 1 || h.poster.posts[0] != healthDigestText {
		t.Fatalf("posts: %q", h.poster.posts)
	}
}

func TestDigestLeadsWithHealthLines(t *testing.T) {
	h, clock, dir := digestHarness(t, true)
	bad := clock.Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	writeHealth(t, dir, *clock, clock.Add(-time.Minute), bad+"\tpoll-failed\tHTTP 401")
	h.a.FailedPings = func() int { return 3 }
	h.tick()
	want := "poller: 1 failed poll in the last 24h (last: " + bad + " poll-failed HTTP 401)\n" +
		"agent: 3 failed heartbeat pings since the last digest\n" + healthDigestText
	if len(h.poster.posts) != 1 || h.poster.posts[0] != want {
		t.Fatalf("got  %q\nwant %q", h.poster.posts, want)
	}
	if marker(h) != clock.Format("2006-01-02") {
		t.Fatalf("marker %q", marker(h))
	}
}

func TestDigestAgentPingSingular(t *testing.T) {
	h, clock, dir := digestHarness(t, false)
	writeHealth(t, dir, *clock, clock.Add(-time.Minute))
	h.a.FailedPings = func() int { return 1 }
	h.tick()
	if len(h.poster.posts) != 1 || h.poster.posts[0] != "agent: 1 failed heartbeat ping since the last digest\n" {
		t.Fatalf("posts: %q", h.poster.posts)
	}
}

func TestQuietDayWithProblemPostsHealthOnly(t *testing.T) {
	h, clock, dir := digestHarness(t, false)
	old := clock.Add(-3 * time.Hour)
	writeHealth(t, dir, *clock, old)
	h.tick()
	want := "poller: no successful run since " + old.UTC().Format(time.RFC3339) + "\n"
	if len(h.poster.posts) != 1 || h.poster.posts[0] != want {
		t.Fatalf("got %q", h.poster.posts)
	}
	if marker(h) != clock.Format("2006-01-02") {
		t.Fatalf("marker %q", marker(h))
	}
	h.tick()
	if len(h.poster.posts) != 1 {
		t.Fatal("once per day")
	}
}

func TestQuietDayWithoutProblemPostsNothing(t *testing.T) {
	h, clock, dir := digestHarness(t, false)
	writeHealth(t, dir, *clock, clock.Add(-time.Minute))
	h.a.FailedPings = func() int { return 0 }
	h.tick()
	if len(h.poster.posts) != 0 {
		t.Fatalf("posts: %q", h.poster.posts)
	}
	if marker(h) != clock.Format("2006-01-02") {
		t.Fatalf("marker %q", marker(h))
	}
}

func TestDigestSaysWhenHealthIsUnreadable(t *testing.T) {
	h, clock, _ := digestHarness(t, false)
	h.a.PollerStateDir = filepath.Join(t.TempDir(), "missing")
	h.tick()
	if len(h.poster.posts) != 1 || !strings.HasPrefix(h.poster.posts[0], "poller health unknown: ") {
		t.Fatalf("posts: %q", h.poster.posts)
	}
	_ = clock
}

func TestFailedPingsResetsOnlyWhenDigestRuns(t *testing.T) {
	h, clock, dir := digestHarness(t, false)
	writeHealth(t, dir, *clock, clock.Add(-time.Minute))
	calls := 0
	h.a.FailedPings = func() int { calls++; return 0 }
	h.tick()
	h.tick() // marker written: not due again, so the count is left to accumulate
	if calls != 1 {
		t.Fatalf("FailedPings called %d times", calls)
	}
}
