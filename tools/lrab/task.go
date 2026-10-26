package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Task is one LRAB task. The runner reads the fields that a cell needs; the scorer
// reads the milestones and the final checks from the same file.
type Task struct {
	ID             string   `json:"id"`
	Domain         string   `json:"domain"`
	Fixtures       []string `json:"fixtures"`
	Prompt         string   `json:"prompt"`
	MaxWallMinutes int      `json:"max_wall_minutes"`
	ResumeTest     bool     `json:"resume_test"`
	KillAtPct      int      `json:"kill_at_pct"`

	// File is the task file on disk, which the scorer reads.
	File string `json:"-"`
}

// budget is the wall-clock budget of a cell (SPEC §10.1): 90 minutes for a workflow
// cell and 180 for a long-horizon cell. A non-zero override replaces both.
func (t Task) budget(override time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	if strings.HasPrefix(t.ID, "LH-") {
		return 180 * time.Minute
	}
	return 90 * time.Minute
}

// loadTasks reads the task files under dir, sorted by id. When ids is not empty, only
// those tasks are kept. Held-out tasks are never read, because only dir is walked.
func loadTasks(dir string, ids []string) ([]Task, error) {
	var tasks []Task
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var t Task
		if err := json.Unmarshal(data, &t); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		t.File = path
		if len(ids) == 0 || slices.Contains(ids, t.ID) {
			tasks = append(tasks, t)
		}
		return nil
	})
	slices.SortFunc(tasks, func(a, b Task) int { return strings.Compare(a.ID, b.ID) })
	return tasks, err
}
