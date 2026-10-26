package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCrawlBudgetIsTwiceTheFileBetween64And512KB(t *testing.T) {
	cases := []struct{ size, want int }{{1 << 10, 64 << 10}, {100 << 10, 200 << 10}, {1 << 20, 512 << 10}}
	for _, c := range cases {
		if got := crawlBudget(c.size); got != c.want {
			t.Fatalf("a file of %d bytes has budget %d, want %d", c.size, got, c.want)
		}
	}
}

func TestReadsPastTheBudgetAreThrottledNotRefused(t *testing.T) {
	env := newEnv(t)
	body := strings.Repeat("a line of plain text for the reader\n", 40)
	if err := os.WriteFile(filepath.Join(env.Workspace.Root(), "big.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out string
	for i := 0; i < 80; i++ {
		var err error
		out, err = runReadFile(context.Background(), env, json.RawMessage(`{"path":"big.txt"}`))
		if err != nil {
			t.Fatalf("read %d was refused: %v", i+1, err)
		}
	}
	if !strings.Contains(out, "[crawl guard:") || len(out) > 2600 {
		t.Fatalf("the read past the budget is %d bytes, with the throttle note: %v", len(out), strings.Contains(out, "[crawl guard:"))
	}
}

func TestHintsComeOnTheFourthAndTenthReadAndTheDataStaysIntact(t *testing.T) {
	env := newEnv(t)
	if err := os.WriteFile(filepath.Join(env.Workspace.Root(), "small.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var hints []int
	for i := 1; i <= 12; i++ {
		out, err := runReadFile(context.Background(), env, json.RawMessage(`{"path":"small.txt"}`))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "[hint:") {
			hints = append(hints, i)
		}
		if !strings.Contains(out, "two") {
			t.Fatalf("read %d lost its data: %q", i, out)
		}
	}
	if !slices.Equal(hints, []int{4, 10}) {
		t.Fatalf("hints on reads %v, want 4 and 10", hints)
	}
}

func TestResetCrawlStartsTheCountAgain(t *testing.T) {
	env := newEnv(t)
	body := strings.Repeat("a line of plain text for the reader\n", 40)
	if err := os.WriteFile(filepath.Join(env.Workspace.Root(), "big.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 80; i++ {
		if _, err := runReadFile(context.Background(), env, json.RawMessage(`{"path":"big.txt"}`)); err != nil {
			t.Fatal(err)
		}
	}
	env.ResetCrawl()
	out, err := runReadFile(context.Background(), env, json.RawMessage(`{"path":"big.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "[crawl guard:") {
		t.Fatal("a new task still carries the throttle of the old one")
	}
}
