// Package config reads the agent's environment. Everything is required unless
// it has a default, and a missing value fails at start, not at the first alert.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	SlackBotToken string
	SlackAppToken string
	SlackOwnerID  string

	IRMAPIURL        string
	GrafanaURL       string
	IRMPersonalToken string
	IRMScheduleID    string
	OnCallMe         string

	OutboxDir     string
	AgentStateDir string
	ClaudeBin     string
	ClaudeModel   string
	MetarepoDir   string

	AckDryRun    bool
	CollapseMax  int
	ScanInterval time.Duration
	HeartbeatURL string // optional IRM heartbeat; pinged after each successful scan
	DigestAt     string // HH:MM in DigestTZ; "" DMs every brief regardless of shift
	DigestTZ     string
}

func Load(getenv func(string) string) (Config, error) {
	var missing []string
	req := func(k string) string {
		v := strings.TrimSpace(getenv(k))
		if v == "" {
			missing = append(missing, k)
		}
		return v
	}
	home := getenv("HOME")
	opt := func(k, def string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return def
	}
	c := Config{
		SlackBotToken:    req("SLACK_BOT_TOKEN"),
		SlackAppToken:    req("SLACK_APP_TOKEN"),
		SlackOwnerID:     req("SLACK_OWNER_ID"),
		IRMAPIURL:        req("IRM_API_URL"),
		GrafanaURL:       opt("GRAFANA_URL", ""),
		IRMPersonalToken: req("IRM_PERSONAL_TOKEN"),
		IRMScheduleID:    req("IRM_SCHEDULE_ID"),
		OnCallMe:         req("ONCALL_ME"),
		OutboxDir:        req("OUTBOX_DIR"),
		AgentStateDir:    opt("AGENT_STATE_DIR", filepath.Join(home, ".local", "state", "oncall-slack-agent")),
		ClaudeBin:        opt("CLAUDE_BIN", "claude"),
		ClaudeModel:      opt("CLAUDE_MODEL", ""),
		MetarepoDir:      req("METAREPO_DIR"),
		AckDryRun:        opt("ACK_DRY_RUN", "1") != "0",
		HeartbeatURL:     opt("HEARTBEAT_URL", ""),
		DigestAt:         opt("DIGEST_AT", "08:05"),
		DigestTZ:         opt("DIGEST_TZ", "America/Los_Angeles"),
		CollapseMax:      6,
		ScanInterval:     10 * time.Second,
	}
	if v := getenv("COLLAPSE_MAX"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("COLLAPSE_MAX: %w", err)
		}
		c.CollapseMax = n
	}
	if v := getenv("SCAN_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("SCAN_INTERVAL: %w", err)
		}
		c.ScanInterval = d
	}
	if c.DigestAt != "" && c.DigestAt != "off" {
		if _, err := time.Parse("15:04", c.DigestAt); err != nil {
			return c, fmt.Errorf("DIGEST_AT: %w", err)
		}
		if _, err := time.LoadLocation(c.DigestTZ); err != nil {
			return c, fmt.Errorf("DIGEST_TZ: %w", err)
		}
	} else {
		c.DigestAt = ""
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("missing env: %s", strings.Join(missing, ", "))
	}
	if st, err := os.Stat(c.OutboxDir); err != nil || !st.IsDir() {
		return c, fmt.Errorf("OUTBOX_DIR %s: not a readable directory", c.OutboxDir)
	}
	return c, nil
}
