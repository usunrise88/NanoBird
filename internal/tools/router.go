package tools

import (
	"strings"
	"unicode"
)

// Categories lists the M1 categories in the order the tools tool shows them.
// Core is always offered; the others load when the router or the model asks.
var Categories = []string{"core", "files", "code", "memory"}

// keywords are the English words that route a task to a category (SPEC §5.1,
// §5.13). Misrouting is recoverable: the model can enable a category itself.
var keywords = map[string][]string{
	"files": {"file", "files", "folder", "directory", "delete", "remove", "rename", "move", "copy", "append", "rollback", "restore"},
	"code": {"code", "function", "class", "method", "test", "tests", "bug", "fix", "refactor", "build",
		"compile", "search", "find", "grep", "implement", "program", "script", "module", "package",
		"repository", "repo", "run", "lint"},
	"memory": {"remember", "recall", "memory"},
}

// Route returns the routed categories a task needs, by its English keywords.
func Route(task string) []string {
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(task), func(r rune) bool { return !unicode.IsLetter(r) }) {
		words[w] = true
	}
	var out []string
	for _, c := range []string{"files", "code", "memory"} {
		for _, k := range keywords[c] {
			if words[k] {
				out = append(out, c)
				break
			}
		}
	}
	return out
}
