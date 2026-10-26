package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/usunrise88/nanobird/internal/config"
	"github.com/usunrise88/nanobird/internal/loop"
	"github.com/usunrise88/nanobird/internal/mcp"
	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/prefill"
	"github.com/usunrise88/nanobird/internal/safety"
	"github.com/usunrise88/nanobird/internal/session"
	"github.com/usunrise88/nanobird/internal/skills"
	"github.com/usunrise88/nanobird/internal/tools"
)

// runTask is `nanobird run --task FILE` (SPEC §11.2): one prompt, run headless
// with the same loop as ACP. It prints the stop reason, the summary and the
// transcript path, and exits 0 only when the finish was accepted.
func runTask(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	taskFile := fs.String("task", "", "file with the task text (required, unless --resume is given)")
	resume := fs.String("resume", "", "continue the session with this id from its last checkpoint (SPEC §9.3)")
	logLevel := fs.String("log-level", envDefault("NANOBIRD_LOG_LEVEL", "info"), "debug, info, warn or error; prompts are logged at debug only (SPEC §9.4)")
	unsafe := fs.Bool("unsafe", false, "turn rings 1 to 3 off; ring 4 stays on (NANOBIRD_UNSAFE=1 does the same)")
	flags := addEngineFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *taskFile == "" && *resume == "" {
		fmt.Fprintln(stderr, "nanobird run: --task or --resume is required")
		fs.Usage()
		return 2
	}
	cfg, err := flags.load()
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 2
	}
	if cfg.Loop.ToolFormat == "strict" {
		fmt.Fprintln(stderr, "nanobird run: loop.tool_format strict needs the native NanoLLM backend (RFC R4), which is not built yet; use rescue")
		return 2
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		fmt.Fprintf(stderr, "nanobird run: --log-level: %v\n", err)
		return 2
	}
	task := ""
	if *taskFile != "" {
		raw, err := os.ReadFile(*taskFile)
		if err != nil {
			fmt.Fprintf(stderr, "nanobird run: %v\n", err)
			return 2
		}
		task = strings.TrimSpace(string(raw))
		if task == "" {
			fmt.Fprintf(stderr, "nanobird run: %s is empty\n", *taskFile)
			return 2
		}
	}
	switches, err := mech.Parse(strings.Join(cfg.Mechanisms.Disable, ","))
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 2
	}
	ws, err := safety.NewWorkspace(*flags.cwd)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 1
	}
	client, err := flags.client(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 2
	}
	now := time.Now
	var log *session.Log
	if *resume != "" {
		log, err = session.Open(ws.Root(), *resume, now)
	} else {
		log, err = session.Create(ws.Root(), session.NewID(now()), now)
	}
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 1
	}
	defer func() { _ = log.Close() }()
	logDir := filepath.Join(ws.Root(), ".nanobird", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 1
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, log.ID()+".log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 1
	}
	defer func() { _ = logFile.Close() }()
	logger := slog.New(slog.NewJSONHandler(io.MultiWriter(stderr, logFile), &slog.HandlerOptions{Level: level}))

	env := &tools.Env{
		Workspace:    ws,
		Switches:     switches,
		Shell:        tools.DetectShell(),
		ShellTimeout: time.Duration(cfg.Tools.ShellTimeoutS) * time.Second,
		Unsafe:       *unsafe || os.Getenv("NANOBIRD_UNSAFE") == "1",
		// Headless runs have no one to ask, so a permission prompt is a denial.
		Unattended: true,
		Clock:      now,
	}
	list, err := skills.Load(skillDirs(ws.Root(), cfg)...)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 1
	}
	env.Skills = list
	system := prefill.System(env.Shell.Name)
	if len(list) > 0 {
		system += "\nSkills you can load with the skills tool:\n" + skills.Index(list)
	}
	loopCfg := loop.Config{
		System:          system,
		MaxTurns:        cfg.Loop.MaxTurns,
		OutputCapTokens: cfg.Loop.OutputCapTokens,
		Temperature:     cfg.Loop.Temperature,
		Thinking:        cfg.Loop.Thinking == "on",
		ThinkingPlan:    cfg.Loop.Thinking == "plan",
		ThinkingBudget:  cfg.Loop.ThinkingBudget,
		ResultCapBytes:  cfg.Tools.ResultCapBytes,
		RetryBackoff:    2 * time.Second,
		ContextTokens:   cfg.Loop.ContextTokens,
		CompactionAt:    cfg.Loop.CompactionAt,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	mcpPath := cfg.MCP.Config
	if !filepath.IsAbs(mcpPath) {
		mcpPath = filepath.Join(ws.Root(), ".nanobird", mcpPath)
	}
	builtins := tools.Builtins()
	builtinNames := make([]string, 0, len(builtins))
	for _, t := range builtins {
		builtinNames = append(builtinNames, t.Name)
	}
	mgr, err := mcp.Load(ctx, mcpPath, cfg.MCP.FlatLimit, time.Duration(cfg.MCP.CallTimeoutS)*time.Second, builtinNames)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 1
	}
	defer mgr.Close()
	for _, w := range mgr.Warnings {
		fmt.Fprintf(stderr, "nanobird run: %s\n", w)
	}
	lp := loop.New(client, tools.NewRegistry(append(builtins, mgr.Tools()...)), env, log, loopCfg).WithLogger(logger)
	var res loop.Result
	if *resume != "" {
		res = lp.Resume(ctx)
	} else {
		res = lp.Run(ctx, task)
	}

	fmt.Fprintf(stdout, "stop: %s\nturns: %d\ntranscript: %s\n", res.Stop, res.Turns, log.Path())
	if res.Summary != "" {
		fmt.Fprintf(stdout, "summary: %s\n", res.Summary)
	}
	if res.Unverified {
		fmt.Fprintln(stdout, "unverified: the finish was accepted after repeated rejections (SPEC §5.3)")
	}
	if res.Blocker != "" {
		fmt.Fprintf(stdout, "blocker: %s\n", res.Blocker)
	}
	if res.Err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", res.Err)
	}
	// A run that ends unverified or blocked did not finish the task, so it fails.
	if res.Stop == loop.StopEndTurn && !res.Unverified && res.Blocker == "" {
		return 0
	}
	return 1
}

// envDefault reads an environment variable, with a fallback when it is unset.
func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// skillDirs lists where skills are read from: the workspace first, then the
// configured directories, then the user's config directory (SPEC §5.9).
func skillDirs(root string, cfg config.Config) []string {
	dirs := []string{filepath.Join(root, ".nanobird", "skills")}
	dirs = append(dirs, cfg.Skills.Dirs...)
	if dir, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(dir, "nanobird", "skills"))
	}
	return dirs
}
