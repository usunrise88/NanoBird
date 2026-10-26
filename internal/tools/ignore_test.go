package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitignoreHidesEntriesFromListAndSearch(t *testing.T) {
	env := newEnv(t)
	root := env.Workspace.Root()
	for _, f := range []string{"build/out.txt", "src/app.txt", "debug.log", "keep.log"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("build/\n*.log\n!keep.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := call(t, env, "list_dir", `{"path": ".", "depth": 2}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(list, "build") || strings.Contains(list, "debug.log") || !strings.Contains(list, "keep.log") || !strings.Contains(list, "src/") {
		t.Fatalf("list_dir = %q", list)
	}
	found, err := call(t, env, "search_files", `{"pattern": "needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(found, "build") || strings.Contains(found, "debug.log") || !strings.Contains(found, "keep.log") || !strings.Contains(found, "app.txt") {
		t.Fatalf("search_files = %q", found)
	}
}
