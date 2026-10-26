package safety

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newWorkspace(t *testing.T) *Workspace {
	t.Helper()
	ws, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	return ws
}

func TestResolveKeepsPathsInside(t *testing.T) {
	ws := newWorkspace(t)
	for _, p := range []string{"a.txt", "src/new/file.go", "./b.txt"} {
		got, err := ws.Resolve(p)
		if err != nil {
			t.Errorf("Resolve(%q): %v", p, err)
			continue
		}
		if rel, err := ws.Rel(got); err != nil || rel == "" {
			t.Errorf("Resolve(%q) = %s, not inside the workspace", p, got)
		}
	}
}

func TestResolveRefusesEscapes(t *testing.T) {
	ws := newWorkspace(t)
	for _, p := range []string{"", "   ", "../outside.txt", "a/../../outside.txt", "/etc/passwd", `\\server\share`} {
		if _, err := ws.Resolve(p); err == nil {
			t.Errorf("Resolve(%q) was accepted", p)
		}
	}
}

func TestResolveRefusesSymlinkOutside(t *testing.T) {
	ws := newWorkspace(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws.Root(), "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ws.Resolve("link/x.txt"); err == nil {
		t.Fatal("a symlink to a directory outside the workspace was followed")
	}
}

func TestNewWorkspaceNeedsADirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWorkspace(file); err == nil {
		t.Fatal("a file was accepted as the workspace")
	}
}

func TestBackupAndLatestBackup(t *testing.T) {
	ws := newWorkspace(t)
	abs := filepath.Join(ws.Root(), "notes", "a.txt")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := ws.Backup(abs, t0); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if err := os.WriteFile(abs, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Backup(abs, t0.Add(time.Second)); err != nil {
		t.Fatalf("second Backup: %v", err)
	}
	latest, err := ws.LatestBackup(abs)
	if err != nil {
		t.Fatalf("LatestBackup: %v", err)
	}
	data, err := os.ReadFile(latest)
	if err != nil || string(data) != "second" {
		t.Fatalf("latest backup = %q, %v; want the second version", data, err)
	}
}

func TestTrashMovesTheFile(t *testing.T) {
	ws := newWorkspace(t)
	abs := filepath.Join(ws.Root(), "gone.txt")
	if err := os.WriteFile(abs, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := ws.Trash(abs, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatalf("the file is still in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Root(), filepath.FromSlash(rel))); err != nil {
		t.Fatalf("trashed file missing at %s: %v", rel, err)
	}
}

func TestAssessShell(t *testing.T) {
	cases := []struct {
		cmd  string
		want Level
	}{
		{"echo hello", Allow},
		{"go test ./...", Allow},
		{"rm -rf build", Allow},
		{"Remove-Item -Recurse -Force .\\build", Allow},
		{"rm -rf /", Ask},
		{"rm -rf ../other", Ask},
		{`rd /s /q C:\temp`, Ask},
		{"apt-get remove git", Ask},
		{`reg add HKCU\Software\x /v y /d z`, Ask},
		{"format C:", Refuse},
		{"mkfs.ext4 /dev/sdb1", Refuse},
		{"dd if=/dev/zero of=/dev/sda bs=1M", Refuse},
		{"dism /online /cleanup-image /restorehealth", Refuse},
		{"wsl --unregister Ubuntu", Refuse},
	}
	for _, c := range cases {
		if got := AssessShell(c.cmd).Level; got != c.want {
			t.Errorf("AssessShell(%q) = %d, want %d", c.cmd, got, c.want)
		}
	}
}

func TestTouchesSensitive(t *testing.T) {
	cases := map[string]bool{
		"cat ~/.ssh/id_rsa":                 true,
		`type C:\Users\me\.aws\credentials`: true,
		"cat /home/me/.env":                 true,
		"cat ../.env":                       true,
		"cat .env":                          false,
		"go test ./...":                     false,
	}
	for cmd, want := range cases {
		if _, got := TouchesSensitive(cmd); got != want {
			t.Errorf("TouchesSensitive(%q) = %v, want %v", cmd, got, want)
		}
	}
}
