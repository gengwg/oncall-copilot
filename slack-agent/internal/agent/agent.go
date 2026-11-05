// Package agent sequences what happens to one brief: post it to the owner,
// decide whether to ack, and hand hardware faults to the DC request runner.
// Automatic work and delivery intent are saved before the next side effect.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/dcrequest"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/health"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/ledger"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/notes"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/oncall"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/outbox"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/pager"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/slackio"
)

// Pager is the paging service the agent acks in. Errors that mean "no such
// alert" and "already acked" must satisfy errors.Is against the pager package's
// sentinels.
type Pager interface {
	Get(ctx context.Context, id string) (pager.Alert, error)
	Find(ctx context.Context, ref string) (pager.Alert, error) // "#N", "N", or a link the pager understands
	Acknowledge(ctx context.Context, id string) error
	Unacknowledge(ctx context.Context, id string) error
}

type Shifts interface {
	Mine(at time.Time) (oncall.Shift, bool)
	Current(at time.Time) (oncall.Shift, bool)
}

type Ledger interface {
	Recent(cluster, node string, since time.Time) (ledger.Entry, bool)
	Append(e ledger.Entry) error
}

const (
	maxPostAttempts = 3
	collapseWindow  = 5 * time.Minute
	ledgerWindow    = 14 * 24 * time.Hour
	// A follow-up or an already-active ticket is the owner's roughly weekly
	// check-in, so those results settle a node for less than a new request.
	ticketedWindow = 7 * 24 * time.Hour
	// A skill run that found the existing request is not worth repeating on
	// `file` for a day; `file again` repeats it anyway.
	alreadySentFresh = 24 * time.Hour
)

type Agent struct {
	Outbox      string
	Store       *event.Store
	Poster      slackio.Poster
	Pager       Pager
	PagerName   string // selects the label in operator-facing text; "" is irm
	Shifts      Shifts
	Ledger      Ledger
	Runner      dcrequest.Runner
	DryRun      bool
	CollapseMax int
	Now         func() time.Time
	Log         *log.Logger
	// Off-shift briefs are not DMed; they wait for one digest per day at
	// DigestAt (HH:MM in DigestTZ). DigestMarker is the file holding the date
	// of the last digest. Empty DigestAt disables deferral: every brief is DMed.
	DigestAt     string
	DigestTZ     *time.Location
	DigestMarker string
	// PollerStateDir holds the poller's health files; "" leaves poller health
	// out of the digest. FailedPings and SkippedPings return and reset the
	// counts of this agent's failed and skipped (tick error) heartbeat pings;
	// nil means none. RestorePings puts them back when the digest post fails, so
	// a retry still reports them. The counts live in memory, so a restart
	// forgets them: the digest undercounts, never overcounts.
	PollerStateDir string
	FailedPings    func() int
	SkippedPings   func() int
	RestorePings   func(failed, skipped int)
	// Notes holds the owner's `note` replies per node; the poller feeds them
	// into later briefs on that node.
	Notes notes.Store

	recordMu sync.Mutex
	dcRunMu  sync.Mutex
	dcWake   chan struct{}
	dcOnce   sync.Once
}

func (a *Agent) label() string { return pager.Label(a.PagerName) }

type dcJob struct {
	id      string
	attempt uint64
}

func (a *Agent) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Agent) logf(format string, args ...any) {
	if a.Log != nil {
		a.Log.Printf(format, args...)
	}
}

// Tick scans the outbox once and handles every event that still has work.
func (a *Agent) Tick(ctx context.Context) error {
	a.flushAllReplies(ctx)
	events, scanErr := outbox.Scan(a.Outbox)
	var fresh []event.Event
	var pending []event.Event
	for _, ev := range events {
		rec, err := a.Store.Get(ev.ID)
		if err != nil {
			return err
		}
		switch {
		case rec == nil:
			fresh = append(fresh, ev)
		case !rec.PostFailed && !rec.Deferred && (rec.SlackTS == "" || !rec.AckDecided || a.wantsDC(rec)):
			pending = append(pending, ev)
		}
	}
	if a.DigestAt != "" {
		if _, mine := a.Shifts.Mine(a.now()); !mine {
			// Someone else's shift: hold the briefs for the digest. Nothing is
			// acked for them anyway, and the pager pages the on-call as usual.
			for _, ev := range fresh {
				rec := &event.Record{ID: ev.ID, Event: ev, Deferred: true, AckDecided: true,
					DeferredAt: a.now().UTC().Format(time.RFC3339),
					AckNote:    "not acked: not your shift; held for the morning digest"}
				if err := a.saveRecordIfAbsent(rec); err != nil {
					return err
				}
			}
			fresh = nil
		}
		if err := a.digest(ctx); err != nil {
			a.logf("digest: %v", err)
		}
	}
	if a.CollapseMax > 0 && len(fresh) > 0 {
		recent, err := a.Store.PostedSince(a.now().Add(-collapseWindow))
		if err != nil {
			return err
		}
		if recent+len(fresh) > a.CollapseMax {
			if err := a.collapse(ctx, fresh); err != nil {
				return err
			}
			fresh = nil
		}
	}
	for _, ev := range append(fresh, pending...) {
		if err := a.HandleEvent(ctx, ev); err != nil {
			a.logf("event %s: %v", ev.ID, err)
		}
	}
	return scanErr
}

func (a *Agent) wantsDC(rec *event.Record) bool {
	return rec.Event.DCDraft != nil && rec.DCStatus == ""
}

// saveRecordIfAbsent preserves a command that attached an event after Tick
// classified it as fresh. Existing state is never replaced by that stale view.
func (a *Agent) saveRecordIfAbsent(rec *event.Record) error {
	a.recordMu.Lock()
	defer a.recordMu.Unlock()
	current, err := a.Store.Get(rec.ID)
	if err != nil {
		return err
	}
	if current != nil {
		return nil
	}
	return a.Store.Save(rec)
}

// digest posts, once per day after DigestAt, everything deferred since the
// last digest. Quiet days post nothing; the heartbeat says the agent is alive.
func (a *Agent) digest(ctx context.Context) error {
	now := a.now()
	loc := a.DigestTZ
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	at, err := time.ParseInLocation("15:04", a.DigestAt, loc)
	if err != nil {
		return fmt.Errorf("DigestAt %q: %w", a.DigestAt, err)
	}
	due := time.Date(local.Year(), local.Month(), local.Day(), at.Hour(), at.Minute(), 0, 0, loc)
	if local.Before(due) {
		return nil
	}
	today := local.Format("2006-01-02")
	if b, err := os.ReadFile(a.DigestMarker); err == nil && strings.TrimSpace(string(b)) == today {
		return nil
	}
	all, err := a.Store.Undigested()
	if err != nil {
		return err
	}
	// Only what was deferred before today's digest time. Anything held after
	// it belongs to tomorrow's, so a late start never flushes the day's briefs.
	var recs []event.Record
	for _, r := range all {
		t, err := time.Parse(time.RFC3339, r.DeferredAt)
		if err == nil && t.Before(due) {
			recs = append(recs, r)
		}
	}
	healthText, failed, skipped := a.healthLines(now)
	restore := func() {
		if a.RestorePings != nil {
			a.RestorePings(failed, skipped)
		}
	}
	if len(recs) > 0 {
		var b strings.Builder
		b.WriteString(healthText)
		fmt.Fprintf(&b, "*Overnight digest: %d brief(s) on other shifts, none acked by me.*\n", len(recs))
		for _, r := range recs {
			ev := r.Event
			fmt.Fprintf(&b, "• %s #%s %s (%s) %s/%s · verdict %s · fault %s", pager.Label(ev.Pager), ev.Ref, ev.Alertname, ev.Severity, orDash(ev.Cluster), orDash(ev.Node), ev.Verdict, ev.Fault)
			if ev.DCDraft != nil {
				fmt.Fprintf(&b, " · DC draft ready: reply `file #%s`", ev.Ref)
			}
			if ev.URL != "" {
				b.WriteString(" · " + ev.URL)
			}
			b.WriteString("\n")
		}
		if _, err := a.Poster.PostDM(ctx, b.String()); err != nil {
			restore()
			return fmt.Errorf("post: %w", err)
		}
		for i := range recs {
			// The digest post may overlap a command or DC completion. Merge only
			// this fact into the current record instead of saving our stale copy.
			a.recordMu.Lock()
			rec, err := a.Store.Get(recs[i].ID)
			if err == nil && rec != nil {
				rec.Digested = true
				err = a.Store.Save(rec)
			}
			a.recordMu.Unlock()
			if err != nil {
				return err
			}
		}
	} else if healthText != "" {
		if _, err := a.Poster.PostDM(ctx, "*Health:*\n"+healthText); err != nil {
			restore()
			return fmt.Errorf("post: %w", err)
		}
	}
	return os.WriteFile(a.DigestMarker, []byte(today+"\n"), 0o600)
}

// healthLines is the digest's problem report, each line newline-terminated,
// or "" when neither the poller nor this agent's heartbeat had trouble. It also
// returns the agent ping counts it consumed, for the caller to restore if the
// report is not delivered.
func (a *Agent) healthLines(now time.Time) (text string, failed, skipped int) {
	var b strings.Builder
	if a.PollerStateDir != "" {
		if s := health.Poller(a.PollerStateDir, now); s != "" {
			b.WriteString(s + "\n")
		}
	}
	if a.FailedPings != nil {
		failed = a.FailedPings()
	}
	if a.SkippedPings != nil {
		skipped = a.SkippedPings()
	}
	var parts []string
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", skipped))
	}
	if len(parts) > 0 {
		// The noun agrees with the count next to it.
		last := failed
		if skipped > 0 {
			last = skipped
		}
		noun := "pings"
		if last == 1 {
			noun = "ping"
		}
		fmt.Fprintf(&b, "agent: %s heartbeat %s since the last digest\n", strings.Join(parts, " and "), noun)
	}
	return b.String(), failed, skipped
}

// collapse posts one summary for a burst and marks each as reported but not
// acked: acked means individually reported, and a summary is not that.
func (a *Agent) collapse(ctx context.Context, evs []event.Event) error {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d briefs in the last %s; summarised, none acked.*\n", len(evs), collapseWindow)
	for _, ev := range evs {
		fmt.Fprintf(&b, "• %s #%s %s (%s) %s/%s verdict %s fault %s\n", pager.Label(ev.Pager), ev.Ref, ev.Alertname, ev.Severity, ev.Cluster, ev.Node, ev.Verdict, ev.Fault)
	}
	ts, err := a.Poster.PostDM(ctx, b.String())
	if err != nil {
		return fmt.Errorf("collapse post: %w", err)
	}
	now := a.now().UTC().Format(time.RFC3339)
	for _, ev := range evs {
		rec := &event.Record{ID: ev.ID, Event: ev, SlackTS: ts, PostedAt: now, PostAttempts: 1,
			Collapsed: true, AckDecided: true, AckNote: "not acked: part of a burst summary"}
		if err := a.saveRecordIfAbsent(rec); err != nil {
			return err
		}
	}
	return nil
}

// HandleEvent moves one event forward as far as it can this tick.
func (a *Agent) HandleEvent(ctx context.Context, ev event.Event) error {
	a.recordMu.Lock()
	defer a.recordMu.Unlock()
	rec, err := a.Store.Get(ev.ID)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = &event.Record{ID: ev.ID, Event: ev}
	}
	if rec.PostFailed {
		return nil
	}
	if rec.SlackTS == "" {
		if rec.PostAttempts >= maxPostAttempts {
			rec.PostFailed = true
			a.logf("event %s: giving up posting after %d attempts; not acked", ev.ID, rec.PostAttempts)
			return a.Store.Save(rec)
		}
		rec.PostAttempts++
		ts, err := a.Poster.PostDM(ctx, Render(ev))
		if err != nil {
			if serr := a.Store.Save(rec); serr != nil {
				return fmt.Errorf("post: %v; and saving the attempt: %w", err, serr)
			}
			return fmt.Errorf("post: %w", err)
		}
		rec.SlackTS = ts
		rec.PostedAt = a.now().UTC().Format(time.RFC3339)
		if err := a.Store.Save(rec); err != nil {
			return err
		}
	}
	if !rec.AckDecided {
		decision, note := a.ackGate(ctx, ev, rec)
		switch decision {
		case ackRetry:
			a.logf("event %s: %s; retrying next tick", ev.ID, note)
			return nil
		case ackConfirm:
			rec.AckedAt = a.now().UTC().Format(time.RFC3339)
			note = fmt.Sprintf("%s #%s was already acknowledged", a.label(), ev.Ref)
		case ackYes:
			if a.DryRun {
				note = "would ack (dry run): your shift, brief delivered"
				break
			}
			rec.AckInFlight = true
			if err := a.Store.Save(rec); err != nil {
				return err
			}
			err := a.Pager.Acknowledge(ctx, ev.ID)
			if err != nil && !errors.Is(err, pager.ErrAlreadyAcked) {
				a.logf("event %s: ack failed: %v; retrying next tick", ev.ID, err)
				return nil
			}
			rec.AckedAt = a.now().UTC().Format(time.RFC3339)
			note = fmt.Sprintf("acked %s #%s: your shift, brief delivered", a.label(), ev.Ref)
			if errors.Is(err, pager.ErrAlreadyAcked) {
				note = fmt.Sprintf("%s #%s was already acknowledged", a.label(), ev.Ref)
			}
		}
		rec.AckInFlight = false
		rec.AckDecided = true
		rec.AckNote = note
		rec.AckReply = &event.ReplyReceipt{ID: "ack", ThreadTS: rec.SlackTS, Text: note}
		if err := a.Store.Save(rec); err != nil {
			return err
		}
	}
	if hasPendingReply(rec) {
		if err := a.flushRepliesLocked(ctx, rec); err != nil {
			a.logf("event %s: reply failed: %v", ev.ID, err)
		}
	}
	if a.wantsDC(rec) {
		if err := a.queueDC(rec, false); err != nil {
			return err
		}
	}
	return nil
}

type ackDecision int

const (
	ackNo ackDecision = iota
	ackYes
	ackRetry
	ackConfirm
)

// ackGate is the whole policy. Every "no" is explained in the thread.
func (a *Agent) ackGate(ctx context.Context, ev event.Event, rec *event.Record) (ackDecision, string) {
	if rec.AckInFlight {
		// This attempt already passed the gate. Recover it from pager state rather
		// than applying a new shift or schedule after a restart.
		g, err := a.Pager.Get(ctx, ev.ID)
		if err != nil {
			return ackRetry, a.label() + " lookup failed: " + err.Error()
		}
		switch g.State {
		case "acknowledged":
			return ackConfirm, ""
		case "firing":
			return ackYes, ""
		default:
			return ackNo, "not acked: already " + g.State
		}
	}
	if rec.SlackTS == "" || rec.Collapsed {
		return ackNo, "not acked: brief not individually delivered"
	}
	if ev.Severity != "critical" && ev.Severity != "warning" {
		return ackNo, "not acked: severity " + ev.Severity + " does not page"
	}
	if ev.Degraded != nil {
		return ackNo, "not acked: brief degraded (" + *ev.Degraded + ")"
	}
	// An ack a minute or two in cuts the owner's own push, SMS and call steps.
	// Only a brief that says this can wait earns that; a page keeps escalating.
	if ev.Verdict != "hold" {
		return ackNo, "not acked: verdict " + ev.Verdict + ", " + a.label() + " keeps paging you"
	}
	now := a.now()
	mine, ok := a.Shifts.Mine(now)
	if !ok {
		if cur, ok := a.Shifts.Current(now); ok {
			return ackNo, fmt.Sprintf("not acked: on call is %s until %s", cur.UserEmail, cur.End.In(time.Local).Format("Mon 15:04 MST"))
		}
		return ackNo, "not acked: on-call schedule unavailable"
	}
	start, ok := ev.StartTime()
	if !ok {
		return ackNo, "not acked: alert start time unknown"
	}
	if !mine.Covers(start) {
		return ackNo, "not acked: alert fired at " + start.In(time.Local).Format("Mon 15:04 MST") + ", before your shift"
	}
	g, err := a.Pager.Get(ctx, ev.ID)
	if err != nil {
		return ackRetry, a.label() + " lookup failed: " + err.Error()
	}
	if g.State != "firing" {
		return ackNo, "not acked: already " + g.State
	}
	return ackYes, ""
}

// Render is the DM for one brief: the facts first, the brief under them.
func Render(ev event.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%s #%s %s* (%s) %s / %s\n", pager.Label(ev.Pager), ev.Ref, ev.Alertname, ev.Severity, orDash(ev.Cluster), orDash(ev.Node))
	fmt.Fprintf(&b, "verdict %s · fault %s · failure mode %s", ev.Verdict, ev.Fault, ev.FailureMode)
	if ev.Degraded != nil {
		fmt.Fprintf(&b, " · degraded: %s", *ev.Degraded)
	}
	if ev.DCDraft != nil {
		b.WriteString(" · DC draft pending")
	}
	b.WriteString("\n")
	if ev.URL != "" {
		b.WriteString(ev.URL + "\n")
	}
	brief := strings.TrimSpace(ev.Brief)
	if brief == "" {
		brief = "(no brief)"
	}
	if len(brief) > 2500 {
		brief = brief[:2500] + "\n…"
	}
	fmt.Fprintf(&b, "```%s```", brief)
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// --- DC requests: one worker, so two alerts on one node cannot both draft.

const dcRetryInterval = 10 * time.Second

func dcPending(status string) bool { return status == "queued" || status == "running" }

// queueDC persists the queue entry before waking the worker. The caller holds
// recordMu; the record, not the wake-up channel, owns the job.
func (a *Agent) queueDC(rec *event.Record, force bool) error {
	rec.DCAttempt++
	rec.DCStatus = "queued"
	rec.DCForce = force
	clearPendingDCReply(rec)
	if err := a.Store.Save(rec); err != nil {
		return err
	}
	a.wakeDC()
	return nil
}

func (a *Agent) wakeDC() {
	a.dcOnce.Do(func() { a.dcWake = make(chan struct{}, 1) })
	select {
	case a.dcWake <- struct{}{}:
	default:
	}
}

// RunDCWorker recovers persisted attempts at startup, then drains on wake-ups
// and retries interrupted store or runner transitions on a bounded interval.
func (a *Agent) RunDCWorker(ctx context.Context) {
	a.dcOnce.Do(func() { a.dcWake = make(chan struct{}, 1) })
	ticker := time.NewTicker(dcRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		a.DrainDC(ctx)
		select {
		case <-ctx.Done():
			return
		case <-a.dcWake:
		case <-ticker.C:
		}
	}
}

// DrainDC runs persisted jobs synchronously; for tests and for shutdown. A
// failed transition remains queued for the worker's next interval.
func (a *Agent) DrainDC(ctx context.Context) {
	a.dcRunMu.Lock()
	defer a.dcRunMu.Unlock()
	for {
		recs, err := a.Store.All()
		if err != nil {
			a.logf("dc queue: %v", err)
			return
		}
		var next *dcJob
		for i := range recs {
			if dcPending(recs[i].DCStatus) {
				next = &dcJob{id: recs[i].ID, attempt: recs[i].DCAttempt}
				break
			}
		}
		if next == nil {
			return
		}
		if err := a.runDC(ctx, *next); err != nil {
			a.logf("dc %s: %v", next.id, err)
			return
		}
	}
}

func (a *Agent) runDC(ctx context.Context, j dcJob) error {
	a.recordMu.Lock()
	rec, err := a.Store.Get(j.id)
	if err != nil {
		a.recordMu.Unlock()
		return err
	}
	if rec == nil {
		a.recordMu.Unlock()
		return fmt.Errorf("record %s not found", j.id)
	}
	if rec.DCAttempt == 0 && j.attempt == 0 {
		// Records queued by the previous version had no generation or force bit.
		rec.DCAttempt = 1
		j.attempt = 1
	}
	if rec.DCAttempt != j.attempt || !dcPending(rec.DCStatus) {
		a.recordMu.Unlock()
		return nil
	}
	rec.DCStatus = "running"
	if err := a.Store.Save(rec); err != nil {
		a.recordMu.Unlock()
		return err
	}
	force, ev := rec.DCForce, rec.Event
	a.recordMu.Unlock()

	now := a.now()
	if !force {
		if e, ok := a.Ledger.Recent(ev.Cluster, ev.Node, now.Add(-ledgerWindow)); ok && !ticketedAndStale(e, now) {
			msg := fmt.Sprintf("DC request not drafted: %s/%s already %s on %s (%s %s). Reply `file` to draft anyway.",
				ev.Cluster, ev.Node, e.Status, e.TS.In(time.Local).Format("Jan 2"), a.label(), e.ID)
			return a.finishDC(ctx, j, "skipped_recent", msg, nil, nil)
		}
	}
	res, err := a.Runner.FileDCRequest(ctx, ev)
	if ctx.Err() != nil && (err != nil || res.Status == dcrequest.Failed) {
		// Both production runners report cancellation as Failed with a nil error.
		// Leave the attempt running for startup recovery; a completed draft stands.
		return ctx.Err()
	}
	if err != nil {
		res = dcrequest.Result{Status: dcrequest.Failed, Detail: err.Error()}
	}
	var status, msg string
	switch res.Status {
	case dcrequest.Drafted:
		status = "drafted"
		msg = fmt.Sprintf("DC request drafted for %s/%s in %s. Review and send it from your Slack drafts. Reply `ignore` if it should not go.", ev.Cluster, ev.Node, orDash(res.Detail))
	case dcrequest.FollowupDrafted:
		status = "followup_drafted"
		msg = fmt.Sprintf("Follow-up drafted in the existing thread for %s/%s: %s. Review and send it from your Slack drafts.", ev.Cluster, ev.Node, orDash(res.Detail))
	case dcrequest.AlreadySent:
		status = "already_sent"
		msg = fmt.Sprintf("DC request for %s/%s already exists: %s. Nothing drafted.", ev.Cluster, ev.Node, orDash(res.Detail))
	default:
		status = "failed"
		msg = fmt.Sprintf("DC request not drafted: %s. Draft file: %s. Manual: /file-dc-request %s", res.Detail, deref(ev.DCDraft), ev.URL)
	}
	// The run is a headless Claude session; its price is part of the result.
	if res.CostUSD > 0 {
		msg += fmt.Sprintf(" (run cost $%.2f)", res.CostUSD)
	}
	if res.Via != "" && res.Status != dcrequest.Failed {
		msg += " Claude Code was at its spend limit, so " + res.Via + " ran the skill."
	}
	a.logf("dc %s: %s, cost $%.2f%s", ev.ID, status, res.CostUSD, viaNote(res.Via))
	entry := &ledger.Entry{TS: now, Cluster: ev.Cluster, Node: ev.Node, ID: ev.ID, Status: status}
	return a.finishDC(ctx, j, status, msg, &res, entry)
}

// finishDC rejects a completion after a newer command or status change has
// superseded it. The runner's external side effects cannot be undone, but the
// old result cannot overwrite current state or announce itself as current.
func (a *Agent) finishDC(ctx context.Context, j dcJob, status, msg string, res *dcrequest.Result, entry *ledger.Entry) error {
	a.recordMu.Lock()
	defer a.recordMu.Unlock()
	rec, err := a.Store.Get(j.id)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("record %s not found", j.id)
	}
	if rec.DCAttempt != j.attempt || rec.DCStatus != "running" {
		a.logf("dc %s attempt %d: discarding stale completion", j.id, j.attempt)
		return nil
	}
	rec.DCStatus = status
	if res != nil {
		rec.DCDetail = res.Detail
	}
	rec.DCReply = &event.ReplyReceipt{ID: fmt.Sprintf("dc:%d", j.attempt), ThreadTS: rec.SlackTS, Text: msg, Generation: j.attempt}
	if err := a.Store.Save(rec); err != nil {
		return err
	}
	// The terminal result is durable before this advisory suppression record.
	// A ledger failure may repeat work on a later alert, never this result reply.
	if entry != nil {
		if err := a.Ledger.Append(*entry); err != nil {
			a.logf("dc %s: ledger write failed, the next alert on %s may draft again: %v", rec.ID, rec.Event.Node, err)
		}
	}
	return a.flushRepliesLocked(ctx, rec)
}

func viaNote(via string) string {
	if via == "" {
		return ""
	}
	return ", via " + via
}

// freshAlreadySent reports the skill's own finding from the last day that a
// request for this node already exists, so `file` can repeat it instead of
// paying for the same check again.
func (a *Agent) freshAlreadySent(rec *event.Record) (string, bool) {
	ev := rec.Event
	now := a.now()
	e, ok := a.Ledger.Recent(ev.Cluster, ev.Node, now.Add(-alreadySentFresh))
	if !ok || e.Status != "already_sent" {
		return "", false
	}
	detail := rec.DCDetail
	if e.ID != rec.ID {
		if other, err := a.Store.Get(e.ID); err == nil && other != nil {
			detail = other.DCDetail
		}
	}
	return fmt.Sprintf("DC request for %s/%s already exists: %s (checked %s, %s %s). Nothing drafted. Reply `file again` to check again.",
		ev.Cluster, ev.Node, orDash(detail), e.TS.In(time.Local).Format("Jan 2 15:04"), a.label(), e.ID), true
}

func ticketedAndStale(e ledger.Entry, now time.Time) bool {
	return (e.Status == "followup_drafted" || e.Status == "followup_sent" || e.Status == "already_sent") && e.TS.Before(now.Add(-ticketedWindow))
}

func deref(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

// --- Replies from the owner.

const help = "Reply in a brief's thread with `file` (draft the DC request now; `file again` to repeat a fresh check), `sent` (you sent the draft), `ignore` (never for this node for 14 days), `ack`, `unack`, `note <what you found>` (later briefs on this node include it; `note` alone lists them); or send `file #5474` / `file <alert link>` (also `sent`, `ack`, `unack`, `ignore`, or the bare link to attach it) as a new message for any alert; `status` anywhere."

// slackLink strips Slack's <url|label> wrapping from a pasted link.
var slackLink = regexp.MustCompile(`<([^|>]+)(?:\|[^>]*)?>`)

// HandleInbound acts on one message from the owner.
func (a *Agent) HandleInbound(ctx context.Context, in slackio.Inbound) {
	a.recordMu.Lock()
	defer a.recordMu.Unlock()
	text := strings.TrimSpace(slackLink.ReplaceAllString(in.Text, "$1"))
	cmd, ref, _ := strings.Cut(text, " ")
	cmd = strings.ToLower(cmd)
	ref = strings.TrimSpace(ref)
	// A bare alert link or number, with no command, attaches that alert here.
	if in.ThreadTS == "" && ref == "" && (strings.HasPrefix(cmd, "http") || (strings.HasPrefix(cmd, "#") && len(cmd) > 1)) {
		cmd, ref = "attach", text
	}
	again := false
	if cmd == "file" {
		if rest, ok := strings.CutPrefix(strings.ToLower(ref), "again"); ok && (rest == "" || rest[0] == ' ') {
			again, ref = true, strings.TrimSpace(ref[len("again"):])
		}
	}
	reply := func(ts, text string) {
		if err := a.Poster.Reply(ctx, ts, text); err != nil {
			a.logf("reply: %v", err)
		}
	}
	save := func(r *event.Record) bool {
		if err := a.Store.Save(r); err != nil {
			a.logf("record %s: save failed: %v", r.ID, err)
			return false
		}
		return true
	}
	threadTS := in.ThreadTS
	var rec *event.Record
	switch {
	case threadTS == "" && ref != "" && (cmd == "attach" || cmd == "file" || cmd == "sent" || cmd == "ignore" || cmd == "ack" || cmd == "unack"):
		// A command about any alert: attach the group to this message's thread.
		al, err := a.Pager.Find(ctx, ref)
		if err != nil {
			reply(in.TS, "Could not find that alert: "+err.Error())
			return
		}
		threadTS = in.TS
		rec, err = a.Store.ByThread(threadTS)
		if err == nil && rec == nil {
			if rec, err = a.Store.Get(al.ID); err == nil && rec == nil {
				rec = &event.Record{ID: al.ID, Event: eventFromAlert(a.PagerName, al), SlackTS: threadTS,
					PostedAt: a.now().UTC().Format(time.RFC3339), PostAttempts: 1,
					AckDecided: true, AckNote: "attached by hand; no automatic ack"}
			}
		}
		if err != nil || rec == nil {
			reply(in.TS, "Could not attach the alert: "+fmt.Sprint(err))
			return
		}
		if rec.SlackTS == "" {
			rec.SlackTS = threadTS
		}
		save(rec)
	case threadTS == "":
		switch cmd {
		case "status":
			reply(in.TS, a.status(ctx))
		default:
			reply(in.TS, help)
		}
		return
	default:
		var err error
		rec, err = a.Store.ByThread(threadTS)
		if err != nil || rec == nil {
			reply(threadTS, "No alert is attached to this thread. "+help)
			return
		}
	}
	ev := rec.Event
	in.ThreadTS = threadTS
	switch cmd {
	case "file":
		if !again {
			if dcPending(rec.DCStatus) {
				reply(in.ThreadTS, fmt.Sprintf("A DC run for %s/%s is already queued; its result will appear here. Reply `file again` to queue another.", ev.Cluster, ev.Node))
				return
			}
			if msg, ok := a.freshAlreadySent(rec); ok {
				reply(in.ThreadTS, msg)
				return
			}
		}
		if err := a.queueDC(rec, true); err != nil {
			reply(in.ThreadTS, "Could not queue the DC request: "+err.Error())
			return
		}
		reply(in.ThreadTS, fmt.Sprintf("Drafting the DC request for %s/%s now; this takes a few minutes.", ev.Cluster, ev.Node))
	case "sent":
		// A sent follow-up stays on the 7-day rhythm; only a fresh request earns 14 days.
		status, days := "sent", 14
		if rec.DCStatus == "followup_drafted" {
			status, days = "followup_sent", int(ticketedWindow/(24*time.Hour))
		}
		if err := a.Ledger.Append(ledger.Entry{TS: a.now(), Cluster: ev.Cluster, Node: ev.Node, ID: ev.ID, Status: status}); err != nil {
			reply(in.ThreadTS, "Could not record it as sent: "+err.Error())
			return
		}
		rec.DCStatus = status
		clearPendingDCReply(rec)
		if !save(rec) {
			return
		}
		reply(in.ThreadTS, fmt.Sprintf("Noted: DC request for %s/%s sent. No new draft for this node for %d days unless you reply `file`.", ev.Cluster, ev.Node, days))
	case "ignore":
		if err := a.Ledger.Append(ledger.Entry{TS: a.now(), Cluster: ev.Cluster, Node: ev.Node, ID: ev.ID, Status: "ignored"}); err != nil {
			reply(in.ThreadTS, "Could not record the ignore: "+err.Error())
			return
		}
		rec.DCStatus = "ignored"
		clearPendingDCReply(rec)
		if !save(rec) {
			return
		}
		reply(in.ThreadTS, fmt.Sprintf("Noted: no DC request for %s/%s for 14 days unless you reply `file`.", ev.Cluster, ev.Node))
	case "ack":
		if a.DryRun {
			reply(in.ThreadTS, "Dry run: would ack on your instruction.")
			return
		}
		err := a.Pager.Acknowledge(ctx, rec.ID)
		if err != nil && !errors.Is(err, pager.ErrAlreadyAcked) {
			reply(in.ThreadTS, "Ack failed: "+err.Error())
			return
		}
		rec.AckedAt = a.now().UTC().Format(time.RFC3339)
		rec.AckDecided = true
		rec.AckInFlight = false
		rec.AckNote = "acked on your instruction"
		clearPendingAckReply(rec)
		if !save(rec) {
			return
		}
		reply(in.ThreadTS, fmt.Sprintf("Acked %s #%s on your instruction.", a.label(), ev.Ref))
	case "unack":
		if rec.AckedAt == "" {
			reply(in.ThreadTS, "I did not ack this one, so I will not unack it. Use the "+a.label()+" card.")
			return
		}
		if a.DryRun {
			reply(in.ThreadTS, "Dry run: would unack.")
			return
		}
		if err := a.Pager.Unacknowledge(ctx, rec.ID); err != nil {
			reply(in.ThreadTS, "Unack failed: "+err.Error())
			return
		}
		rec.AckInFlight = false
		rec.AckNote = "unacked on your instruction"
		clearPendingAckReply(rec)
		if !save(rec) {
			return
		}
		reply(in.ThreadTS, fmt.Sprintf("Unacked %s #%s; it escalates again.", a.label(), ev.Ref))
	case "note":
		reply(in.ThreadTS, a.note(ev, ref))
	case "attach":
		// Thread commands find a record by its one thread, so an alert that
		// already has one (usually its brief) is pointed back there.
		if rec.SlackTS != in.ThreadTS {
			reply(in.ThreadTS, fmt.Sprintf("%s #%s %s %s/%s already has a thread here (its brief); reply there, or send `ack #%s` / `file #%s` as a new message.",
				a.label(), ev.Ref, ev.Alertname, orDash(ev.Cluster), orDash(ev.Node), ev.Ref, ev.Ref))
			return
		}
		reply(in.ThreadTS, fmt.Sprintf("Attached %s #%s %s %s/%s. Reply here with `file`, `sent`, `ignore`, `ack`, `unack` or `note`.",
			a.label(), ev.Ref, ev.Alertname, orDash(ev.Cluster), orDash(ev.Node)))
	case "status":
		reply(in.ThreadTS, a.status(ctx))
	default:
		reply(in.ThreadTS, help)
	}
}

// note records the owner's finding for the alert's node, or lists the node's
// notes when text is empty.
func (a *Agent) note(ev event.Event, text string) string {
	if ev.Cluster == "" {
		return "This alert names no cluster, so there is nothing to keep a note on."
	}
	where := ev.Cluster + "/" + orDash(ev.Node)
	if text == "" {
		ns, err := a.Notes.List(ev.Cluster, ev.Node)
		if err != nil {
			return "Could not read the notes: " + err.Error()
		}
		if len(ns) == 0 {
			return fmt.Sprintf("No notes for %s. Reply `note <what you found>` to add one.", where)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Notes for %s, oldest first:", where)
		for _, n := range ns {
			fmt.Fprintf(&b, "\n• %s, %s: %s", n.TS.In(time.Local).Format("Jan 2 15:04"), n.Ref, n.Text)
		}
		return b.String()
	}
	n := notes.Note{TS: a.now(), Ref: fmt.Sprintf("%s #%s", a.label(), ev.Ref), Alertname: ev.Alertname, Text: text}
	if err := a.Notes.Append(ev.Cluster, ev.Node, n); err != nil {
		return "Could not save the note: " + err.Error()
	}
	return fmt.Sprintf("Noted for %s. Later briefs on it include this.", where)
}

// An instance label names a node only as a bare name: host:port, an IP or a
// URL is a scrape target or a probe.
var (
	bareHost = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	ipv4ish  = regexp.MustCompile(`^[0-9.]+$`)
)

// eventFromAlert builds the event the DC runner and replies need from an alert
// the owner pointed at by hand. There is no brief, so the verdict fields say so.
func eventFromAlert(name string, a pager.Alert) event.Event {
	l := a.Labels
	if name == "" {
		name = "irm"
	}
	// Some rules name the node Hostname, node-exporter rules only instance;
	// poll.sh reads them the same way.
	node := l["node"]
	if node == "" {
		node = l["Hostname"]
	}
	if node == "" && bareHost.MatchString(l["instance"]) && !ipv4ish.MatchString(l["instance"]) {
		node = l["instance"]
	}
	return event.Event{Pager: name, ID: a.ID, Ref: a.Ref, URL: a.URL, Alertname: l["alertname"],
		Severity: l["severity"], Cluster: l["cluster"], Node: node, StartsAt: a.StartsAt,
		Fault: "manual", Verdict: "manual", FailureMode: "manual", Brief: "(attached by hand, no brief)",
		TS: time.Now().UTC().Format(time.RFC3339)}
}

func (a *Agent) status(ctx context.Context) string {
	now := a.now()
	var b strings.Builder
	if s, ok := a.Shifts.Mine(now); ok {
		fmt.Fprintf(&b, "Your shift until %s; acks %s.\n", s.End.In(time.Local).Format("Mon 15:04 MST"), onOff(!a.DryRun))
	} else if s, ok := a.Shifts.Current(now); ok {
		fmt.Fprintf(&b, "On call: %s until %s; not acking.\n", s.UserEmail, s.End.In(time.Local).Format("Mon 15:04 MST"))
	} else {
		b.WriteString("On-call schedule unavailable; not acking.\n")
	}
	evs, _ := outbox.Scan(a.Outbox)
	recs, _ := a.Store.All()
	acked, dc, held := 0, 0, 0
	for _, r := range recs {
		if r.AckedAt != "" {
			acked++
		}
		if r.DCStatus != "" && !dcPending(r.DCStatus) {
			dc++
		}
		if r.Deferred && !r.Digested {
			held++
		}
	}
	fmt.Fprintf(&b, "Outbox: %d events, %d records, %d acked by me, %d DC decisions, %d held for the next digest. Dry run: %v.", len(evs), len(recs), acked, dc, held, a.DryRun)
	return b.String()
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off (dry run)"
}
