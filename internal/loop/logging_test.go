package loop

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// logsAt runs one task with a logger at level and returns what it logged.
func logsAt(t *testing.T, level slog.Level) string {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level}))
	h := newHarness(t, t.TempDir(), defaultConfig(), listDir(), finishWith("ok"), finishWith("ok"))
	h.loop.WithLogger(logger)
	if res := h.loop.Run(context.Background(), "list the secret files, then finish"); res.Stop != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	return buf.String()
}

func TestInfoLogsLeaveThePromptOut(t *testing.T) {
	out := logsAt(t, slog.LevelInfo)
	if strings.Contains(out, "secret files") {
		t.Fatalf("the prompt reached the info log:\n%s", out)
	}
	if !strings.Contains(out, `"msg":"task started"`) || !strings.Contains(out, `"msg":"run stopped"`) {
		t.Fatalf("the run's start and stop are missing:\n%s", out)
	}
}

func TestDebugLogsCarryThePrompt(t *testing.T) {
	if out := logsAt(t, slog.LevelDebug); !strings.Contains(out, "secret files") {
		t.Fatalf("the prompt is missing at debug level:\n%s", out)
	}
}

func TestThinkingPlanThinksOnTheFirstTurnAndAfterARejection(t *testing.T) {
	cfg := defaultConfig()
	cfg.ThinkingPlan = true
	cfg.ThinkingBudget = 256
	h := newHarness(t, t.TempDir(), cfg, listDir(), finishWith("ok"), finishWith("ok"))
	if res := h.loop.Run(context.Background(), "list the files, then finish"); res.Stop != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	want := []bool{true, false, true}
	for i, w := range want {
		req := h.client.Requests[i]
		got := req.Thinking != nil && *req.Thinking
		if got != w {
			t.Fatalf("turn %d: thinking = %v, want %v", i+1, got, w)
		}
		if w && req.ReasoningBudget != 256 {
			t.Fatalf("turn %d: reasoning budget = %d, want 256", i+1, req.ReasoningBudget)
		}
	}
}
