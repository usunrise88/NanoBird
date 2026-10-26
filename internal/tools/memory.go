package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxRecall bounds how many notes one recall returns.
const maxRecall = 50

var memoryStoreTool = Tool{
	Name:        "memory_store",
	Description: "Keep a note about this workspace in .nanobird/memory.md, for later tasks.",
	Parameters: schema(`{
		"text": {"type": "string", "description": "The note, on one line"}
	}`, "text"),
	run: runMemoryStore,
}

func runMemoryStore(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Text string `json:"text"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	text := strings.Join(strings.Fields(a.Text), " ")
	if text == "" {
		return "", errors.New("text is empty")
	}
	path := memoryPath(env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString("- " + text + "\n"); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return "stored the note", nil
}

var memoryRecallTool = Tool{
	Name:        "memory_recall",
	Description: "Find the notes kept for this workspace that contain a word, most recent first. An empty query returns the latest notes.",
	Parameters: schema(`{
		"query": {"type": "string", "description": "Word to look for"}
	}`),
	run: runMemoryRecall,
}

func runMemoryRecall(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	data, err := os.ReadFile(memoryPath(env))
	if errors.Is(err, fs.ErrNotExist) {
		return "no notes yet", nil
	}
	if err != nil {
		return "", err
	}
	q := strings.ToLower(strings.TrimSpace(a.Query))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var hits []string
	for i := len(lines) - 1; i >= 0 && len(hits) < maxRecall; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" && (q == "" || strings.Contains(strings.ToLower(line), q)) {
			hits = append(hits, line)
		}
	}
	if len(hits) == 0 {
		return "no notes match", nil
	}
	return strings.Join(hits, "\n"), nil
}

// memoryPath is the notes file of the workspace.
func memoryPath(env *Env) string {
	return filepath.Join(env.Workspace.Root(), ".nanobird", "memory.md")
}
