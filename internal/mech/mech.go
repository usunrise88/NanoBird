// Package mech holds the mechanism ids, their ablation switches (SPEC §2, rule 3)
// and the logic of the mechanisms that act on the loop. A mechanism runs unless
// NANOBIRD_ABLATION or the config's mechanisms.disable names it. Ids the code
// does not implement yet are refused, so a leave-one-out run never silently
// measures nothing.
package mech

import (
	"fmt"
	"strings"
)

// Ids of the mechanisms the code implements, named as SPEC §5 names them.
const (
	// AntiLoop is M4: repeated and idle calls are refused, with escalation.
	AntiLoop = "anti_loop"
	// Boundary is M8: a shell command that names a credential store needs permission.
	Boundary = "boundary"
	// EditSafety is M6: an edit that undoes the edit before it draws a hint to re-read the file.
	EditSafety = "edit_safety"
	// FinishGate is M3a: a finish is checked before it is accepted.
	FinishGate = "finish_gate"
	// FormatRescue is M5: a tool call written as text is read as a call when its shape is known.
	FormatRescue = "format_rescue"
	// Resilience is M7: a failed model call is retried with backoff.
	Resilience = "resilience"
	// SelfCheck is M3b: the task is re-read once before the first finish is accepted.
	SelfCheck = "self_check"
	// ToolNameFix is N4: an unknown tool name gets the closest known tools back.
	ToolNameFix = "tool_name_fix"
	// VerifyFeedback is M3c: a failing test run is reported with its output.
	VerifyFeedback = "verify_feedback"
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

var implemented = map[string]bool{
	AntiLoop: true, Boundary: true, EditSafety: true, FinishGate: true,
	FormatRescue: true, Resilience: true, SelfCheck: true, ToolNameFix: true,
	VerifyFeedback: true, WriteGuard: true,
}

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
