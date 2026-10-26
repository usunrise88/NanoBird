package loop

import (
	"testing"

	"github.com/usunrise88/nanobird/internal/config"
	"github.com/usunrise88/nanobird/internal/mech"
)

// TestTheLoopThresholdsAreTheSpecs pins the numbers that the spec names. CI carries the
// loop thresholds (SPEC §5.10), so a change to any of them is a change to the spec.
func TestTheLoopThresholdsAreTheSpecs(t *testing.T) {
	if mech.SameCallLimit != 6 || mech.IdleLimit != 15 || mech.LadderLimit != 6 {
		t.Fatalf("anti-loop limits %d, %d, %d; the spec says 6 identical calls, 15 idle calls and 6 failed turns per rung (SPEC §5.4)",
			mech.SameCallLimit, mech.IdleLimit, mech.LadderLimit)
	}
	d := config.Defaults()
	if d.Loop.MaxTurns != 60 {
		t.Fatalf("max_turns %d, want 60", d.Loop.MaxTurns)
	}
	if d.Loop.OutputCapTokens != 8192 {
		t.Fatalf("output cap %d tokens, want 8192 (SPEC §6.3)", d.Loop.OutputCapTokens)
	}
	if d.Loop.ThinkingBudget != 1024 {
		t.Fatalf("thinking budget %d, want 1024 (SPEC §4.5)", d.Loop.ThinkingBudget)
	}
	if d.Tools.ResultCapBytes != 16384 {
		t.Fatalf("tool result cap %d bytes, want 16 KB (SPEC §6.3)", d.Tools.ResultCapBytes)
	}
}
