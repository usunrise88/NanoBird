package prefill

import (
	"testing"
	"unicode"

	"github.com/usunrise88/nanobird/internal/tools"
)

// longestShell is the longest shell name DetectShell can return, so the
// budget holds on every platform.
const longestShell = "Windows PowerShell 5.1"

func TestStaticPrefillWithinBudget(t *testing.T) {
	got := Static(longestShell, tools.NewRegistry(tools.Builtins()).Schemas())
	t.Logf("static prefill: %d bytes, budget %d", got, BudgetBytes)
	if got > BudgetBytes {
		t.Fatalf("static prefill is %d bytes, over the budget of %d (SPEC §5.1)", got, BudgetBytes)
	}
}

func TestSystemPromptIsEnglish(t *testing.T) {
	for _, r := range systemPrompt {
		if unicode.IsLetter(r) && r > unicode.MaxASCII {
			t.Fatalf("system prompt has the non-Latin letter %q (SPEC §5.13)", r)
		}
	}
}
