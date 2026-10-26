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

// defaultKillAtPct is where a resume test kills the agent when the task does not say
// (Mingbird's protocol kills at 50 percent of the budget).
const defaultKillAtPct = 50

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
	Resumed  bool            `json:"resumed,omitempty"`
	KilledAt float64         `json:"killed_at_seconds,omitempty"`
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
	o := runAttempts(ctx, cfg, t, workdir, budget, log)
	c.Seconds = time.Since(started).Seconds()
	c.Stop, c.TimedOut, c.Stats, c.Firings = o.stop, o.timedOut, o.stats, o.firings
	c.Resumed, c.KilledAt = o.resumed, o.killedAt.Seconds()
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

// runAttempts runs the agent of a cell. A task with a resume test (LRAB, SPEC §10.1) is
// killed at kill_at_pct of its budget, then started again in the same workdir with the
// rest of the budget, and the second attempt counts. A run that ends before the kill
// point is not interrupted, so it is not resumed.
func runAttempts(ctx context.Context, cfg cellConfig, t Task, workdir string, budget time.Duration, log io.Writer) outcome {
	if !t.ResumeTest {
		return drive(ctx, cfg, t, workdir, budget, "", false, log)
	}
	pct := t.KillAtPct
	if pct <= 0 || pct >= 100 {
		pct = defaultKillAtPct
	}
	killAt := budget * time.Duration(pct) / 100
	first := drive(ctx, cfg, t, workdir, killAt, "", true, log)
	if !first.killed {
		return first
	}
	second := drive(ctx, cfg, t, workdir, budget-killAt, first.id, false, log)
	second.resumed = true
	second.killedAt = killAt
	second.firings = mergeCounts(first.firings, second.firings)
	return second
}

// mergeCounts adds the firings of the second attempt to those of the first.
func mergeCounts(a, b map[string]int) map[string]int {
	out := make(map[string]int, len(a)+len(b))
	for k, v := range a {
		out[k] += v
	}
	for k, v := range b {
		out[k] += v
	}
	return out
}

// outcome is what the client saw of one attempt.
type outcome struct {
	id       sdk.SessionId
	stop     string
	timedOut bool
	killed   bool
	resumed  bool
	killedAt time.Duration
	stats    json.RawMessage
	firings  map[string]int
	err      error
}

// drive runs one attempt over ACP: the agent in the workdir starts a session, or resumes
// the session `resume` when that is not empty. Then it applies the ablation and sends the
// task as one prompt that the budget bounds. With kill set, the budget ends the attempt
// by killing the agent instead of cancelling the prompt. The agent process ends with the
// attempt.
func drive(ctx context.Context, cfg cellConfig, t Task, workdir string, budget time.Duration, resume sdk.SessionId, kill bool, log io.Writer) outcome {
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
	id, err := openSession(setup, conn, workdir, resume)
	if err != nil {
		return outcome{err: err, firings: cl.counts()}
	}
	if len(cfg.ablate) > 0 {
		params := map[string]any{"sessionId": id, "mechanisms": cfg.ablate}
		if _, err := conn.CallExtension(setup, "_nanobird/ablation", params); err != nil {
			return outcome{id: id, err: fmt.Errorf("_nanobird/ablation: %w", err), firings: cl.counts()}
		}
	}

	end := prompt(ctx, conn, id, t.Prompt, budget, kill)
	o := outcome{id: id, stop: end.stop, timedOut: end.timedOut, killed: end.killed, err: end.err}
	if o.killed {
		o.firings = cl.counts()
		return o
	}
	statsCtx, cancelStats := context.WithTimeout(context.Background(), time.Minute)
	defer cancelStats()
	stats, err := conn.CallExtension(statsCtx, "_nanobird/stats", map[string]any{"sessionId": id})
	if err != nil && o.err == nil {
		o.err = fmt.Errorf("_nanobird/stats: %w", err)
	}
	o.stats = stats
	o.firings = cl.counts()
	return o
}

// openSession starts a session in the workdir, or resumes the session `resume`.
func openSession(ctx context.Context, conn *sdk.ClientSideConnection, workdir string, resume sdk.SessionId) (sdk.SessionId, error) {
	if resume == "" {
		sess, err := conn.NewSession(ctx, sdk.NewSessionRequest{Cwd: workdir, McpServers: []sdk.McpServer{}})
		if err != nil {
			return "", fmt.Errorf("session/new: %w", err)
		}
		return sess.SessionId, nil
	}
	req := sdk.ResumeSessionRequest{SessionId: resume, Cwd: workdir, McpServers: []sdk.McpServer{}}
	if _, err := conn.ResumeSession(ctx, req); err != nil {
		return "", fmt.Errorf("session/resume: %w", err)
	}
	return resume, nil
}

// promptEnd is how a prompt ended.
type promptEnd struct {
	stop     string
	timedOut bool
	killed   bool
	err      error
}

// prompt sends the task as one session/prompt and waits for its stop. When the budget
// runs out, it cancels the prompt and waits up to cancelGrace for the stop. With kill
// set, it reports the attempt as killed and leaves the stopping to the caller.
func prompt(ctx context.Context, conn *sdk.ClientSideConnection, id sdk.SessionId, task string, budget time.Duration, kill bool) promptEnd {
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
		return promptEnd{stop: string(r.res.StopReason), err: r.err}
	case <-ctx.Done():
		cancelPrompt(conn, id)
		return promptEnd{err: ctx.Err()}
	case <-timer.C:
	}
	if kill {
		return promptEnd{killed: true}
	}
	cancelPrompt(conn, id)
	select {
	case r := <-done:
		return promptEnd{stop: string(r.res.StopReason), timedOut: true, err: r.err}
	case <-time.After(cancelGrace):
		return promptEnd{timedOut: true, err: fmt.Errorf("the agent did not stop within %s of the cancel", cancelGrace)}
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
