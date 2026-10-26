package prefill

import (
	"fmt"

	"github.com/usunrise88/nanobird/internal/model"
)

// BudgetBytes caps the static prefill: the system prompt and the schemas of the
// core tools, in bytes, which does not depend on a tokenizer (SPEC §5.1). The
// default request carries only the core tools; the router adds routed categories
// per task. Growth past it fails CI unless this constant is raised in the same
// commit, with the reason. It went from 4,383 to 4,438 when the skills and tools
// meta tools joined the core set.
//
// Token counts are per model, not part of this pin (docs/ROADMAP.md). Measured on
// 2026-10-26 with NanoLLM's tokenize endpoint, Gemma 4 E4B: 1,258 tokens with the
// template and one user turn. Qwen3.5-4B, rendered through its own template: 1,575.
const BudgetBytes = 4438

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

// ChatSystem is the system prompt of a chat-mode reply (SPEC §4.1): English, at
// most four lines, and no tools.
const ChatSystem = `You are NanoBird, a coding assistant. Answer the message in English, briefly and plainly.
You have no tools for this message. If the user asks for a change in the workspace, say that a task is needed.`

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
