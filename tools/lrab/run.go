package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// runAll runs every task the given number of times, one cell at a time, so that cells
// never share the machine (SPEC §10.1). Each cell is written as soon as it ends, and
// the summary is written last.
func runAll(ctx context.Context, cfg cellConfig, tasks []Task, reps int) ([]cell, error) {
	var cells []cell
	for _, t := range tasks {
		for rep := 1; rep <= reps; rep++ {
			c, err := runCell(ctx, cfg, t, rep)
			if err != nil {
				return cells, fmt.Errorf("%s replication %d: %w", t.ID, rep, err)
			}
			if err := writeJSON(filepath.Join(cfg.out, t.ID, fmt.Sprintf("rep%d", rep), "cell.json"), c); err != nil {
				return cells, err
			}
			cells = append(cells, c)
		}
	}
	return cells, writeJSON(filepath.Join(cfg.out, "summary.json"), summarize(cells))
}

// summary is the mean score of each task and of the run, with the timeouts (SPEC §10.1).
type summary struct {
	Tasks    []taskSummary `json:"tasks"`
	Cells    int           `json:"cells"`
	Mean     float64       `json:"mean"`
	Timeouts int           `json:"timeouts"`
}

type taskSummary struct {
	Task     string  `json:"task"`
	Cells    int     `json:"cells"`
	Mean     float64 `json:"mean"`
	Timeouts int     `json:"timeouts"`
}

// summarize averages the cells of each task, in the order the tasks ran, and the run.
func summarize(cells []cell) summary {
	var s summary
	byTask := map[string]*taskSummary{}
	var order []string
	var sum float64
	for _, c := range cells {
		ts := byTask[c.Task]
		if ts == nil {
			ts = &taskSummary{Task: c.Task}
			byTask[c.Task] = ts
			order = append(order, c.Task)
		}
		total := 0.0
		if c.Score != nil {
			total = c.Score.Total
		}
		ts.Cells++
		ts.Mean += total
		sum += total
		s.Cells++
		if c.TimedOut {
			ts.Timeouts++
			s.Timeouts++
		}
	}
	for _, id := range order {
		ts := byTask[id]
		ts.Mean /= float64(ts.Cells)
		s.Tasks = append(s.Tasks, *ts)
	}
	if s.Cells > 0 {
		s.Mean = sum / float64(s.Cells)
	}
	return s
}

// writeJSON writes v as indented JSON to path, and creates its folders.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
