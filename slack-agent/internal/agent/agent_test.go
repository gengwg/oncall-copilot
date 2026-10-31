package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/dcrequest"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/irm"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/ledger"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/oncall"
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
}

func (f *fakeIRM) Group(_ context.Context, id string) (irm.Group, error) {
	if f.groupErr != nil {
		return irm.Group{}, f.groupErr
	}
	st, ok := f.states[id]
	if !ok {
		st = "firing"
	}
	return irm.Group{ID: id, State: st}, nil
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
		Outbox: out, Store: store, Poster: h.poster, IRM: h.irm, Shifts: fakeShifts{shifts},
		Ledger: ledger.New(filepath.Join(dir, "dc.tsv")), Runner: h.runner,
		CollapseMax: 6, Now: func() time.Time { return now },
	}
	return h
}

func (h *harness) put(ev event.Event) {
	h.t.Helper()
	b, _ := json.Marshal(ev)
	if err := os.WriteFile(filepath.Join(h.outbox, ev.IRMID+".json"), b, 0o644); err != nil {
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
	return event.Event{IRMID: id, IRMNumber: num, IRMURL: "https://slack/p" + id, Alertname: "GPU Xid Critical",
		Severity: sev, Cluster: "c1", Node: "n-" + id, StartsAt: startsAt.Format(time.RFC3339),
		Fault: "real", Verdict: "hold", FailureMode: "hardware", Brief: "ALERT: GPU Xid Critical (" + sev + ")", TS: now.Format(time.RFC3339)}
}

func withDraft(e event.Event) event.Event {
	p := "/drafts/" + e.IRMID + ".md"
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
