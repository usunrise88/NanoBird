package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	sdk "github.com/coder/acp-go-sdk"
)

// cancelGrace is how long a cell waits for the agent to stop after a cancel (SPEC §10.1).
const cancelGrace = 30 * time.Second

// cellConfig is what the cells of a run share.
type cellConfig struct {
	agent  []string      // the agent's program and its arguments
	root   string        // the LRAB directory
	python string        // the interpreter of the scorer
	budget time.Duration // replaces the budget of SPEC §10.1 when it is not zero
	allow  bool          // answer permission requests with allow once
	ablate []string      // mechanisms that the session leaves off
	score  bool          // grade the workdir with the scorer
	out    string        // the results directory of the run
}

// cell is one replication of one task, written as cell.json.
type cell struct {
	Task     string          `json:"task"`
	Rep      int             `json:"rep"`
	Budget   string          `json:"budget"`
	Stop     string          `json:"stop,omitempty"`
	TimedOut bool            `json:"timed_out"`
	Seconds  float64         `json:"seconds"`
	Score    *Score          `json:"score,omitempty"`
	Scorer   json.RawMessage `json:"scorer,omitempty"`
	Firings  map[string]int  `json:"firings"`
	Stats    json.RawMessage `json:"stats,omitempty"`
	Error    string          `json:"error,omitempty"`
	Workdir  string          `json:"workdir"`
}

// runCell runs one replication of a task in a fresh workdir (SPEC §10.1). A timeout
// scores 0. Any other stop is graded on the artifacts that the workdir holds.
func runCell(ctx context.Context, cfg cellConfig, t Task, rep int) (cell, error) {
	dir := filepath.Join(cfg.out, t.ID, fmt.Sprintf("rep%d", rep))
	workdir := filepath.Join(dir, "work")
	budget := t.budget(cfg.budget)
	c := cell{Task: t.ID, Rep: rep, Budget: budget.String(), Workdir: workdir}
	if err := prepareWorkdir(cfg.root, t.Fixtures, workdir); err != nil {
		return c, err
	}
	log, err := os.Create(filepath.Join(dir, "agent.log"))
	if err != nil {
		return c, err
	}
	defer log.Close()

	started := time.Now()
	o := drive(ctx, cfg, t, workdir, budget, log)
	c.Seconds = time.Since(started).Seconds()
	c.Stop, c.TimedOut, c.Stats, c.Firings = o.stop, o.timedOut, o.stats, o.firings
	if o.err != nil {
		c.Error = o.err.Error()
	}
	// The agent's own folder is kept for audit, and it stays out of the artifact checks
	// (SPEC §10.1).
	if err := moveAside(filepath.Join(workdir, ".nanobird"), filepath.Join(dir, "nanobird")); err != nil {
		return c, err
	}
	switch {
	case c.TimedOut:
		c.Score = &Score{}
	case cfg.score:
		s, raw, err := score(ctx, cfg.python, filepath.Join(cfg.root, "scoring", "score_task.py"), t, workdir)
		if err != nil {
			return c, err
		}
		c.Score, c.Scorer = &s, raw
	}
	return c, nil
}

// outcome is what the client saw of one cell.
type outcome struct {
	stop     string
	timedOut bool
	stats    json.RawMessage
	firings  map[string]int
	err      error
}

// drive runs the agent in the workdir and talks to it over ACP: initialize, one session
// in the workdir, the ablation, then one prompt that the budget bounds. The agent
// process ends with the cell.
func drive(ctx context.Context, cfg cellConfig, t Task, workdir string, budget time.Duration, log io.Writer) outcome {
	cmd := exec.CommandContext(ctx, cfg.agent[0], cfg.agent[1:]...)
	cmd.Dir = workdir
	cmd.Stderr = log
	in, err := cmd.StdinPipe()
	if err != nil {
		return outcome{err: err}
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return outcome{err: err}
	}
	if err := cmd.Start(); err != nil {
		return outcome{err: err}
	}
	cl := newClient(cfg.allow)
	conn := sdk.NewClientSideConnection(cl, in, out)
	conn.SetLogger(slog.New(slog.NewTextHandler(log, nil)))
	defer func() {
		_ = in.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	setup, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	impl := &sdk.Implementation{Name: "lrab", Version: "1"}
	if _, err := conn.Initialize(setup, sdk.InitializeRequest{ProtocolVersion: sdk.ProtocolVersionNumber, ClientInfo: impl}); err != nil {
		return outcome{err: fmt.Errorf("initialize: %w", err), firings: cl.counts()}
	}
	sess, err := conn.NewSession(setup, sdk.NewSessionRequest{Cwd: workdir, McpServers: []sdk.McpServer{}})
	if err != nil {
		return outcome{err: fmt.Errorf("session/new: %w", err), firings: cl.counts()}
	}
	if len(cfg.ablate) > 0 {
		params := map[string]any{"sessionId": sess.SessionId, "mechanisms": cfg.ablate}
		if _, err := conn.CallExtension(setup, "_nanobird/ablation", params); err != nil {
			return outcome{err: fmt.Errorf("_nanobird/ablation: %w", err), firings: cl.counts()}
		}
	}

	o := outcome{}
	o.stop, o.timedOut, o.err = prompt(ctx, conn, sess.SessionId, t.Prompt, budget)
	statsCtx, cancelStats := context.WithTimeout(context.Background(), time.Minute)
	defer cancelStats()
	stats, err := conn.CallExtension(statsCtx, "_nanobird/stats", map[string]any{"sessionId": sess.SessionId})
	if err != nil && o.err == nil {
		o.err = fmt.Errorf("_nanobird/stats: %w", err)
	}
	o.stats = stats
	o.firings = cl.counts()
	return o
}

// prompt sends the task as one session/prompt and waits for its stop. When the budget
// runs out, it cancels the prompt and waits up to cancelGrace for the stop. It returns
// the stop reason, whether the budget ran out, and any error.
func prompt(ctx context.Context, conn *sdk.ClientSideConnection, id sdk.SessionId, task string, budget time.Duration) (string, bool, error) {
	type reply struct {
		res sdk.PromptResponse
		err error
	}
	done := make(chan reply, 1)
	go func() {
		res, err := conn.Prompt(context.Background(), sdk.PromptRequest{SessionId: id, Prompt: []sdk.ContentBlock{sdk.TextBlock(task)}})
		done <- reply{res, err}
	}()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case r := <-done:
		return string(r.res.StopReason), false, r.err
	case <-ctx.Done():
		cancelPrompt(conn, id)
		return "", false, ctx.Err()
	case <-timer.C:
	}
	cancelPrompt(conn, id)
	select {
	case r := <-done:
		return string(r.res.StopReason), true, r.err
	case <-time.After(cancelGrace):
		return "", true, fmt.Errorf("the agent did not stop within %s of the cancel", cancelGrace)
	}
}

// cancelPrompt asks the agent to stop the session's prompt (SPEC §8.5).
func cancelPrompt(conn *sdk.ClientSideConnection, id sdk.SessionId) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_ = conn.Cancel(ctx, sdk.CancelNotification{SessionId: id})
}

// moveAside moves dir to dst when dir exists.
func moveAside(dir, dst string) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.Rename(dir, dst)
}
