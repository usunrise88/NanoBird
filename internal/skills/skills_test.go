package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadReadsFolderAndFileSkills(t *testing.T) {
	ws := t.TempDir()
	write(t, filepath.Join(ws, "release", "SKILL.md"), "---\nname: release\ndescription: Cut a release tag.\n---\nTag the commit, then push.\n")
	write(t, filepath.Join(ws, "lint.md"), "Run the linter before finishing.\n")
	list, err := Load(ws, filepath.Join(ws, "missing"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Directory entries come back sorted by name: lint.md before release/.
	if len(list) != 2 || list[0].Name != "lint" || list[1].Name != "release" || list[1].Description != "Cut a release tag." {
		t.Fatalf("skills = %+v", list)
	}
	body, err := Body(list[1])
	if err != nil || body != "Tag the commit, then push." {
		t.Fatalf("body = %q, %v", body, err)
	}
	if idx := Index(list); !strings.Contains(idx, "- release: Cut a release tag.") {
		t.Fatalf("index = %q", idx)
	}
}

func TestEarlierDirectoryWins(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, filepath.Join(a, "x.md"), "---\ndescription: first\n---\n")
	write(t, filepath.Join(b, "x.md"), "---\ndescription: second\n---\n")
	list, err := Load(a, b)
	if err != nil || len(list) != 1 || list[0].Description != "first" {
		t.Fatalf("skills = %+v, %v", list, err)
	}
}
