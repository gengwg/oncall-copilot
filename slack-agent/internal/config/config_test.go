package config

import (
	"strings"
	"testing"
)

func env(t *testing.T, kv map[string]string) func(string) string {
	base := map[string]string{
		"SLACK_BOT_TOKEN": "b", "SLACK_APP_TOKEN": "a", "SLACK_OWNER_ID": "U1",
		"IRM_API_URL": "https://irm", "IRM_PERSONAL_TOKEN": "t", "IRM_SCHEDULE_ID": "S",
		"ONCALL_ME": "ME", "OUTBOX_DIR": t.TempDir(), "METAREPO_DIR": "/m", "HOME": "/h",
	}
	for k, v := range kv {
		base[k] = v
	}
	return func(k string) string { return base[k] }
}

func TestConfigPagerDefaultsToIRM(t *testing.T) {
	c, err := Load(env(t, nil))
	if err != nil || c.Pager != "irm" {
		t.Fatalf("%+v %v", c, err)
	}
}

// PAGER is the terminal pager variable (less, bat); it must never select ours.
func TestConfigIgnoresTerminalPAGER(t *testing.T) {
	c, err := Load(env(t, map[string]string{"PAGER": "less"}))
	if err != nil || c.Pager != "irm" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestConfigUnknownPager(t *testing.T) {
	_, err := Load(env(t, map[string]string{"ONCALL_PAGER": "pagerduty"}))
	if err == nil || err.Error() != "ONCALL_PAGER=pagerduty: no such pager" {
		t.Fatalf("got %v", err)
	}
}

func TestConfigIRMVarsRequiredForIRM(t *testing.T) {
	_, err := Load(env(t, map[string]string{"IRM_API_URL": ""}))
	if err == nil || !strings.Contains(err.Error(), "IRM_API_URL") {
		t.Fatalf("got %v", err)
	}
}
