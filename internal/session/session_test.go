package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTranscriptIsOneRecordPerLine(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { now = now.Add(time.Second); return now }
	log, err := Create(root, "run-1", clock)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := log.Write("request", map[string]any{"turn": 1}); err != nil {
		t.Fatal(err)
	}
	if err := log.Write("stop", map[string]any{"reason": "end_turn"}); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".nanobird", "sessions", "run-1", "transcript.jsonl"); log.Path() != want {
		t.Fatalf("Path() = %s, want %s", log.Path(), want)
	}
	f, err := os.Open(log.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var kinds []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec struct {
			Seq  int    `json:"seq"`
			Time string `json:"time"`
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("record is not JSON: %v", err)
		}
		if rec.Seq != len(kinds)+1 || !strings.HasSuffix(rec.Time, "Z") {
			t.Fatalf("record %d has seq %d and time %q", len(kinds)+1, rec.Seq, rec.Time)
		}
		kinds = append(kinds, rec.Kind)
	}
	if strings.Join(kinds, ",") != "request,stop" {
		t.Fatalf("kinds = %v", kinds)
	}
}

func TestCreateRefusesAnExistingSession(t *testing.T) {
	root := t.TempDir()
	clock := func() time.Time { return time.Unix(0, 0) }
	first, err := Create(root, "same", clock)
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	if _, err := Create(root, "same", clock); err == nil {
		t.Fatal("a second transcript replaced the first")
	}
}
