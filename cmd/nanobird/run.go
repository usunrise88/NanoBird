package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/usunrise88/nanobird/internal/config"
	"github.com/usunrise88/nanobird/internal/loop"
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
	cfg, err := flags.load(*flags.cwd)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 2
	}
	if err := refuseStrict(cfg); err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 2
	}
	level, err := logLevelOf(*logLevel)
	if err != nil {
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
	client, err := flags.client(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := newRunSetup(ctx, setupOptions{
		workspace:  *flags.cwd,
		resume:     *resume,
		cfg:        cfg,
		client:     client,
		level:      level,
		logs:       stderr,
		unsafe:     *unsafe || os.Getenv("NANOBIRD_UNSAFE") == "1",
		unattended: true,
		warn:       stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 1
	}
	defer s.Close()
	var res loop.Result
	if *resume != "" {
		res = s.lp.Resume(ctx)
	} else {
		res = s.lp.Run(ctx, task)
	}

	fmt.Fprintf(stdout, "stop: %s\nturns: %d\ntranscript: %s\n", res.Stop, res.Turns, s.log.Path())
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
