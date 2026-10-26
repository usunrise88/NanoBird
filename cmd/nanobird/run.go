package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/usunrise88/nanobird/internal/loop"
	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/model/openai"
	"github.com/usunrise88/nanobird/internal/prefill"
	"github.com/usunrise88/nanobird/internal/safety"
	"github.com/usunrise88/nanobird/internal/session"
	"github.com/usunrise88/nanobird/internal/tools"
)

// runTask is `nanobird run --task FILE --cwd DIR` (SPEC §11.2): one prompt, run
// headless with the same loop as ACP. It prints the stop reason and the
// summary, and exits 0 only when the finish was accepted.
func runTask(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	taskFile := fs.String("task", "", "file with the task text (required)")
	cwd := fs.String("cwd", ".", "workspace directory")
	endpoint := fs.String("endpoint", envOr("NANOBIRD_MODEL_ENDPOINT", "http://127.0.0.1:8080"), "root of the OpenAI-compatible endpoint (SPEC §7.5)")
	model := fs.String("model", os.Getenv("NANOBIRD_MODEL_NAME"), "model id as the engine lists it (required)")
	keyEnv := fs.String("key-env", "NANOBIRD_MODEL_KEY", "environment variable that holds the API key")
	maxTurns := fs.Int("max-turns", 60, "turn budget per prompt (SPEC §4.3)")
	ablation := fs.String("ablation", os.Getenv("NANOBIRD_ABLATION"), "mechanisms to switch off, comma-separated (SPEC §5)")
	unsafe := fs.Bool("unsafe", os.Getenv("NANOBIRD_UNSAFE") == "1", "turn safety rings 1 to 3 off; ring 4 stays on (SPEC §5.11)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *taskFile == "" || *model == "" {
		fmt.Fprintln(stderr, "nanobird run: --task and --model are required")
		fs.Usage()
		return 2
	}
	raw, err := os.ReadFile(*taskFile)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 2
	}
	task := strings.TrimSpace(string(raw))
	if task == "" {
		fmt.Fprintf(stderr, "nanobird run: %s is empty\n", *taskFile)
		return 2
	}
	switches, err := mech.Parse(*ablation)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 2
	}
	ws, err := safety.NewWorkspace(*cwd)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 1
	}
	client, err := openai.New(openai.Config{BaseURL: *endpoint, Model: *model, APIKey: os.Getenv(*keyEnv)})
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 2
	}
	now := time.Now
	log, err := session.Create(ws.Root(), session.NewID(now()), now)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", err)
		return 1
	}
	defer func() { _ = log.Close() }()

	env := &tools.Env{
		Workspace:    ws,
		Switches:     switches,
		Shell:        tools.DetectShell(),
		ShellTimeout: 120 * time.Second,
		Unsafe:       *unsafe,
		// Headless runs have no one to ask, so a permission prompt is a denial.
		Unattended: true,
		Clock:      now,
	}
	cfg := loop.Config{
		System:          prefill.System(env.Shell.Name),
		MaxTurns:        *maxTurns,
		OutputCapTokens: 8192,
		Temperature:     0,
		ResultCapBytes:  16384,
		RetryBackoff:    2 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res := loop.New(client, tools.NewRegistry(tools.Builtins()), env, log, cfg).Run(ctx, task)

	fmt.Fprintf(stdout, "stop: %s\nturns: %d\ntranscript: %s\n", res.Stop, res.Turns, log.Path())
	if res.Summary != "" {
		fmt.Fprintf(stdout, "summary: %s\n", res.Summary)
	}
	if res.Err != nil {
		fmt.Fprintf(stderr, "nanobird run: %v\n", res.Err)
	}
	if res.Stop == loop.StopEndTurn {
		return 0
	}
	return 1
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
