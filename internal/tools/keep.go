package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// keptBytes is the most of a kept output that one tool_result reply carries (SPEC §6.3).
const keptBytes = 4000

// Keep stores the full output of a tool call, so that tool_result can return a part of
// it after the context has dropped the output (SPEC §4.4).
func (e *Env) Keep(id, full string) {
	if e.kept == nil {
		e.kept = map[string]string{}
	}
	e.kept[id] = full
}

var resultTool = Tool{
	Name:        "tool_result",
	Description: "Read a part of a tool output that the context dropped. Give the id from the dropped message; offset is a byte position, and the reply holds at most 4000 bytes.",
	Parameters: schema(`{
		"id": {"type": "string", "description": "the id from the dropped message"},
		"offset": {"type": "integer", "description": "byte position to start from, 0 by default"}
	}`, "id"),
	run: runResult,
}

func runResult(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		ID     string `json:"id"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	full, ok := env.kept[a.ID]
	if !ok {
		return "", fmt.Errorf("no output is kept for id %s: the id is unknown, or the session restarted", a.ID)
	}
	if a.Offset < 0 || a.Offset > len(full) {
		return "", fmt.Errorf("offset %d is outside the output of %d bytes", a.Offset, len(full))
	}
	start := a.Offset
	for start > 0 && start < len(full) && !utf8.RuneStart(full[start]) {
		start--
	}
	end := min(start+keptBytes, len(full))
	for end < len(full) && !utf8.RuneStart(full[end]) {
		end--
	}
	if end == len(full) {
		return full[start:end] + fmt.Sprintf("\n[bytes %d to %d of %d; the end of the output]", start, end, len(full)), nil
	}
	return full[start:end] + fmt.Sprintf("\n[bytes %d to %d of %d; read on with offset %d]", start, end, len(full), end), nil
}
