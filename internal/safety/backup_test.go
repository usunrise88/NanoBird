package safety

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFirstBackupAddsTheFolderToAnExistingGitignoreOnce(t *testing.T) {
	ws, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The workspace resolves its root, which on Windows can differ from the temp
	// path (RUNNER~1 is a short name), so the files are named from the root.
	root := ws.Root()
	ignore := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(ignore, []byte("build/"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	for i := range 2 {
		if _, err := ws.Backup(file, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(ignore)
	if err != nil {
		t.Fatal(err)
	}
	if want := "build/\n# NanoBird's backups and trash\n.nanobird/\n"; string(data) != want {
		t.Fatalf(".gitignore = %q, want %q", data, want)
	}
}

func TestBackupCreatesNoGitignoreWhenThereIsNone(t *testing.T) {
	ws, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The workspace resolves its root, which on Windows can differ from the temp
	// path (RUNNER~1 is a short name), so the files are named from the root.
	root := ws.Root()
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Backup(file, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf(".gitignore was created or unreadable: %v", err)
	}
}
