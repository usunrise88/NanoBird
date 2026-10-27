package mech

import (
	"fmt"
	"strings"
)

// digestLimit bounds the first line a digest quotes (SPEC §4.4).
const digestLimit = 100

// Digest is the one-line stand-in for a tool output that compaction drops: the
// tool, the first line of its output, and its size (SPEC §4.4, level 1).
func Digest(id, tool, output string) string {
	first, _, _ := strings.Cut(output, "\n")
	if r := []rune(first); len(r) > digestLimit {
		first = string(r[:digestLimit])
	}
	return fmt.Sprintf("[output of %s dropped to save context; id %s; first line: %s; %d bytes; tool_result returns it]", tool, id, first, len(output))
}

// TokensOf estimates the tokens of a number of bytes, at four bytes per token.
// The engine's own count replaces the estimate once it has reported one.
func TokensOf(bytes int) int {
	return bytes / 4
}
