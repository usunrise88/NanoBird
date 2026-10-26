package loop

import (
	"testing"

	"github.com/usunrise88/nanobird/internal/model"
)

func TestShortFollowUpAfterAWriteIsAContinuation(t *testing.T) {
	wrote := []model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "1", Name: "create_file"}}}}
	if !continues(wrote, "and the second one?") {
		t.Fatal("a short follow-up after a write was not a continuation")
	}
	if continues(nil, "and the second one?") {
		t.Fatal("a follow-up with no earlier write was a continuation")
	}
	if continues(wrote, "please explain the whole design of this module in detail with examples and tests for each part") {
		t.Fatal("a long prompt was a continuation")
	}
	if continues(wrote, "thanks") {
		t.Fatal("a thank-you was a continuation")
	}
}

func TestDecideKeepsAFollowUpOnItsTools(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig())
	h.loop.history = []model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "1", Name: "edit_file"}}}}
	if got := h.loop.decide("and the second one?"); got != ModeTask {
		t.Fatalf("decide = %s, want task", got)
	}
	if got := h.loop.decide("thanks"); got != ModeChat {
		t.Fatalf("decide(thanks) = %s, want chat", got)
	}
}
