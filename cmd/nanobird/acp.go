package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/usunrise88/nanobird/internal/acp"
	"github.com/usunrise88/nanobird/internal/config"
	"github.com/usunrise88/nanobird/internal/loop"
	"github.com/usunrise88/nanobird/internal/mcp"
	"github.com/usunrise88/nanobird/internal/mech"
)

// acpCommand is `nanobird acp` (SPEC §8): an ACP agent over stdio. The client gives
// each session its workspace, and that workspace's configuration applies to the
// session. Stdout carries the protocol, so every other line goes to stderr.
func acpCommand(args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	fs := flag.NewFlagSet("acp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	logLevel := fs.String("log-level", envDefault("NANOBIRD_LOG_LEVEL", "info"), "debug, info, warn or error; prompts are logged at debug only (SPEC §9.4)")
	flags := addModelFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	level, err := logLevelOf(*logLevel)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird acp: --log-level: %v\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	open := func(workspace, id string, servers map[string]mcp.Spec, obs loop.Observer) (acp.Runner, error) {
		cfg, err := flags.load(workspace)
		if err != nil {
			return nil, err
		}
		client, err := flags.client(cfg)
		if err != nil {
			return nil, err
		}
		s, err := newRunSetup(ctx, setupOptions{
			workspace:  workspace,
			resume:     id,
			servers:    servers,
			cfg:        cfg,
			client:     client,
			level:      level,
			logs:       stderr,
			unattended: true,
			observer: clientIO{
				Observer: obs,
				fs:       cfg.ACP.ClientFS == "read",
				terminal: cfg.ACP.ClientTerminal == "on",
			},
			warn: stderr,
		})
		if err != nil {
			return nil, err
		}
		if id != "" {
			if err := s.lp.Restore(); err != nil {
				s.Close()
				return nil, err
			}
		}
		return &sessionRunner{setup: s, ctx: ctx, root: workspace, cfg: cfg, flags: flags, client: client}, nil
	}
	if err := acp.Serve(ctx, acp.NewAgent(open, version), stdin, stdout); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "nanobird acp: %v\n", err)
		return 1
	}
	return 0
}

// clientIO is the observer of a session that lets the client read files and run
// terminals, as the configuration allows (SPEC §8.6). With either off, the tools do
// that work themselves.
type clientIO struct {
	loop.Observer
	fs       bool
	terminal bool
}

// ReadText reads through the client when the configuration allows it.
func (c clientIO) ReadText(ctx context.Context, abs string) (string, bool, error) {
	if !c.fs {
		return "", false, nil
	}
	return c.Observer.ReadText(ctx, abs)
}

// RunTerminal runs in the client's terminal when the configuration allows it.
func (c clientIO) RunTerminal(ctx context.Context, command string, args []string, cwd string) (string, bool, error) {
	if !c.terminal {
		return "", false, nil
	}
	return c.Observer.RunTerminal(ctx, command, args, cwd)
}

// sessionRunner is one session of the ACP agent: its prompts run on one setup. It
// keeps the configuration and the engine client, so that the options can change them.
type sessionRunner struct {
	setup  *runSetup
	ctx    context.Context
	root   string
	cfg    config.Config
	flags  *engineFlags
	client engineClient
}

// Offline is the offline option of the session.
func (r *sessionRunner) Offline() bool { return r.cfg.Offline }

// SetOffline changes the offline option, and starts the MCP servers again under it.
func (r *sessionRunner) SetOffline(offline bool) error {
	cfg := r.cfg
	cfg.Offline = offline
	if err := r.setup.reloadMCP(r.ctx, cfg, r.root); err != nil {
		return err
	}
	r.cfg = cfg
	return nil
}

// Model is the model name of the session.
func (r *sessionRunner) Model() string { return r.cfg.Model.Name }

// SetModel changes the model of the session, with a client for that model.
func (r *sessionRunner) SetModel(name string) error {
	cfg := r.cfg
	cfg.Model.Name = name
	client, err := r.flags.client(cfg)
	if err != nil {
		return err
	}
	r.setup.lp.SetClient(client)
	r.client, r.cfg = client, cfg
	return nil
}

// Models lists the models that the engine serves.
func (r *sessionRunner) Models(ctx context.Context) ([]string, error) {
	return r.client.ListModels(ctx)
}

// ID is the session's id, which names its transcript.
func (r *sessionRunner) ID() string { return r.setup.log.ID() }

// Prompt runs one task of the session.
func (r *sessionRunner) Prompt(ctx context.Context, task string) loop.Result {
	return r.setup.lp.Run(ctx, task)
}

// SetMode sets the mode of the session's next prompts.
func (r *sessionRunner) SetMode(mode string) { r.setup.lp.SetMode(loop.Mode(mode)) }

// SetAblation sets the mechanisms that the session leaves off (SPEC §8.8). It
// replaces the list the configuration gave.
func (r *sessionRunner) SetAblation(mechanisms []string) error {
	sw, err := mech.Parse(strings.Join(mechanisms, ","))
	if err != nil {
		return err
	}
	r.setup.lp.SetSwitches(sw)
	return nil
}

// Notices are the warnings of the session's setup, such as an MCP server that did
// not start.
func (r *sessionRunner) Notices() []string { return r.setup.warnings }

// Thinking is the thinking policy of the session's next prompts.
func (r *sessionRunner) Thinking() string { return r.setup.lp.Thinking() }

// SetThinking sets the thinking policy of the session's next prompts.
func (r *sessionRunner) SetThinking(mode string) { r.setup.lp.SetThinking(mode) }

// Close releases the session.
func (r *sessionRunner) Close() { r.setup.Close() }
