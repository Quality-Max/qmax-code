package repl

import (
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// TestApplySettingStrictNumericParsing pins the Sscanf-to-Atoi fix: partial
// numbers used to be silently truncated ("1e3" → 1, "0x1f" → 0, "12abc" → 12).
func TestApplySettingStrictNumericParsing(t *testing.T) {
	cases := []struct{ key, value string }{
		{"project", "1e3"},
		{"project", "0x1f"},
		{"project", "12abc"},
		{"budget", "0x1f"},
		{"budget", "10k"},
	}
	for _, tc := range cases {
		ag := &agent.Agent{AppConfig: api.DefaultConfig()}
		before := *ag.AppConfig
		if got := applySettingValue(tc.key, tc.value, ag, &tui.Terminal{}); got != settingInvalid {
			t.Errorf("applySettingValue(%q, %q) = %v, want settingInvalid", tc.key, tc.value, got)
		}
		if ag.AppConfig.DefaultProject != before.DefaultProject || ag.AppConfig.MaxTokenBudget != before.MaxTokenBudget {
			t.Errorf("applySettingValue(%q, %q) mutated config despite rejection", tc.key, tc.value)
		}
	}
}

func TestApplySettingProjectValues(t *testing.T) {
	ag := &agent.Agent{}
	if got := applySettingValue("project", "149", ag, &tui.Terminal{}); got != settingApplied {
		t.Fatalf("project 149 = %v, want settingApplied", got)
	}
	if ag.AppConfig.DefaultProject != 149 {
		t.Fatalf("DefaultProject = %d, want 149", ag.AppConfig.DefaultProject)
	}

	if got := applySettingValue("project", "0", ag, &tui.Terminal{}); got != settingApplied {
		t.Fatalf("project 0 = %v, want settingApplied", got)
	}
	if ag.AppConfig.DefaultProject != 0 {
		t.Fatalf("project 0 should clear DefaultProject, got %d", ag.AppConfig.DefaultProject)
	}

	if got := applySettingValue("project", "-3", ag, &tui.Terminal{}); got != settingInvalid {
		t.Fatalf("project -3 = %v, want settingInvalid", got)
	}
}

func TestApplySettingUnknownKey(t *testing.T) {
	ag := &agent.Agent{}
	if got := applySettingValue("definitely_not_a_key", "1", ag, &tui.Terminal{}); got != settingInvalid {
		t.Fatalf("unknown key = %v, want settingInvalid", got)
	}
}

// TestRedactSecretInput pins the history hygiene fix: /set values for the
// secret keys must never be recallable via up-arrow — including the
// whitespace variations strings.Fields accepts as valid commands.
func TestRedactSecretInput(t *testing.T) {
	cases := map[string]string{
		"/set apikey qm-live-secret123":      "/set apikey <redacted>",
		"/set anthropic-key sk-ant-abc123":   "/set anthropic-key <redacted>",
		"/SET ANTHROPIC_KEY sk-ant-abc123":   "/set ANTHROPIC_KEY <redacted>",
		"/set  apikey   qm-live-secret123":   "/set apikey <redacted>",
		"/set\tanthropic-key\tsk-ant-x":      "/set anthropic-key <redacted>",
		"/set\u00A0apikey\u00A0qm-live-x":    "/set apikey <redacted>", // NBSP is a unicode space strings.Fields splits on
		"/set apikey":                        "/set apikey",
		"/set theme dark":                    "/set theme dark",
		"what is the anthropic-key for this": "what is the anthropic-key for this",
	}
	for in, want := range cases {
		if got := redactSecretInput(in); got != want {
			t.Errorf("redactSecretInput(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBuildSettingsRowsSnapshot checks the picker rows reflect the config and
// that boolean defaults render as "false" rather than empty strings.
func TestBuildSettingsRowsSnapshot(t *testing.T) {
	on := true
	cfg := &api.Config{DefaultProject: 149, MaxTokenBudget: 5000, CloudSync: &on, Theme: "ocean"}
	rows := buildSettingsRows(cfg)

	byKey := map[string]tui.SettingsRow{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	for _, key := range []string{"project", "budget", "cloud_sync", "theme", "cerebras_model", "cerebras_reasoning_effort"} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("settings rows missing %q", key)
		}
	}
	if byKey["project"].Value != "149" || byKey["budget"].Value != "5000" {
		t.Errorf("project/budget snapshot wrong: %q / %q", byKey["project"].Value, byKey["budget"].Value)
	}
	if byKey["cloud_sync"].Value != "true" {
		t.Errorf("cloud_sync with pointer true should snapshot as true, got %q", byKey["cloud_sync"].Value)
	}
	if byKey["theme"].Value != "ocean" {
		t.Errorf("theme snapshot = %q, want ocean", byKey["theme"].Value)
	}
	if !strings.Contains(byKey["project"].Hint, "unset") {
		t.Errorf("project row should hint that 0 unsets it")
	}
}
