package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/usunrise88/nanobird/internal/acp"
	"github.com/usunrise88/nanobird/internal/loop"
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
	open := func(workspace, id string, obs loop.Observer) (acp.Runner, error) {
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
			cfg:        cfg,
			client:     client,
			level:      level,
			logs:       stderr,
			unattended: true,
			observer:   obs,
			warn:       stderr,
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
		return &sessionRunner{setup: s}, nil
	}
	if err := acp.Serve(ctx, acp.NewAgent(open, version), stdin, stdout); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "nanobird acp: %v\n", err)
		return 1
	}
	return 0
}

// sessionRunner is one session of the ACP agent: its prompts run on one setup.
type sessionRunner struct {
	setup *runSetup
}

// ID is the session's id, which names its transcript.
func (r *sessionRunner) ID() string { return r.setup.log.ID() }

// Prompt runs one task of the session.
func (r *sessionRunner) Prompt(ctx context.Context, task string) loop.Result {
	return r.setup.lp.Run(ctx, task)
}

// Close releases the session.
func (r *sessionRunner) Close() { r.setup.Close() }
