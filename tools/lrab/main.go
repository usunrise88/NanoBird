// Command lrab runs LRAB tasks against an agent over ACP (SPEC §10.1). It is an ACP
// client: each cell gets a fresh workdir, one session/prompt with the task text, and a
// budget. LRAB's deterministic artifact scorer then grades the workdir, with the judge
// off. Results go to a directory of cell.json files and a summary.json.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func main() {
	os.Exit(mainWith(os.Args[1:], os.Stdout, os.Stderr))
}

func mainWith(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lrab", flag.ContinueOnError)
	fs.SetOutput(stderr)
	agent := fs.String("agent", "nanobird acp", "the agent command: its program and arguments, separated by spaces")
	root := fs.String("root", "tools/lrab", "the LRAB directory with tasks, fixtures and scoring")
	only := fs.String("only", "", "task ids to run, separated by commas; empty runs every dev task")
	reps := fs.Int("reps", 1, "replications of each task")
	budget := fs.Duration("budget", 0, "wall-clock budget of every cell; 0 uses SPEC §10.1, 90 minutes and 180 for LH tasks")
	permissions := fs.String("permissions", "deny", "answer to permission requests: deny, as an unattended run does, or allow")
	ablate := fs.String("ablate", "", "mechanisms that the session leaves off, separated by commas (SPEC §8.8)")
	python := fs.String("python", "python3", "the interpreter that runs the scorer")
	out := fs.String("out", "", "results directory; empty means .lrab/results/<UTC time>")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	argv := strings.Fields(*agent)
	if len(argv) == 0 {
		fmt.Fprintln(stderr, "lrab: -agent names no program")
		return 2
	}
	if *permissions != "deny" && *permissions != "allow" {
		fmt.Fprintln(stderr, "lrab: -permissions must be deny or allow")
		return 2
	}
	if *reps < 1 {
		fmt.Fprintln(stderr, "lrab: -reps must be at least 1")
		return 2
	}
	ids := splitList(*only)
	tasks, err := loadTasks(filepath.Join(*root, "tasks"), ids)
	if err != nil {
		fmt.Fprintf(stderr, "lrab: %v\n", err)
		return 1
	}
	if err := checkSelected(tasks, ids); err != nil {
		fmt.Fprintf(stderr, "lrab: %v\n", err)
		return 1
	}
	if _, err := exec.LookPath(*python); err != nil {
		fmt.Fprintf(stderr, "lrab: the scorer needs %s: %v\n", *python, err)
		return 1
	}
	dir := *out
	if dir == "" {
		dir = filepath.Join(".lrab", "results", time.Now().UTC().Format("20060102-150405"))
	}
	cfg := cellConfig{
		agent:  argv,
		root:   *root,
		python: *python,
		budget: *budget,
		allow:  *permissions == "allow",
		ablate: splitList(*ablate),
		score:  true,
		out:    dir,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cells, err := runAll(ctx, cfg, tasks, *reps)
	printSummary(stdout, summarize(cells))
	fmt.Fprintf(stdout, "results in %s\n", dir)
	if err != nil {
		fmt.Fprintf(stderr, "lrab: %v\n", err)
		return 1
	}
	return 0
}

// splitList splits a comma-separated flag value. An empty value is an empty list.
func splitList(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// checkSelected fails when a task id that was asked for is not in the task directory.
func checkSelected(tasks []Task, ids []string) error {
	for _, id := range ids {
		if !slices.ContainsFunc(tasks, func(t Task) bool { return t.ID == id }) {
			return fmt.Errorf("no task %s", id)
		}
	}
	return nil
}

// printSummary writes the mean score of each task and of the whole run.
func printSummary(w io.Writer, s summary) {
	for _, t := range s.Tasks {
		fmt.Fprintf(w, "%-6s cells %d  mean %.3f  timeouts %d\n", t.Task, t.Cells, t.Mean, t.Timeouts)
	}
	fmt.Fprintf(w, "all    cells %d  mean %.3f  timeouts %d\n", s.Cells, s.Mean, s.Timeouts)
}
