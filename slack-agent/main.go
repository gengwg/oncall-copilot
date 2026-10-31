// oncall-slack-agent: the Slack side of the on-call copilot. It reads the
// poller's briefs, DMs them to one person, acks pages during that person's own
// shift once the brief has reached them, and drafts DC hardware requests
// through the metarepo skill. See docs/real-oncall-research.md §3.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/agent"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/config"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/dcrequest"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/irm"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/ledger"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/oncall"
	"github.com/gengwg/oncall-copilot/slack-agent/internal/slackio"
)

const shiftRefresh = time.Hour

func main() {
	lg := log.New(os.Stderr, "", log.LstdFlags|log.LUTC)
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		lg.Fatalf("config: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := event.NewStore(filepath.Join(cfg.AgentStateDir, "events"))
	if err != nil {
		lg.Fatalf("state: %v", err)
	}
	irmc, err := irm.New(cfg.IRMAPIURL, cfg.IRMPersonalToken, cfg.GrafanaURL)
	if err != nil {
		lg.Fatalf("irm: %v", err)
	}
	// Two refresh periods without a good fetch and the table is stale: fail closed.
	shifts := oncall.NewTracker(cfg.OnCallMe, 2*shiftRefresh+5*time.Minute, time.Now)
	slk, err := slackio.New(ctx, cfg.SlackBotToken, cfg.SlackAppToken, cfg.SlackOwnerID, lg)
	if err != nil {
		lg.Fatalf("slack: %v", err)
	}
	a := &agent.Agent{
		Outbox: cfg.OutboxDir, Store: store, Poster: slk, IRM: irmc, Shifts: shifts,
		Ledger: ledger.New(filepath.Join(cfg.AgentStateDir, "dc-requests.tsv")),
		Runner: dcrequest.ExecRunner{Bin: cfg.ClaudeBin, Dir: cfg.MetarepoDir, Model: cfg.ClaudeModel, Timeout: 10 * time.Minute},
		DryRun: cfg.AckDryRun, CollapseMax: cfg.CollapseMax, Log: lg,
	}

	refresh := func() {
		now := time.Now()
		sh, err := irmc.FinalShifts(ctx, cfg.IRMScheduleID, now.Add(-24*time.Hour), now.Add(48*time.Hour))
		if err != nil {
			lg.Printf("final_shifts: %v (keeping the previous table until it goes stale)", err)
			return
		}
		shifts.Update(sh)
		if s, ok := shifts.Mine(now); ok {
			lg.Printf("shift: mine until %s", s.End.Local().Format(time.RFC822))
		} else if s, ok := shifts.Current(now); ok {
			lg.Printf("shift: %s until %s", s.UserEmail, s.End.Local().Format(time.RFC822))
		} else {
			lg.Printf("shift: nobody listed now")
		}
	}
	refresh()
	go func() {
		t := time.NewTicker(shiftRefresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				refresh()
			}
		}
	}()
	go a.RunDCWorker(ctx)
	go func() {
		if err := slk.Listen(ctx, func(in slackio.Inbound) { a.HandleInbound(ctx, in) }); err != nil && ctx.Err() == nil {
			lg.Fatalf("slack socket mode: %v", err)
		}
	}()

	lg.Printf("started: outbox %s, dry run %v, every %s, heartbeat %v", cfg.OutboxDir, cfg.AckDryRun, cfg.ScanInterval, cfg.HeartbeatURL != "")
	// Pinged only after a scan that read the outbox, so IRM alerts when the
	// agent dies, loses the outbox, or stalls. The URL carries a secret: never logged.
	heartbeat := func() {
		if cfg.HeartbeatURL == "" {
			return
		}
		hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(hctx, http.MethodGet, cfg.HeartbeatURL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lg.Printf("heartbeat ping failed: %v", err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			lg.Printf("heartbeat ping: HTTP %d", resp.StatusCode)
		}
	}
	t := time.NewTicker(cfg.ScanInterval)
	defer t.Stop()
	for {
		if err := a.Tick(ctx); err != nil {
			lg.Printf("tick: %v", err)
		} else {
			heartbeat()
		}
		select {
		case <-ctx.Done():
			lg.Printf("stopping")
			return
		case <-t.C:
		}
	}
}
