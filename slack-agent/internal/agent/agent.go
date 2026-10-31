// Package agent sequences what happens to one brief: post it to the owner,
// decide whether to ack, and hand hardware faults to the DC request runner.
// Every step is saved to the record before the next side effect.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/dcrequest"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/irm"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/ledger"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/oncall"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/outbox"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/slackio"
)

type IRM interface {
	Group(ctx context.Context, id string) (irm.Group, error)
	FindGroup(ctx context.Context, ref string) (irm.Group, error)
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
)

type Agent struct {
	Outbox      string
	Store       *event.Store
	Poster      slackio.Poster
	IRM         IRM
	Shifts      Shifts
	Ledger      Ledger
	Runner      dcrequest.Runner
	DryRun      bool
	CollapseMax int
	Now         func() time.Time
	Log         *log.Logger

	dcJobs chan dcJob
}

type dcJob struct {
	id    string
	force bool
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
	events, scanErr := outbox.Scan(a.Outbox)
	var fresh []event.Event
	var pending []event.Event
	for _, ev := range events {
		rec, err := a.Store.Get(ev.IRMID)
		if err != nil {
			return err
		}
		switch {
		case rec == nil:
			fresh = append(fresh, ev)
		case !rec.PostFailed && (rec.SlackTS == "" || !rec.AckDecided || a.wantsDC(rec)):
			pending = append(pending, ev)
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
			a.logf("event %s: %v", ev.IRMID, err)
		}
	}
	return scanErr
}

func (a *Agent) wantsDC(rec *event.Record) bool {
	return rec.Event.DCDraft != nil && rec.DCStatus == ""
}

// collapse posts one summary for a burst and marks each as reported but not
// acked: acked means individually reported, and a summary is not that.
func (a *Agent) collapse(ctx context.Context, evs []event.Event) error {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d briefs in the last %s; summarised, none acked.*\n", len(evs), collapseWindow)
	for _, ev := range evs {
		fmt.Fprintf(&b, "• IRM #%d %s (%s) %s/%s verdict %s fault %s\n", ev.IRMNumber, ev.Alertname, ev.Severity, ev.Cluster, ev.Node, ev.Verdict, ev.Fault)
	}
	ts, err := a.Poster.PostDM(ctx, b.String())
	if err != nil {
		return fmt.Errorf("collapse post: %w", err)
	}
	now := a.now().UTC().Format(time.RFC3339)
	for _, ev := range evs {
		rec := &event.Record{IRMID: ev.IRMID, Event: ev, SlackTS: ts, PostedAt: now, PostAttempts: 1,
			Collapsed: true, AckDecided: true, AckNote: "not acked: part of a burst summary"}
		if err := a.Store.Save(rec); err != nil {
			return err
		}
	}
	return nil
}

// HandleEvent moves one event forward as far as it can this tick.
func (a *Agent) HandleEvent(ctx context.Context, ev event.Event) error {
	rec, err := a.Store.Get(ev.IRMID)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = &event.Record{IRMID: ev.IRMID, Event: ev}
	}
	if rec.PostFailed {
		return nil
	}
	if rec.SlackTS == "" {
		if rec.PostAttempts >= maxPostAttempts {
			rec.PostFailed = true
			a.logf("event %s: giving up posting after %d attempts; not acked", ev.IRMID, rec.PostAttempts)
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
			a.logf("event %s: %s; retrying next tick", ev.IRMID, note)
			return nil
		case ackYes:
			if a.DryRun {
				note = "would ack (dry run): your shift, brief delivered"
			} else if err := a.IRM.Acknowledge(ctx, ev.IRMID); err != nil && !errors.Is(err, irm.ErrAlreadyAcked) {
				a.logf("event %s: ack failed: %v; retrying next tick", ev.IRMID, err)
				return nil
			} else {
				rec.AckedAt = a.now().UTC().Format(time.RFC3339)
				note = fmt.Sprintf("acked IRM #%d: your shift, brief delivered", ev.IRMNumber)
				if errors.Is(err, irm.ErrAlreadyAcked) {
					note = fmt.Sprintf("IRM #%d was already acknowledged", ev.IRMNumber)
				}
			}
		}
		rec.AckDecided = true
		rec.AckNote = note
		if err := a.Store.Save(rec); err != nil {
			return err
		}
		if err := a.Poster.Reply(ctx, rec.SlackTS, note); err != nil {
			a.logf("event %s: reply failed: %v", ev.IRMID, err)
		}
	}
	if a.wantsDC(rec) {
		rec.DCStatus = "queued"
		if err := a.Store.Save(rec); err != nil {
			return err
		}
		a.enqueue(dcJob{id: ev.IRMID})
	}
	return nil
}

type ackDecision int

const (
	ackNo ackDecision = iota
	ackYes
	ackRetry
)

// ackGate is the whole policy. Every "no" is explained in the thread.
func (a *Agent) ackGate(ctx context.Context, ev event.Event, rec *event.Record) (ackDecision, string) {
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
		return ackNo, "not acked: verdict " + ev.Verdict + ", IRM keeps paging you"
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
	g, err := a.IRM.Group(ctx, ev.IRMID)
	if err != nil {
		return ackRetry, "IRM lookup failed: " + err.Error()
	}
	if g.State != "firing" {
		return ackNo, "not acked: already " + g.State
	}
	return ackYes, ""
}

// Render is the DM for one brief: the facts first, the brief under them.
func Render(ev event.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*IRM #%d %s* (%s) %s / %s\n", ev.IRMNumber, ev.Alertname, ev.Severity, orDash(ev.Cluster), orDash(ev.Node))
	fmt.Fprintf(&b, "verdict %s · fault %s · failure mode %s", ev.Verdict, ev.Fault, ev.FailureMode)
	if ev.Degraded != nil {
		fmt.Fprintf(&b, " · degraded: %s", *ev.Degraded)
	}
	if ev.DCDraft != nil {
		b.WriteString(" · DC draft pending")
	}
	b.WriteString("\n")
	if ev.IRMURL != "" {
		b.WriteString(ev.IRMURL + "\n")
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

func (a *Agent) enqueue(j dcJob) {
	if a.dcJobs == nil {
		a.dcJobs = make(chan dcJob, 64)
	}
	select {
	case a.dcJobs <- j:
	default:
		a.logf("dc queue full; dropping job for %s", j.id)
	}
}

// RunDCWorker drains the queue until ctx ends.
func (a *Agent) RunDCWorker(ctx context.Context) {
	if a.dcJobs == nil {
		a.dcJobs = make(chan dcJob, 64)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-a.dcJobs:
			if err := a.runDC(ctx, j); err != nil {
				a.logf("dc %s: %v", j.id, err)
			}
		}
	}
}

// DrainDC runs queued jobs synchronously; for tests and for shutdown.
func (a *Agent) DrainDC(ctx context.Context) {
	for {
		select {
		case j := <-a.dcJobs:
			if err := a.runDC(ctx, j); err != nil {
				a.logf("dc %s: %v", j.id, err)
			}
		default:
			return
		}
	}
}

func (a *Agent) runDC(ctx context.Context, j dcJob) error {
	rec, err := a.Store.Get(j.id)
	if err != nil || rec == nil {
		return fmt.Errorf("record %s: %v", j.id, err)
	}
	ev := rec.Event
	now := a.now()
	if !j.force {
		if e, ok := a.Ledger.Recent(ev.Cluster, ev.Node, now.Add(-ledgerWindow)); ok {
			rec.DCStatus = "skipped_recent"
			if err := a.Store.Save(rec); err != nil {
				return err
			}
			return a.Poster.Reply(ctx, rec.SlackTS, fmt.Sprintf("DC request not drafted: %s/%s already %s on %s (IRM %s). Reply `file` to draft anyway.",
				ev.Cluster, ev.Node, e.Status, e.TS.In(time.Local).Format("Jan 2"), e.IRMID))
		}
	}
	res, err := a.Runner.FileDCRequest(ctx, ev)
	if err != nil {
		res = dcrequest.Result{Status: dcrequest.Failed, Detail: err.Error()}
	}
	var status, msg string
	switch res.Status {
	case dcrequest.Drafted:
		status = "drafted"
		msg = fmt.Sprintf("DC request drafted for %s/%s in %s. Review and send it from your Slack drafts. Reply `ignore` if it should not go.", ev.Cluster, ev.Node, orDash(res.Detail))
	case dcrequest.AlreadySent:
		status = "already_sent"
		msg = fmt.Sprintf("DC request for %s/%s already exists: %s. Nothing drafted.", ev.Cluster, ev.Node, orDash(res.Detail))
	default:
		status = "failed"
		msg = fmt.Sprintf("DC request not drafted: %s. Draft file: %s. Manual: /file-dc-request %s", res.Detail, deref(ev.DCDraft), ev.IRMURL)
	}
	// The run is a headless Claude session; its price is part of the result.
	if res.CostUSD > 0 {
		msg += fmt.Sprintf(" (run cost $%.2f)", res.CostUSD)
	}
	a.logf("dc %s: %s, cost $%.2f", ev.IRMID, status, res.CostUSD)
	if err := a.Ledger.Append(ledger.Entry{TS: now, Cluster: ev.Cluster, Node: ev.Node, IRMID: ev.IRMID, Status: status}); err != nil {
		a.logf("dc %s: ledger write failed, the next alert on %s may draft again: %v", ev.IRMID, ev.Node, err)
	}
	rec.DCStatus = status
	if err := a.Store.Save(rec); err != nil {
		return err
	}
	return a.Poster.Reply(ctx, rec.SlackTS, msg)
}

func deref(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

// --- Replies from the owner.

const help = "Reply in a brief's thread with `file` (draft the DC request now), `sent` (you sent the draft), `ignore` (never for this node for 14 days), `ack`, `unack`; or send `file #5474` / `file <alert link>` (also `sent`, `ack`, `unack`, `ignore`) as a new message for any alert; `status` anywhere."

// slackLink strips Slack's <url|label> wrapping from a pasted link.
var slackLink = regexp.MustCompile(`<([^|>]+)(?:\|[^>]*)?>`)

// HandleInbound acts on one message from the owner.
func (a *Agent) HandleInbound(ctx context.Context, in slackio.Inbound) {
	text := strings.TrimSpace(slackLink.ReplaceAllString(in.Text, "$1"))
	cmd, ref, _ := strings.Cut(text, " ")
	cmd = strings.ToLower(cmd)
	ref = strings.TrimSpace(ref)
	reply := func(ts, text string) {
		if err := a.Poster.Reply(ctx, ts, text); err != nil {
			a.logf("reply: %v", err)
		}
	}
	save := func(r *event.Record) {
		if err := a.Store.Save(r); err != nil {
			a.logf("record %s: save failed: %v", r.IRMID, err)
		}
	}
	threadTS := in.ThreadTS
	var rec *event.Record
	switch {
	case threadTS == "" && ref != "" && (cmd == "file" || cmd == "sent" || cmd == "ignore" || cmd == "ack" || cmd == "unack"):
		// A command about any alert: attach the group to this message's thread.
		g, err := a.IRM.FindGroup(ctx, ref)
		if err != nil {
			reply(in.TS, "Could not find that alert: "+err.Error())
			return
		}
		threadTS = in.TS
		rec, err = a.Store.ByThread(threadTS)
		if err == nil && rec == nil {
			if rec, err = a.Store.Get(g.ID); err == nil && rec == nil {
				rec = &event.Record{IRMID: g.ID, Event: eventFromGroup(g), SlackTS: threadTS,
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
		rec.DCStatus = "queued"
		save(rec)
		a.enqueue(dcJob{id: rec.IRMID, force: true})
		reply(in.ThreadTS, fmt.Sprintf("Drafting the DC request for %s/%s now; this takes a few minutes.", ev.Cluster, ev.Node))
	case "sent":
		if err := a.Ledger.Append(ledger.Entry{TS: a.now(), Cluster: ev.Cluster, Node: ev.Node, IRMID: ev.IRMID, Status: "sent"}); err != nil {
			reply(in.ThreadTS, "Could not record it as sent: "+err.Error())
			return
		}
		rec.DCStatus = "sent"
		save(rec)
		reply(in.ThreadTS, fmt.Sprintf("Noted: DC request for %s/%s sent. No new draft for this node for 14 days unless you reply `file`.", ev.Cluster, ev.Node))
	case "ignore":
		if err := a.Ledger.Append(ledger.Entry{TS: a.now(), Cluster: ev.Cluster, Node: ev.Node, IRMID: ev.IRMID, Status: "ignored"}); err != nil {
			reply(in.ThreadTS, "Could not record the ignore: "+err.Error())
			return
		}
		rec.DCStatus = "ignored"
		save(rec)
		reply(in.ThreadTS, fmt.Sprintf("Noted: no DC request for %s/%s for 14 days unless you reply `file`.", ev.Cluster, ev.Node))
	case "ack":
		if a.DryRun {
			reply(in.ThreadTS, "Dry run: would ack on your instruction.")
			return
		}
		err := a.IRM.Acknowledge(ctx, rec.IRMID)
		if err != nil && !errors.Is(err, irm.ErrAlreadyAcked) {
			reply(in.ThreadTS, "Ack failed: "+err.Error())
			return
		}
		rec.AckedAt = a.now().UTC().Format(time.RFC3339)
		rec.AckDecided = true
		rec.AckNote = "acked on your instruction"
		save(rec)
		reply(in.ThreadTS, fmt.Sprintf("Acked IRM #%d on your instruction.", ev.IRMNumber))
	case "unack":
		if rec.AckedAt == "" {
			reply(in.ThreadTS, "I did not ack this one, so I will not unack it. Use the IRM card.")
			return
		}
		if a.DryRun {
			reply(in.ThreadTS, "Dry run: would unack.")
			return
		}
		if err := a.IRM.Unacknowledge(ctx, rec.IRMID); err != nil {
			reply(in.ThreadTS, "Unack failed: "+err.Error())
			return
		}
		rec.AckNote = "unacked on your instruction"
		save(rec)
		reply(in.ThreadTS, fmt.Sprintf("Unacked IRM #%d; it escalates again.", ev.IRMNumber))
	case "status":
		reply(in.ThreadTS, a.status(ctx))
	default:
		reply(in.ThreadTS, help)
	}
}

// eventFromGroup builds the event the DC runner and replies need from an IRM
// alert group the owner pointed at by hand. There is no brief, so the verdict
// fields say so.
func eventFromGroup(g irm.Group) event.Event {
	l := g.Labels()
	url := g.Permalinks.Slack
	if url == "" {
		url = g.Permalinks.Web
	}
	return event.Event{IRMID: g.ID, IRMNumber: g.Number, IRMURL: url, Alertname: l["alertname"],
		Severity: l["severity"], Cluster: l["cluster"], Node: l["node"], StartsAt: g.StartsAt(),
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
	acked, dc := 0, 0
	for _, r := range recs {
		if r.AckedAt != "" {
			acked++
		}
		if r.DCStatus != "" && r.DCStatus != "queued" {
			dc++
		}
	}
	fmt.Fprintf(&b, "Outbox: %d events, %d records, %d acked by me, %d DC decisions. Dry run: %v.", len(evs), len(recs), acked, dc, a.DryRun)
	return b.String()
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off (dry run)"
}
