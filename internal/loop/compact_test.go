package loop

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/model/fake"
	"github.com/usunrise88/nanobird/internal/tools"
)

// longRun is a run state with twelve turns of listing, each with a long output.
func longRun(t *testing.T, h *harness, big string) *state {
	t.Helper()
	st := &state{
		task: "task",
		msgs: []model.Message{{Role: model.RoleSystem, Content: "system"}, {Role: model.RoleUser, Content: "task"}},
		anti: mech.NewLoopTracker(),
		gate: mech.NewGate(),
		conv: mech.NewConvergence(),
	}
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("c%d", i)
		st.msgs = append(st.msgs,
			model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: id, Name: "list_dir", Arguments: "{}"}}},
			model.Message{Role: model.RoleTool, ToolCallID: id, Content: big})
	}
	st.lastPrompt = 500
	st.sentLen = len(st.msgs)
	return st
}

func compactionConfig() Config {
	cfg := defaultConfig()
	cfg.ContextTokens = 400
	cfg.CompactionAt = 0.5
	return cfg
}

func TestCompactionDigestsThenSummarises(t *testing.T) {
	h := newHarness(t, t.TempDir(), compactionConfig(), fake.Reply{Text: "The earlier steps listed files twice."})
	big := strings.Repeat("x", 1000)
	st := longRun(t, h, big)
	if err := h.loop.compactIfNeeded(context.Background(), 13, st); err != nil {
		t.Fatalf("compactIfNeeded: %v", err)
	}
	if len(st.msgs) != 2+1+keepTail {
		t.Fatalf("%d messages after compaction, want %d", len(st.msgs), 2+1+keepTail)
	}
	if !strings.Contains(st.msgs[2].Content, "listed files twice") {
		t.Fatalf("summary = %q", st.msgs[2].Content)
	}
	if st.msgs[len(st.msgs)-1].Content != big {
		t.Fatal("the latest output was compacted")
	}
	text := transcript(t, h.log.Path())
	if !strings.Contains(text, `"level":1`) || !strings.Contains(text, `"level":3`) {
		t.Fatalf("both levels should be recorded:\n%s", text)
	}
}

func TestCompactionKeepsThePlanVerbatim(t *testing.T) {
	h := newHarness(t, t.TempDir(), compactionConfig(), fake.Reply{Text: "summary"})
	h.env.Plan.Items = []tools.PlanItem{{Text: "write the report"}}
	st := longRun(t, h, strings.Repeat("x", 1000))
	if err := h.loop.compactIfNeeded(context.Background(), 13, st); err != nil {
		t.Fatalf("compactIfNeeded: %v", err)
	}
	if !strings.Contains(st.msgs[2].Content, "The plan, kept verbatim") || !strings.Contains(st.msgs[2].Content, "write the report") {
		t.Fatalf("plan not re-injected: %q", st.msgs[2].Content)
	}
}

func TestCompactionSwitchOffKeepsTheContext(t *testing.T) {
	h := newHarness(t, t.TempDir(), compactionConfig())
	off, err := mech.Parse(mech.Compaction)
	if err != nil {
		t.Fatal(err)
	}
	h.env.Switches = off
	st := longRun(t, h, strings.Repeat("x", 1000))
	before := len(st.msgs)
	if err := h.loop.compactIfNeeded(context.Background(), 13, st); err != nil || len(st.msgs) != before {
		t.Fatalf("switched off, yet the context changed: %d -> %d (%v)", before, len(st.msgs), err)
	}
}
