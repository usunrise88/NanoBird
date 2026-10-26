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
	// The default request carries the core tools; routed categories add to it per task.
	got := Static(longestShell, tools.NewRegistry(tools.Builtins()).Offered(nil))
	t.Logf("static prefill: %d bytes, budget %d", got, BudgetBytes)
	if got > BudgetBytes {
		t.Fatalf("static prefill is %d bytes, over the budget of %d (SPEC §5.1)", got, BudgetBytes)
	}
}

func TestSystemPromptIsEnglish(t *testing.T) {
	for _, text := range []string{systemPrompt, ChatSystem} {
		for _, r := range text {
			if unicode.IsLetter(r) && r > unicode.MaxASCII {
				t.Fatalf("prompt has the non-Latin letter %q (SPEC §5.13)", r)
			}
		}
	}
}
