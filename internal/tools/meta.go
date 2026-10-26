package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/usunrise88/nanobird/internal/skills"
)

var toolsTool = Tool{
	Name:        "tools",
	Description: "Find, list or enable tools. find searches every tool for a word. list shows the categories and which are enabled. enable offers the tools of the named categories, or a named tool, from the next turn on.",
	Parameters: schema(`{
		"action": {"type": "string", "enum": ["find", "list", "enable"], "description": "What to do"},
		"query": {"type": "string", "description": "Word to search for, with find"},
		"names": {"type": "array", "items": {"type": "string"}, "description": "Categories or tool names, with enable"}
	}`, "action"),
	run: runTools,
}

func runTools(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Action string   `json:"action"`
		Query  string   `json:"query"`
		Names  []string `json:"names"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if env.Catalog == nil {
		return "", errors.New("no tool catalogue is loaded")
	}
	switch a.Action {
	case "find":
		q := strings.ToLower(strings.TrimSpace(a.Query))
		var b strings.Builder
		for _, t := range env.Catalog.all() {
			if q == "" || strings.Contains(strings.ToLower(t.Name+" "+t.Description), q) {
				fmt.Fprintf(&b, "%s (%s): %s\n", t.Name, t.Category, firstSentence(t.Description))
			}
		}
		if b.Len() == 0 {
			return fmt.Sprintf("no tool mentions %q", a.Query), nil
		}
		return b.String(), nil
	case "list":
		var b strings.Builder
		for _, c := range Categories {
			state := "available"
			if c == "core" || env.Enabled[c] {
				state = "enabled"
			}
			fmt.Fprintf(&b, "%s (%s): %s\n", c, state, strings.Join(env.Catalog.inCategory(c), ", "))
		}
		return b.String(), nil
	case "enable":
		if len(a.Names) == 0 {
			return "", errors.New("enable needs names: categories or tool names")
		}
		if env.Enabled == nil {
			env.Enabled = map[string]bool{}
		}
		var unknown []string
		for _, n := range a.Names {
			n = strings.TrimSpace(n)
			if _, ok := env.Catalog.Lookup(n); ok || slices.Contains(Categories, n) {
				env.Enabled[n] = true
				continue
			}
			unknown = append(unknown, n)
		}
		if len(unknown) > 0 {
			return "", fmt.Errorf("unknown categories or tools: %s; use find or list", strings.Join(unknown, ", "))
		}
		return "enabled " + strings.Join(a.Names, ", ") + "; they are offered from the next turn on", nil
	}
	return "", fmt.Errorf("unknown action %q; use find, list or enable", a.Action)
}

// firstSentence cuts a description after its first sentence.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

var skillsTool = Tool{
	Name:        "skills",
	Description: "List the skills of this workspace, or load one. load returns the instructions of the named skill.",
	Parameters: schema(`{
		"action": {"type": "string", "enum": ["list", "load"], "description": "What to do"},
		"name": {"type": "string", "description": "Skill name, with load"}
	}`, "action"),
	run: runSkills,
}

func runSkills(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	switch a.Action {
	case "list":
		if len(env.Skills) == 0 {
			return "(no skills)", nil
		}
		return skills.Index(env.Skills), nil
	case "load":
		for _, s := range env.Skills {
			if s.Name == a.Name {
				return skills.Body(s)
			}
		}
		return "", fmt.Errorf("no skill named %q; use skills list", a.Name)
	}
	return "", fmt.Errorf("unknown action %q; use list or load", a.Action)
}
