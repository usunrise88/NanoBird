package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/usunrise88/nanobird/internal/mech"
)

// Config is the effective configuration of a run (SPEC §9.1). Every key has a
// NANOBIRD_* override: the key path in upper case with dots as underscores,
// for example NANOBIRD_LOOP_MAX_TURNS.
type Config struct {
	Model      Model      `yaml:"model"`
	Loop       Loop       `yaml:"loop"`
	Mechanisms Mechanisms `yaml:"mechanisms"`
	Tools      Tools      `yaml:"tools"`
	Safety     Safety     `yaml:"safety"`
	MCP        MCP        `yaml:"mcp"`
	Skills     Skills     `yaml:"skills"`
	Offline    bool       `yaml:"offline"`
}

// Model names the engine and how to reach it (SPEC §7.5).
type Model struct {
	// Backend is nanollm or openai (SPEC §7.2, §7.3).
	Backend  string `yaml:"backend"`
	Endpoint string `yaml:"endpoint"`
	Name     string `yaml:"name"`
	// KeyEnv names the environment variable that holds the API key. The key
	// itself never goes in a file.
	KeyEnv string `yaml:"key_env"`
}

// Loop holds the turn settings (SPEC §4).
type Loop struct {
	MaxTurns        int     `yaml:"max_turns"`
	OutputCapTokens int     `yaml:"output_cap_tokens"`
	ContextTokens   int     `yaml:"context_tokens"`
	CompactionAt    float64 `yaml:"compaction_at"`
	Temperature     float64 `yaml:"temperature"`
	Thinking        string  `yaml:"thinking"`
	ThinkingBudget  int     `yaml:"thinking_budget"`
	ToolFormat      string  `yaml:"tool_format"`
}

// Mechanisms lists the ablation ids to switch off (SPEC §5).
type Mechanisms struct {
	Disable []string `yaml:"disable"`
}

// Tools holds the limits of the built-in tools (SPEC §6).
type Tools struct {
	ShellTimeoutS  int    `yaml:"shell_timeout_s"`
	ResultCapBytes int    `yaml:"result_cap_bytes"`
	Web            bool   `yaml:"web"`
	ClientFS       string `yaml:"client_fs"`
	ClientTerminal string `yaml:"client_terminal"`
}

// Safety holds the permission default for runs with no one to ask (SPEC §5.11).
type Safety struct {
	Unattended string `yaml:"unattended"`
}

// MCP holds the MCP client settings (SPEC §6.4, §6.5). They are read now and
// used once the MCP client lands.
type MCP struct {
	Config       string `yaml:"config"`
	FlatLimit    int    `yaml:"flat_limit"`
	ActiveCap    int    `yaml:"active_cap"`
	Push         bool   `yaml:"push"`
	PushSize     int    `yaml:"push_size"`
	Rerank       string `yaml:"rerank"`
	CallForm     string `yaml:"call_form"`
	CallTimeoutS int    `yaml:"call_timeout_s"`
}

// Skills lists extra skill directories (SPEC §5.9).
type Skills struct {
	Dirs []string `yaml:"dirs"`
}

// Defaults returns the built-in configuration (SPEC §9.1).
func Defaults() Config {
	return Config{
		Model: Model{
			Backend:  "nanollm",
			Endpoint: "http://127.0.0.1:8080",
			Name:     "gemma-4-e4b",
			KeyEnv:   "NANOBIRD_MODEL_KEY",
		},
		Loop: Loop{
			MaxTurns:        60,
			OutputCapTokens: 8192,
			CompactionAt:    0.9,
			Thinking:        "off",
			ThinkingBudget:  1024,
			ToolFormat:      "rescue",
		},
		Tools: Tools{
			ShellTimeoutS:  120,
			ResultCapBytes: 16384,
			ClientFS:       "off",
			ClientTerminal: "off",
		},
		Safety: Safety{Unattended: "deny"},
		MCP: MCP{
			Config:       "mcp.json",
			FlatLimit:    12,
			ActiveCap:    12,
			Push:         true,
			PushSize:     6,
			Rerank:       "none",
			CallForm:     "native",
			CallTimeoutS: 60,
		},
	}
}

// Load builds the effective configuration for a workspace. Later sources win:
// defaults, the user file, the workspace file, then NANOBIRD_* variables. env
// reports a variable and whether it is set; files that do not exist are skipped.
func Load(workspace string, env func(string) (string, bool)) (Config, error) {
	c := Defaults()
	if user, ok := userFile(); ok {
		if err := mergeFile(&c, user); err != nil {
			return Config{}, err
		}
	}
	if err := mergeFile(&c, filepath.Join(workspace, ".nanobird", "config.yaml")); err != nil {
		return Config{}, err
	}
	if err := applyEnv(reflect.ValueOf(&c).Elem(), nil, env); err != nil {
		return Config{}, err
	}
	// NANOBIRD_ABLATION is the documented switch of SPEC §2, rule 3. It adds to
	// mechanisms.disable.
	if v, ok := env("NANOBIRD_ABLATION"); ok {
		c.Mechanisms.Disable = append(c.Mechanisms.Disable, splitList(v)...)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate reports the first setting that is out of range or unknown.
func (c Config) Validate() error {
	checks := []struct {
		ok   bool
		what string
	}{
		{oneOf(c.Model.Backend, "nanollm", "openai"), "model.backend must be nanollm or openai"},
		{c.Model.Endpoint != "", "model.endpoint must not be empty"},
		{c.Model.KeyEnv != "", "model.key_env must not be empty"},
		{c.Loop.MaxTurns >= 1, "loop.max_turns must be at least 1"},
		{c.Loop.OutputCapTokens >= 1, "loop.output_cap_tokens must be at least 1"},
		{c.Loop.CompactionAt > 0 && c.Loop.CompactionAt <= 1, "loop.compaction_at must be in (0, 1]"},
		{c.Loop.Temperature >= 0, "loop.temperature must not be negative"},
		{oneOf(c.Loop.Thinking, "off", "plan", "on"), "loop.thinking must be off, plan or on"},
		{oneOf(c.Loop.ToolFormat, "rescue", "strict"), "loop.tool_format must be rescue or strict"},
		{c.Tools.ShellTimeoutS >= 1, "tools.shell_timeout_s must be at least 1"},
		{c.Tools.ResultCapBytes >= 1, "tools.result_cap_bytes must be at least 1"},
		{oneOf(c.Tools.ClientFS, "off", "read"), "tools.client_fs must be off or read"},
		{oneOf(c.Tools.ClientTerminal, "off"), "tools.client_terminal must be off"},
		{oneOf(c.Safety.Unattended, "deny", "ask"), "safety.unattended must be deny or ask"},
		{oneOf(c.MCP.Rerank, "none", "decision", "readout"), "mcp.rerank must be none, decision or readout"},
		{oneOf(c.MCP.CallForm, "native", "proxy"), "mcp.call_form must be native or proxy"},
	}
	for _, ch := range checks {
		if !ch.ok {
			return errors.New("config: " + ch.what)
		}
	}
	if _, err := mech.Parse(strings.Join(c.Mechanisms.Disable, ",")); err != nil {
		return fmt.Errorf("config: mechanisms.disable: %w", err)
	}
	return nil
}

// userFile is the user's configuration file, if the platform has one.
func userFile() (string, bool) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", false
	}
	if isWindows() {
		return filepath.Join(dir, "NanoBird", "nanobird.yaml"), true
	}
	return filepath.Join(dir, "nanobird", "nanobird.yaml"), true
}

// mergeFile decodes a YAML file over c. A missing file is skipped. Keys the
// struct does not know are errors.
func mergeFile(c *Config, path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("config: %s: %w", path, err)
	}
	return nil
}

// applyEnv sets every field from its NANOBIRD_* variable, if one is set.
func applyEnv(v reflect.Value, path []string, env func(string) (string, bool)) error {
	t := v.Type()
	for i := range t.NumField() {
		tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
		if tag == "" {
			continue
		}
		field := v.Field(i)
		keys := append(append([]string{}, path...), tag)
		if field.Kind() == reflect.Struct {
			if err := applyEnv(field, keys, env); err != nil {
				return err
			}
			continue
		}
		name := "NANOBIRD_" + strings.ToUpper(strings.Join(keys, "_"))
		raw, ok := env(name)
		if !ok {
			continue
		}
		if err := setField(field, raw); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func setField(field reflect.Value, raw string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
	case reflect.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("%q is not an integer", raw)
		}
		field.SetInt(int64(n))
	case reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("%q is not a number", raw)
		}
		field.SetFloat(f)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%q is not a boolean", raw)
		}
		field.SetBool(b)
	case reflect.Slice:
		field.Set(reflect.ValueOf(splitList(raw)))
	default:
		return fmt.Errorf("unsupported field type %s", field.Type())
	}
	return nil
}

// splitList reads a comma-separated list, dropping empty items.
func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func oneOf(v string, options ...string) bool {
	for _, o := range options {
		if v == o {
			return true
		}
	}
	return false
}
