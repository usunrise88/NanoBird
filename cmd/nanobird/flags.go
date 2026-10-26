package main

import (
	"context"

	"flag"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/model/nanollm"
	"os"
	"strings"

	"github.com/usunrise88/nanobird/internal/config"
	"github.com/usunrise88/nanobird/internal/model/openai"
)

// engineFlags are the flags every command that talks to the engine accepts.
// Each one overrides the configuration for that command only.
type engineFlags struct {
	cwd      *string // nil for a command whose workspace comes from elsewhere
	endpoint *string
	backend  *string
	model    *string
	keyEnv   *string
	maxTurns *int
	ablation *string
}

func addEngineFlags(fs *flag.FlagSet) *engineFlags {
	f := addModelFlags(fs)
	f.cwd = fs.String("cwd", ".", "workspace directory; its .nanobird/config.yaml applies")
	return f
}

// addModelFlags adds the flags that override the model and the loop. The workspace
// is not one of them: an ACP session takes its own from the client.
func addModelFlags(fs *flag.FlagSet) *engineFlags {
	return &engineFlags{
		endpoint: fs.String("endpoint", "", "override model.endpoint"),
		backend:  fs.String("backend", "", "override model.backend: nanollm (the native API) or openai (the OpenAI dialect)"),
		model:    fs.String("model", "", "override model.name"),
		keyEnv:   fs.String("key-env", "", "override model.key_env, the variable that holds the API key"),
		maxTurns: fs.Int("max-turns", 0, "override loop.max_turns"),
		ablation: fs.String("ablation", "", "add mechanism ids to mechanisms.disable, comma-separated"),
	}
}

// load builds the configuration of a workspace: its files and the environment
// first, then the flags.
func (f *engineFlags) load(workspace string) (config.Config, error) {
	cfg, err := config.Load(workspace, os.LookupEnv)
	if err != nil {
		return config.Config{}, err
	}
	if *f.endpoint != "" {
		cfg.Model.Endpoint = *f.endpoint
	}
	if *f.backend != "" {
		cfg.Model.Backend = *f.backend
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

// engineClient is the model endpoint of a run: the native NanoLLM client, or the
// OpenAI-dialect client for engines that have no native API (SPEC §7.3).
type engineClient interface {
	model.Client
	ListModels(ctx context.Context) ([]string, error)
}

// client opens the endpoint that model.backend names.
func (f *engineFlags) client(cfg config.Config) (engineClient, error) {
	if cfg.Model.Backend == "openai" {
		c, err := openai.New(openai.Config{
			BaseURL: cfg.Model.Endpoint,
			Model:   cfg.Model.Name,
			APIKey:  os.Getenv(cfg.Model.KeyEnv),
		})
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	c, err := nanollm.New(nanollm.Config{
		BaseURL:    cfg.Model.Endpoint,
		Model:      cfg.Model.Name,
		APIKey:     os.Getenv(cfg.Model.KeyEnv),
		ToolFormat: cfg.Loop.ToolFormat,
	})
	if err != nil {
		return nil, err
	}
	return c, nil
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
