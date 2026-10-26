package session

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrashMovesTheTranscriptAndTheLog(t *testing.T) {
	root := t.TempDir()
	log, err := Create(root, "run-1", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Write("task", map[string]string{"text": "x"}); err != nil {
		t.Fatal(err)
	}
	_ = log.Close()
	logDir := filepath.Join(root, ".nanobird", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "run-1.log"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Trash(root, "run-1", time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if ids, _ := List(root); len(ids) != 0 {
		t.Fatalf("the session is still listed: %v", ids)
	}
	trashed, _ := os.ReadDir(filepath.Join(root, ".nanobird", "trash", "sessions"))
	if len(trashed) != 1 || !strings.HasPrefix(trashed[0].Name(), "run-1@") {
		t.Fatalf("trashed sessions = %v", trashed)
	}
	if _, err := os.Stat(filepath.Join(root, ".nanobird", "trash", "logs")); err != nil {
		t.Fatalf("the log was not trashed: %v", err)
	}
}

func TestTrashRefusesPathsAndMissingSessions(t *testing.T) {
	root := t.TempDir()
	if err := Trash(root, "../escape", time.Now()); err == nil {
		t.Fatal("a path was accepted as a session id")
	}
	if err := Trash(root, "missing", time.Now()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a missing session = %v, want not-exist", err)
	}
}
