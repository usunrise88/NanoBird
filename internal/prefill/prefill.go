package prefill

import (
	"fmt"

	"github.com/usunrise88/nanobird/internal/model"
)

// BudgetBytes caps the static prefill: the system prompt plus every tool
// schema, in bytes, which does not depend on a tokenizer (SPEC §5.1). Until the
// category router (M1) decides which tools a task loads, the budget covers all
// built-in tools. Growth past it fails CI unless this constant is raised in the
// same commit, with the reason.
//
// Measured on 2026-10-26 with NanoLLM's tokenize endpoint, Gemma 4 E4B: this
// prefill is 1,240 tokens, against Mingbird's target of 797 (SPEC §5.1). The
// target is not met yet; docs/ROADMAP.md records the decision that is open.
const BudgetBytes = 4383

// systemPrompt is English throughout (SPEC §5.13). The one verb takes the
// shell name.
const systemPrompt = `You are NanoBird, a coding agent working in one workspace. Use the tools to read, change and check the files, and call finish when the task is done.

Work in small steps: read what you need, make one change, check it, then go on. Give paths relative to the workspace root. Nothing outside the workspace can be changed.

Rules:
- A reply without a tool call does not finish the task. Act with a tool, or call finish with a summary of what changed.
- Read a file before you edit it. edit_file needs the exact old text, including its indentation.
- create_file refuses an existing path. Change that file with edit_file.
- When a call fails, read the error and change the call. Do not repeat it unchanged.
- Write plans, todo items, summaries and new files in English.

Shell: %s
`

// System returns the system prompt with the shell named.
func System(shell string) string {
	return fmt.Sprintf(systemPrompt, shell)
}

// Static returns the size of the static prefill in bytes: the system prompt
// and the tool schemas, which every request carries unchanged.
func Static(shell string, tools []model.Tool) int {
	n := len(System(shell))
	for _, t := range tools {
		n += len(t.Name) + len(t.Description) + len(t.Parameters)
	}
	return n
}
