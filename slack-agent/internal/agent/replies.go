package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
)

func hasPendingReply(rec *event.Record) bool {
	return rec.AckReply != nil && rec.AckReply.SentAt == "" || rec.DCReply != nil && rec.DCReply.SentAt == ""
}

func clearPendingAckReply(rec *event.Record) {
	if rec.AckReply != nil && rec.AckReply.SentAt == "" {
		rec.AckReply = nil
	}
}

func clearPendingDCReply(rec *event.Record) {
	if rec.DCReply != nil && rec.DCReply.SentAt == "" {
		rec.DCReply = nil
	}
}

// flushAllReplies retries independent of the poller outbox, so manually
// attached alerts and events no longer present there still recover.
func (a *Agent) flushAllReplies(ctx context.Context) {
	recs, err := a.Store.All()
	if err != nil {
		a.logf("reply recovery: %v", err)
		return
	}
	for i := range recs {
		if !hasPendingReply(&recs[i]) {
			continue
		}
		a.recordMu.Lock()
		rec, err := a.Store.Get(recs[i].ID)
		if err == nil && rec != nil {
			err = a.flushRepliesLocked(ctx, rec)
		}
		a.recordMu.Unlock()
		if err != nil {
			a.logf("reply %s: %v", recs[i].ID, err)
		}
	}
}

// flushRepliesLocked preserves the order in which an alert's automatic replies
// were created. The caller holds recordMu.
func (a *Agent) flushRepliesLocked(ctx context.Context, rec *event.Record) error {
	if rec.AckReply != nil {
		if err := a.deliverReply(ctx, rec, rec.AckReply); err != nil {
			return err
		}
	}
	if rec.DCReply != nil && rec.DCReply.SentAt == "" && rec.DCReply.Generation != rec.DCAttempt {
		// A newer manual run supersedes a result that never reached Slack.
		rec.DCReply = nil
		if err := a.Store.Save(rec); err != nil {
			return err
		}
	}
	if rec.DCReply != nil {
		return a.deliverReply(ctx, rec, rec.DCReply)
	}
	return nil
}

// deliverReply reconciles an earlier attempt before posting again: Slack can
// accept a message even when the client never receives the success response.
// The caller holds recordMu.
func (a *Agent) deliverReply(ctx context.Context, rec *event.Record, receipt *event.ReplyReceipt) error {
	if receipt.SentAt != "" {
		return nil
	}
	if receipt.Attempts > 0 {
		found, err := a.Poster.ReplyExists(ctx, receipt.ThreadTS, receipt.Text, receipt.ID)
		if err != nil {
			receipt.LastError = err.Error()
			if serr := a.Store.Save(rec); serr != nil {
				return fmt.Errorf("reconcile reply: %v; saving the error: %w", err, serr)
			}
			return fmt.Errorf("reconcile reply: %w", err)
		}
		if found {
			receipt.SentAt = a.now().UTC().Format(time.RFC3339)
			receipt.LastError = ""
			return a.Store.Save(rec)
		}
	}
	receipt.Attempts++
	if err := a.Store.Save(rec); err != nil {
		return err
	}
	if err := a.Poster.ReplyIdentified(ctx, receipt.ThreadTS, receipt.Text, receipt.ID); err != nil {
		receipt.LastError = err.Error()
		if serr := a.Store.Save(rec); serr != nil {
			return fmt.Errorf("reply: %v; saving the failed attempt: %w", err, serr)
		}
		return fmt.Errorf("reply: %w", err)
	}
	receipt.SentAt = a.now().UTC().Format(time.RFC3339)
	receipt.LastError = ""
	return a.Store.Save(rec)
}
