package agent

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/dcrequest"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/irm"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/oncall"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/slackio"
)

func restartAgent(h *harness) {
	h.t.Helper()
	old := h.a
	h.a = &Agent{
		Outbox: old.Outbox, Store: old.Store, Poster: old.Poster, Pager: old.Pager,
		PagerName: old.PagerName, Shifts: old.Shifts, Ledger: old.Ledger, Runner: old.Runner,
		DryRun: old.DryRun, CollapseMax: old.CollapseMax, Now: old.Now, Log: old.Log,
	}
}

func TestAckReplyRetriesWithoutRepeatingPagerAck(t *testing.T) {
	h := newHarness(t, myShift)
	h.put(ev("G1", 1, "critical", now))
	h.poster.replyFailures = 1
	ctx := context.Background()

	if err := h.a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if h.poster.replyCalls != 1 || len(h.poster.replies) != 0 {
		t.Fatalf("first attempt: calls=%d replies=%q", h.poster.replyCalls, h.poster.replies)
	}
	restartAgent(h)
	if err := h.a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if h.poster.replyCalls != 2 || len(h.poster.replies) != 1 {
		t.Fatalf("retry: calls=%d replies=%q", h.poster.replyCalls, h.poster.replies)
	}
	if len(h.irm.acks) != 1 {
		t.Fatalf("reply retry repeated the pager ack: %v", h.irm.acks)
	}
	receipt := h.rec("G1").AckReply
	if receipt == nil || receipt.Attempts != 2 || receipt.SentAt == "" || receipt.LastError != "" {
		t.Fatalf("receipt: %+v", receipt)
	}
}

func TestAckAmbiguousReplyIsReconciledNotRepeated(t *testing.T) {
	h := newHarness(t, myShift)
	h.put(ev("G1", 1, "critical", now))
	h.poster.ambiguousReplyFailure = true
	ctx := context.Background()
	if err := h.a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if h.poster.replyCalls != 1 || len(h.poster.replies) != 1 {
		t.Fatalf("first attempt: calls=%d replies=%q", h.poster.replyCalls, h.poster.replies)
	}

	restartAgent(h)
	if err := h.a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if h.poster.replyCalls != 1 || len(h.poster.replies) != 1 {
		t.Fatalf("ambiguous delivery was repeated: calls=%d replies=%q", h.poster.replyCalls, h.poster.replies)
	}
	receipt := h.rec("G1").AckReply
	if receipt == nil || receipt.Attempts != 1 || receipt.SentAt == "" {
		t.Fatalf("reconciled receipt: %+v", receipt)
	}
}

func TestAckInFlightRecoversAcknowledgedPagerAfterHandoff(t *testing.T) {
	h := newHarness(t, theirShift)
	e := ev("G1", 1, "critical", now)
	h.irm.states = map[string]string{"G1": "acknowledged"}
	if err := h.a.Store.Save(&event.Record{ID: e.ID, Event: e, SlackTS: "ts1", PostAttempts: 1, AckInFlight: true}); err != nil {
		t.Fatal(err)
	}
	if err := h.a.HandleEvent(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	rec := h.rec("G1")
	if rec.AckedAt == "" || rec.AckInFlight || !rec.AckDecided {
		t.Fatalf("ack was not recovered: %+v", rec)
	}
	if rec.AckReply == nil || !strings.Contains(rec.AckReply.Text, "was already acknowledged") || strings.Contains(rec.AckReply.Text, "not acked") {
		t.Fatalf("recovery rewrote the ack as a refusal: %+v", rec.AckReply)
	}
	if len(h.irm.acks) != 0 {
		t.Fatalf("confirmed recovery issued another pager ack: %v", h.irm.acks)
	}
}

func TestManualAckCommandsDiscardPendingAutomaticReply(t *testing.T) {
	for _, tc := range []struct {
		name      string
		shifts    []oncall.Shift
		command   string
		forbidden string
	}{
		{name: "manual ack", shifts: theirShift, command: "ack", forbidden: "not acked:"},
		{name: "unack", shifts: myShift, command: "unack", forbidden: "acked IRM #1: your shift"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.shifts)
			h.put(ev("G1", 1, "critical", now))
			h.poster.replyFailures = 1
			ctx := context.Background()
			if err := h.a.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: h.rec("G1").SlackTS, Text: tc.command})
			calls := h.poster.replyCalls
			if err := h.a.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if h.poster.replyCalls != calls {
				t.Fatalf("superseded automatic reply was retried: %q", h.poster.replies)
			}
			for _, reply := range h.poster.replies {
				if strings.Contains(reply, tc.forbidden) {
					t.Fatalf("contradictory automatic reply: %q", reply)
				}
			}
			if receipt := h.rec("G1").AckReply; receipt != nil && receipt.SentAt == "" {
				t.Fatalf("pending automatic receipt: %+v", receipt)
			}
		})
	}
}

func TestDCRestartDrainsPersistedQueue(t *testing.T) {
	h := newHarness(t, myShift)
	h.put(withDraft(ev("G1", 1, "critical", now)))
	if err := h.a.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.rec("G1").DCStatus; got != "queued" {
		t.Fatalf("before restart: %q", got)
	}

	restartAgent(h)
	h.a.DrainDC(context.Background())
	if len(h.runner.calls) != 1 {
		t.Fatalf("persisted run was not recovered: calls=%v", h.runner.calls)
	}
	if got := h.rec("G1").DCStatus; got != "drafted" {
		t.Fatalf("after recovery: %q", got)
	}
}

func TestLegacyQueuedDCRecovers(t *testing.T) {
	h := newHarness(t, myShift)
	e := withDraft(ev("G1", 1, "critical", now))
	if err := h.a.Store.Save(&event.Record{ID: e.ID, Event: e, SlackTS: "ts1", AckDecided: true, DCStatus: "queued"}); err != nil {
		t.Fatal(err)
	}

	h.a.DrainDC(context.Background())
	if len(h.runner.calls) != 1 {
		t.Fatalf("legacy run was not recovered: calls=%v", h.runner.calls)
	}
	rec := h.rec("G1")
	if rec.DCAttempt != 1 || rec.DCStatus != "drafted" {
		t.Fatalf("legacy recovery: %+v", rec)
	}
}

func TestDCReplyRetriesWithoutRerunningAttempt(t *testing.T) {
	h := newHarness(t, myShift)
	h.put(withDraft(ev("G1", 1, "critical", now)))
	ctx := context.Background()
	if err := h.a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	h.poster.replyFailures = 1
	h.a.DrainDC(ctx)
	receipt := h.rec("G1").DCReply
	if len(h.runner.calls) != 1 || receipt == nil || receipt.SentAt != "" {
		t.Fatalf("first result delivery: calls=%v rec=%+v", h.runner.calls, h.rec("G1"))
	}

	restartAgent(h)
	if err := h.a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.runner.calls) != 1 {
		t.Fatalf("reply retry reran the DC attempt: %v", h.runner.calls)
	}
	receipt = h.rec("G1").DCReply
	if receipt == nil || receipt.Attempts != 2 || receipt.SentAt == "" {
		t.Fatalf("DC receipt: %+v", receipt)
	}
}

func TestReplyReconciliationMatchesDCGeneration(t *testing.T) {
	h := newHarness(t, myShift)
	h.put(withDraft(ev("G1", 1, "critical", now)))
	h.tick()
	ctx := context.Background()
	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: h.rec("G1").SlackTS, Text: "file again"})
	h.poster.replyFailures = 1
	h.a.DrainDC(ctx)
	if receipt := h.rec("G1").DCReply; receipt == nil || receipt.Attempts != 1 || receipt.SentAt != "" {
		t.Fatalf("before recovery: %+v", receipt)
	}

	if err := h.a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	receipt := h.rec("G1").DCReply
	if receipt == nil || receipt.Attempts != 2 || receipt.SentAt == "" {
		t.Fatalf("old generation satisfied the new receipt: %+v", receipt)
	}
	var results int
	for _, reply := range h.poster.replies {
		if strings.Contains(reply, "DC request drafted") {
			results++
		}
	}
	if results != 2 {
		t.Fatalf("identical result text from two generations: %q", h.poster.replies)
	}
}

func TestSentAndIgnoreDiscardPendingDCResult(t *testing.T) {
	for _, command := range []string{"sent", "ignore"} {
		t.Run(command, func(t *testing.T) {
			h := newHarness(t, myShift)
			h.put(withDraft(ev("G1", 1, "critical", now)))
			ctx := context.Background()
			if err := h.a.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			h.poster.replyFailures = 1
			h.a.DrainDC(ctx)
			h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: h.rec("G1").SlackTS, Text: command})
			calls := h.poster.replyCalls
			if err := h.a.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if h.poster.replyCalls != calls {
				t.Fatalf("superseded DC result was retried: %q", h.poster.replies)
			}
			for _, reply := range h.poster.replies {
				if strings.Contains(reply, "DC request drafted") {
					t.Fatalf("late draft result after %s: %q", command, reply)
				}
			}
			if receipt := h.rec("G1").DCReply; receipt != nil && receipt.SentAt == "" {
				t.Fatalf("pending DC receipt: %+v", receipt)
			}
		})
	}
}

func TestDCRestartKeepsForcedAttempt(t *testing.T) {
	h := newHarness(t, myShift)
	h.put(withDraft(ev("G1", 1, "critical", now)))
	h.tick()
	ctx := context.Background()
	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: h.rec("G1").SlackTS, Text: "file again"})
	if got := h.rec("G1").DCStatus; got != "queued" {
		t.Fatalf("before restart: %q", got)
	}

	restartAgent(h)
	h.a.DrainDC(ctx)
	if len(h.runner.calls) != 2 {
		t.Fatalf("forced run was not recovered: calls=%v", h.runner.calls)
	}
}

type blockingRunner struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan dcrequest.Result
}

func (r *blockingRunner) FileDCRequest(_ context.Context, _ event.Event) (dcrequest.Result, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call == 1 {
		close(r.started)
		return <-r.release, nil
	}
	return dcrequest.Result{Status: dcrequest.Drafted, Detail: "new"}, nil
}

type cancellationRunner struct{ started chan struct{} }

func (r cancellationRunner) FileDCRequest(ctx context.Context, _ event.Event) (dcrequest.Result, error) {
	close(r.started)
	<-ctx.Done()
	// Both production runners encode cancellation as a failed result, nil error.
	return dcrequest.Result{Status: dcrequest.Failed, Detail: "timed out after 10m0s"}, nil
}

func TestDCCancellationLeavesAttemptForRecovery(t *testing.T) {
	h := newHarness(t, myShift)
	h.put(withDraft(ev("G1", 1, "critical", now)))
	if err := h.a.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner := cancellationRunner{started: make(chan struct{})}
	h.a.Runner = runner
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.a.DrainDC(ctx)
		close(done)
	}()
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("DC run did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled DC drain did not finish")
	}
	if got := h.rec("G1").DCStatus; got != "running" {
		t.Fatalf("cancelled attempt became terminal: %q", got)
	}

	restartAgent(h)
	h.a.Runner = h.runner
	h.a.DrainDC(context.Background())
	if len(h.runner.calls) != 1 || h.rec("G1").DCStatus != "drafted" {
		t.Fatalf("recovery: calls=%v rec=%+v", h.runner.calls, h.rec("G1"))
	}
}

func TestStaleDCCompletionDoesNotOverwriteNewAttempt(t *testing.T) {
	h := newHarness(t, myShift)
	h.put(withDraft(ev("G1", 1, "critical", now)))
	ctx := context.Background()
	if err := h.a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan dcrequest.Result, 1)}
	h.a.Runner = runner
	done := make(chan struct{})
	go func() {
		h.a.DrainDC(ctx)
		close(done)
	}()
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first DC run did not start")
	}

	h.a.HandleInbound(ctx, slackio.Inbound{ThreadTS: h.rec("G1").SlackTS, Text: "file again"})
	runner.release <- dcrequest.Result{Status: dcrequest.Drafted, Detail: "old"}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DC drain did not finish")
	}

	runner.mu.Lock()
	calls := runner.calls
	runner.mu.Unlock()
	if calls != 2 {
		t.Fatalf("calls=%d, want old and new attempts", calls)
	}
	if got := h.rec("G1").DCDetail; got != "new" {
		t.Fatalf("stale completion won: detail=%q", got)
	}
	var newReplies int
	for _, reply := range h.poster.replies {
		if strings.Contains(reply, "DC request drafted") && strings.Contains(reply, "old") {
			t.Fatalf("stale completion announced its result: %q", reply)
		}
		if strings.Contains(reply, "DC request drafted") && strings.Contains(reply, "new") {
			newReplies++
		}
	}
	if newReplies != 1 {
		t.Fatalf("new result replies=%d, all replies=%q", newReplies, h.poster.replies)
	}
	ledgerBytes, err := os.ReadFile(h.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(ledgerBytes)), "\n"); len(lines) != 1 {
		t.Fatalf("stale completion also wrote the ledger: %s", ledgerBytes)
	}
}

func TestFreshRecordSaveDoesNotOverwriteCommand(t *testing.T) {
	h := newHarness(t, myShift)
	e := withDraft(ev("G1", 1, "critical", now))
	existing := &event.Record{ID: e.ID, Event: e, SlackTS: "u1", AckDecided: true, DCStatus: "queued", DCAttempt: 1, DCForce: true}
	if err := h.a.Store.Save(existing); err != nil {
		t.Fatal(err)
	}
	fresh := &event.Record{ID: e.ID, Event: e, Deferred: true, AckDecided: true}
	if err := h.a.saveRecordIfAbsent(fresh); err != nil {
		t.Fatal(err)
	}
	rec := h.rec("G1")
	if rec.SlackTS != "u1" || rec.DCStatus != "queued" || rec.DCAttempt != 1 || !rec.DCForce || rec.Deferred {
		t.Fatalf("fresh record replaced command state: %+v", rec)
	}
}

func TestDigestMergesWithConcurrentDCState(t *testing.T) {
	h, _, _ := digestHarness(t, true)
	h.irm.findable = map[string]irm.Group{"#1": {ID: "G1", Number: 1, State: "firing"}}
	h.poster.postStarted = make(chan struct{})
	h.poster.postRelease = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- h.a.Tick(context.Background()) }()
	select {
	case <-h.poster.postStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("digest post did not start")
	}

	h.a.HandleInbound(context.Background(), slackio.Inbound{TS: "u1", Text: "file #1"})
	close(h.poster.postRelease)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("digest did not finish")
	}

	rec := h.rec("G1")
	if !rec.Digested || rec.SlackTS != "u1" || rec.DCStatus != "queued" || rec.DCAttempt != 1 {
		t.Fatalf("digest overwrote concurrent state: %+v", rec)
	}
	h.a.DrainDC(context.Background())
	if got := h.rec("G1").DCStatus; got != "drafted" {
		t.Fatalf("recovered queued state: %q", got)
	}
}
