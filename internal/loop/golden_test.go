package loop

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usunrise88/nanobird/internal/model/fake"
)

var update = flag.Bool("update", false, "rewrite the golden transcripts from the current behaviour")

// TestGoldenTranscripts replays every scenario in testdata/golden against the
// fake model and compares the transcript with the golden copy (SPEC §5.10). A
// scenario is script.json (the replies), task.txt (the prompt), an optional
// files/ tree (the starting workspace) and transcript.jsonl (the expected record).
func TestGoldenTranscripts(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "golden")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			runGolden(t, dir)
		})
	}
}

func runGolden(t *testing.T, dir string) {
	t.Helper()
	var script []fake.Reply
	if err := json.Unmarshal(readFile(t, filepath.Join(dir, "script.json")), &script); err != nil {
		t.Fatalf("script.json: %v", err)
	}
	task := strings.TrimSpace(string(readFile(t, filepath.Join(dir, "task.txt"))))
	ws := t.TempDir()
	copyTree(t, filepath.Join(dir, "files"), ws)
	h := newHarness(t, ws, defaultConfig(), script...)
	res := h.loop.Run(context.Background(), task)
	if err := h.log.Close(); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, h.log.Path())
	golden := filepath.Join(dir, "transcript.jsonl")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want := string(readFile(t, golden))
	if string(got) != want {
		t.Errorf("transcript differs from %s; check the change, then run with -update\n%s", golden, firstDifference(want, string(got)))
	}
	if n := h.client.Remaining(); n != 0 {
		t.Errorf("%d scripted replies were never played", n)
	}
	if res.Stop != StopEndTurn {
		t.Errorf("stop = %s (%v), want end_turn", res.Stop, res.Err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// copyTree copies src into dst. A missing src is an empty tree.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// firstDifference names the first line where want and got part.
func firstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("line %d\n  want: %s\n  got:  %s", i+1, wl, gl)
		}
	}
	return "identical"
}
