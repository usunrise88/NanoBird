package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usunrise88/nanobird/internal/config"
	"github.com/usunrise88/nanobird/internal/loop"
	"github.com/usunrise88/nanobird/internal/mcp"
	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/prefill"
	"github.com/usunrise88/nanobird/internal/safety"
	"github.com/usunrise88/nanobird/internal/session"
	"github.com/usunrise88/nanobird/internal/skills"
	"github.com/usunrise88/nanobird/internal/tools"
)

// runSetup is what one run needs in its workspace (SPEC §11.2): the transcript and
// its structured log, the tools and their environment, the loop, and the MCP
// servers that the run loaded. The headless run and each ACP session build one.
type runSetup struct {
	log     *session.Log
	logFile *os.File
	lp      *loop.Loop
	mgr     *mcp.Manager
}

// setupOptions are the choices that a run is built with.
type setupOptions struct {
	workspace  string
	resume     string // the session to continue, or "" for a new one
	cfg        config.Config
	client     model.Client
	level      slog.Level
	logs       io.Writer     // structured log lines go here too, at the same level
	unsafe     bool          // rings 1 to 3 off (SPEC §5.11)
	unattended bool          // a permission prompt is a denial
	observer   loop.Observer // nil when nobody watches the run
	warn       io.Writer     // notes about the run, such as an MCP server that did not start
}

// newRunSetup builds a run in its workspace. A resumed session reopens its
// transcript. Every session writes a structured log to .nanobird/logs.
func newRunSetup(ctx context.Context, o setupOptions) (*runSetup, error) {
	if err := refuseStrict(o.cfg); err != nil {
		return nil, err
	}
	ws, err := safety.NewWorkspace(o.workspace)
	if err != nil {
		return nil, err
	}
	now := time.Now
	var log *session.Log
	if o.resume != "" {
		log, err = session.Open(ws.Root(), o.resume, now)
	} else {
		log, err = session.Create(ws.Root(), session.NewID(now()), now)
	}
	if err != nil {
		return nil, err
	}
	logFile, err := openLog(filepath.Join(ws.Root(), ".nanobird", "logs"), log.ID())
	if err != nil {
		_ = log.Close()
		return nil, err
	}
	s := &runSetup{log: log, logFile: logFile}
	if err := s.build(ctx, ws, o, now); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// build fills in the environment, the loop and the MCP servers of the run.
func (s *runSetup) build(ctx context.Context, ws *safety.Workspace, o setupOptions, now func() time.Time) error {
	switches, err := mech.Parse(strings.Join(o.cfg.Mechanisms.Disable, ","))
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(io.MultiWriter(o.logs, s.logFile), &slog.HandlerOptions{Level: o.level}))
	shell := tools.DetectShell()
	env := &tools.Env{
		Workspace:    ws,
		Switches:     switches,
		Shell:        shell,
		ShellTimeout: time.Duration(o.cfg.Tools.ShellTimeoutS) * time.Second,
		Unsafe:       o.unsafe,
		Unattended:   o.unattended,
		Clock:        now,
	}
	list, err := skills.Load(skillDirs(ws.Root(), o.cfg)...)
	if err != nil {
		return err
	}
	env.Skills = list
	system := prefill.System(shell.Name)
	if len(list) > 0 {
		system += "\nSkills you can load with the skills tool:\n" + skills.Index(list)
	}
	loopCfg := loop.Config{
		System:          system,
		MaxTurns:        o.cfg.Loop.MaxTurns,
		OutputCapTokens: o.cfg.Loop.OutputCapTokens,
		Temperature:     o.cfg.Loop.Temperature,
		Thinking:        o.cfg.Loop.Thinking == "on",
		ThinkingPlan:    o.cfg.Loop.Thinking == "plan",
		ThinkingBudget:  o.cfg.Loop.ThinkingBudget,
		ResultCapBytes:  o.cfg.Tools.ResultCapBytes,
		RetryBackoff:    2 * time.Second,
		ContextTokens:   o.cfg.Loop.ContextTokens,
		CompactionAt:    o.cfg.Loop.CompactionAt,
	}
	mcpPath := o.cfg.MCP.Config
	if !filepath.IsAbs(mcpPath) {
		mcpPath = filepath.Join(ws.Root(), ".nanobird", mcpPath)
	}
	builtins := tools.Builtins()
	builtinNames := make([]string, 0, len(builtins))
	for _, t := range builtins {
		builtinNames = append(builtinNames, t.Name)
	}
	s.mgr, err = mcp.Load(ctx, mcpPath, o.cfg.MCP.FlatLimit, time.Duration(o.cfg.MCP.CallTimeoutS)*time.Second, builtinNames, o.cfg.Offline)
	if err != nil {
		return err
	}
	for _, w := range s.mgr.Warnings {
		fmt.Fprintf(o.warn, "nanobird: %s\n", w)
	}
	s.lp = loop.New(o.client, tools.NewRegistry(append(builtins, s.mgr.Tools()...)), env, s.log, loopCfg).WithLogger(logger)
	if o.observer != nil {
		s.lp = s.lp.WithObserver(o.observer)
	}
	return nil
}

// Close releases the run: its MCP servers, its log file and its transcript.
func (s *runSetup) Close() {
	if s.mgr != nil {
		s.mgr.Close()
	}
	_ = s.logFile.Close()
	_ = s.log.Close()
}

// openLog opens the structured log of a session. Its lines go to stderr as well.
func openLog(dir, id string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, id+".log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
}

// logLevelOf reads a level name: debug, info, warn or error.
func logLevelOf(name string) (slog.Level, error) {
	var level slog.Level
	err := level.UnmarshalText([]byte(name))
	return level, err
}

// refuseStrict says that tool_format strict waits for the native backend (SPEC §5.5).
func refuseStrict(cfg config.Config) error {
	if cfg.Loop.ToolFormat == "strict" {
		return errors.New("loop.tool_format strict needs the native NanoLLM backend (RFC R4), which is not built yet; use rescue")
	}
	return nil
}
