package safety

import "testing"

func TestIgnoreEntriesMatchAsGitReadsThem(t *testing.T) {
	ig := ParseIgnore("# build output\nbuild/\n*.log\n!keep.log\n/root.txt\ndocs/*.md\n**/tmp\n[ab].txt\n")
	for _, c := range []struct {
		rel     string
		isDir   bool
		ignored bool
	}{
		{"build", true, true},
		{"build", false, false},
		{"src/build", true, true},
		{"a.log", false, true},
		{"x/y/a.log", false, true},
		{"keep.log", false, false},
		{"root.txt", false, true},
		{"sub/root.txt", false, false},
		{"docs/a.md", false, true},
		{"docs/x/a.md", false, false},
		{"tmp", true, true},
		{"a/b/tmp", true, true},
		{"a.txt", false, true},
		{"c.txt", false, false},
		{"notes.txt", false, false},
	} {
		if got := ig.Ignored(c.rel, c.isDir); got != c.ignored {
			t.Errorf("Ignored(%q, dir=%v) = %v, want %v", c.rel, c.isDir, got, c.ignored)
		}
	}
}

func TestBlankAndBrokenEntriesAreSkipped(t *testing.T) {
	ig := ParseIgnore("\n# only a comment\n!\n/\n[unclosed\n")
	if len(ig.rules) != 1 {
		t.Fatalf("rules = %d, want the one entry that compiles", len(ig.rules))
	}
}

func TestMissingGitignoreIgnoresNothing(t *testing.T) {
	ig, err := LoadIgnore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ig.Ignored("anything.log", false) {
		t.Fatal("a workspace with no .gitignore ignored a file")
	}
}
