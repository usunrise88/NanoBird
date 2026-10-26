package mech

import (
	"fmt"
	"strings"
)

// Ids of the mechanisms implemented so far, named as SPEC §5 names them.
const (
	// Resilience is M7: a failed model call is retried with backoff.
	Resilience = "resilience"
	// WriteGuard is N1: create_file refuses a path that exists.
	WriteGuard = "write_guard"
)

// known lists every id in SPEC §5, so a typo is told apart from an id that is
// not built yet.
var known = []string{
	"prefill", "layering", "finish_gate", "self_check", "verify_feedback",
	"plan_reinject", "anti_loop", "format_rescue", "edit_safety", "resilience",
	"boundary", "disclosure", "write_guard", "think_budget", "compaction",
	"tool_name_fix", "mcp_scope",
}

var implemented = map[string]bool{Resilience: true, WriteGuard: true}

// Switches records which mechanisms a run has switched off. The zero value
// leaves every mechanism on.
type Switches struct {
	off map[string]bool
}

// Parse reads a comma-separated list of ids to switch off. Unknown ids and ids
// that are not implemented yet are errors.
func Parse(list string) (Switches, error) {
	s := Switches{off: map[string]bool{}}
	for _, id := range strings.Split(list, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if err := check(id); err != nil {
			return Switches{}, err
		}
		s.off[id] = true
	}
	return s, nil
}

// On reports whether the mechanism runs.
func (s Switches) On(id string) bool {
	return !s.off[id]
}

func check(id string) error {
	for _, k := range known {
		if k == id {
			if !implemented[id] {
				return fmt.Errorf("mechanism %s is not implemented yet", id)
			}
			return nil
		}
	}
	return fmt.Errorf("unknown mechanism %q", id)
}
