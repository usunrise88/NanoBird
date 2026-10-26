package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// noEnv is an environment with no NANOBIRD_* variables.
func noEnv(string) (string, bool) { return "", false }

// envOf reads variables from a map.
func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func writeWorkspaceConfig(t *testing.T, body string) string {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, ".nanobird")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestDefaultsMatchTheSpec(t *testing.T) {
	ws := t.TempDir()
	c, err := Load(ws, noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Model.Backend != "nanollm" || c.Model.Endpoint != "http://127.0.0.1:8080" || c.Model.KeyEnv != "NANOBIRD_MODEL_KEY" {
		t.Errorf("model = %+v", c.Model)
	}
	if c.Loop.MaxTurns != 60 || c.Loop.Thinking != "off" || c.Loop.ToolFormat != "rescue" || c.Loop.Temperature != 0 {
		t.Errorf("loop = %+v", c.Loop)
	}
	if c.Tools.ShellTimeoutS != 120 || c.Tools.ResultCapBytes != 16384 || c.Safety.Unattended != "deny" {
		t.Errorf("tools = %+v, safety = %+v", c.Tools, c.Safety)
	}
}

func TestWorkspaceFileOverridesDefaults(t *testing.T) {
	ws := writeWorkspaceConfig(t, "loop:\n  max_turns: 12\nmechanisms:\n  disable: [resilience]\n")
	c, err := Load(ws, noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Loop.MaxTurns != 12 || c.Loop.OutputCapTokens != 8192 {
		t.Errorf("loop = %+v", c.Loop)
	}
	if !reflect.DeepEqual(c.Mechanisms.Disable, []string{"resilience"}) {
		t.Errorf("disable = %v", c.Mechanisms.Disable)
	}
}

func TestUnknownKeysAreErrors(t *testing.T) {
	ws := writeWorkspaceConfig(t, "loop:\n  max_turn: 12\n")
	if _, err := Load(ws, noEnv); err == nil || !strings.Contains(err.Error(), "max_turn") {
		t.Fatalf("a misspelled key was accepted: %v", err)
	}
}

func TestEnvironmentOverridesFiles(t *testing.T) {
	ws := writeWorkspaceConfig(t, "model:\n  endpoint: http://file.example:1\n")
	env := envOf(map[string]string{
		"NANOBIRD_MODEL_ENDPOINT":    "http://env.example:2",
		"NANOBIRD_LOOP_MAX_TURNS":    "7",
		"NANOBIRD_LOOP_TEMPERATURE":  "0.5",
		"NANOBIRD_SAFETY_UNATTENDED": "ask",
		"NANOBIRD_TOOLS_WEB":         "true",
	})
	c, err := Load(ws, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Model.Endpoint != "http://env.example:2" || c.Loop.MaxTurns != 7 || c.Loop.Temperature != 0.5 {
		t.Errorf("env did not win: model %+v loop %+v", c.Model, c.Loop)
	}
	if c.Safety.Unattended != "ask" || !c.Tools.Web {
		t.Errorf("safety %+v tools %+v", c.Safety, c.Tools)
	}
}

func TestAblationAliasAddsToDisable(t *testing.T) {
	ws := t.TempDir()
	c, err := Load(ws, envOf(map[string]string{"NANOBIRD_ABLATION": "write_guard, resilience"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(c.Mechanisms.Disable, []string{"write_guard", "resilience"}) {
		t.Errorf("disable = %v", c.Mechanisms.Disable)
	}
}

func TestValidationNamesTheBadSetting(t *testing.T) {
	cases := map[string]string{
		"loop:\n  thinking: maybe\n":               "loop.thinking",
		"model:\n  backend: ollama\n":              "model.backend",
		"mechanisms:\n  disable: [nope]\n":         "mechanisms.disable",
		"mechanisms:\n  disable: [think_budget]\n": "not implemented yet",
	}
	for body, want := range cases {
		ws := writeWorkspaceConfig(t, body)
		_, err := Load(ws, noEnv)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error = %v, want mention of %q", body, err, want)
		}
	}
}

func TestBadEnvironmentValueIsAnError(t *testing.T) {
	if _, err := Load(t.TempDir(), envOf(map[string]string{"NANOBIRD_LOOP_MAX_TURNS": "many"})); err == nil {
		t.Fatal("a non-integer was accepted")
	}
}
