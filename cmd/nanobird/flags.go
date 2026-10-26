package main

import (
	"flag"
	"os"
	"strings"

	"github.com/usunrise88/nanobird/internal/config"
	"github.com/usunrise88/nanobird/internal/model/openai"
)

// engineFlags are the flags every command that talks to the engine accepts.
// Each one overrides the configuration for that command only.
type engineFlags struct {
	cwd      *string
	endpoint *string
	model    *string
	keyEnv   *string
	maxTurns *int
	ablation *string
}

func addEngineFlags(fs *flag.FlagSet) *engineFlags {
	return &engineFlags{
		cwd:      fs.String("cwd", ".", "workspace directory; its .nanobird/config.yaml applies"),
		endpoint: fs.String("endpoint", "", "override model.endpoint"),
		model:    fs.String("model", "", "override model.name"),
		keyEnv:   fs.String("key-env", "", "override model.key_env, the variable that holds the API key"),
		maxTurns: fs.Int("max-turns", 0, "override loop.max_turns"),
		ablation: fs.String("ablation", "", "add mechanism ids to mechanisms.disable, comma-separated"),
	}
}

// load builds the configuration: files and environment first, then the flags.
func (f *engineFlags) load() (config.Config, error) {
	cfg, err := config.Load(*f.cwd, os.LookupEnv)
	if err != nil {
		return config.Config{}, err
	}
	if *f.endpoint != "" {
		cfg.Model.Endpoint = *f.endpoint
	}
	if *f.model != "" {
		cfg.Model.Name = *f.model
	}
	if *f.keyEnv != "" {
		cfg.Model.KeyEnv = *f.keyEnv
	}
	if *f.maxTurns > 0 {
		cfg.Loop.MaxTurns = *f.maxTurns
	}
	cfg.Mechanisms.Disable = append(cfg.Mechanisms.Disable, splitComma(*f.ablation)...)
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// client opens the endpoint. Both model.backend values speak the OpenAI
// dialect for now; the native NanoLLM backend waits for the Harness API (SPEC §12).
func (f *engineFlags) client(cfg config.Config) (*openai.Client, error) {
	return openai.New(openai.Config{
		BaseURL: cfg.Model.Endpoint,
		Model:   cfg.Model.Name,
		APIKey:  os.Getenv(cfg.Model.KeyEnv),
	})
}

// splitComma reads a comma-separated list, dropping empty items.
func splitComma(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
