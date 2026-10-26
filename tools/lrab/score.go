package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// Score is the scorer's result for one cell, with the judge off (SPEC §10.1). The
// scorer renormalizes its weights without the judge, so the total is the weighted mean
// of the milestone and final shares.
type Score struct {
	Total     float64 `json:"total"`
	Milestone float64 `json:"milestone_score"`
	Final     float64 `json:"final_score"`
}

// score runs LRAB's scorer, unchanged, on a workdir. It returns the parsed score and
// the scorer's whole JSON output. The judge is off: it would call a model server, and
// the runs use no Ollama (docs/ROADMAP.md).
func score(ctx context.Context, python, scorer string, t Task, workdir string) (Score, json.RawMessage, error) {
	cmd := exec.CommandContext(ctx, python, scorer, "--task", t.File, "--workdir", workdir, "--no-judge")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Score{}, nil, fmt.Errorf("scorer: %w: %s", err, stderr.String())
	}
	var s Score
	if err := json.Unmarshal(stdout.Bytes(), &s); err != nil {
		return Score{}, nil, fmt.Errorf("scorer output: %w", err)
	}
	return s, json.RawMessage(stdout.Bytes()), nil
}
