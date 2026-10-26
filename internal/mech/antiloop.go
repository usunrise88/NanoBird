package mech

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// Limits of the anti-loop mechanism (SPEC §5.4).
const (
	// SameCallLimit is how many times one signature may run in a task.
	SameCallLimit = 6
	// IdleLimit is how many calls in a row may pass without a change in the
	// workspace or new shell output.
	IdleLimit = 15
	// LadderLimit is how many failed turns in a row climb one rung of the ladder.
	LadderLimit = 6
)

// Signature is the identity of a call: its tool name and its arguments in
// canonical form (SPEC §5.4). Object keys come out sorted, whitespace inside
// strings is collapsed, and a "path" argument is cleaned.
func Signature(name, args string) string {
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return name + " " + strings.Join(strings.Fields(args), " ")
	}
	out, err := json.Marshal(canonical(v))
	if err != nil {
		return name + " " + args
	}
	return name + " " + string(out)
}

// canonical rewrites a decoded JSON value: strings lose their runs of
// whitespace, and a "path" string is cleaned.
func canonical(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if s, ok := x.(string); ok && k == "path" {
				t[k] = path.Clean(strings.ReplaceAll(s, `\`, "/"))
				continue
			}
			t[k] = canonical(x)
		}
		return t
	case []any:
		for i := range t {
			t[i] = canonical(t[i])
		}
		return t
	case string:
		return strings.Join(strings.Fields(t), " ")
	}
	return v
}

// LoopTracker counts repeated and idle calls for one task (M4). Refused calls do
// not run, so they cannot make progress; the ladder resets the counts.
type LoopTracker struct {
	seen       map[string]int
	idle       int
	rejections int
}

// NewLoopTracker returns a tracker for one task.
func NewLoopTracker() *LoopTracker {
	return &LoopTracker{seen: map[string]int{}}
}

// Before decides whether a call with signature sig may run. A refused call
// comes with the corrective message the model reads (rung 1 of SPEC §5.4).
func (a *LoopTracker) Before(sig string) (bool, string) {
	a.seen[sig]++
	if a.seen[sig] >= SameCallLimit {
		a.rejections++
		return false, a.hint(fmt.Sprintf("You made this same call %d times with the same arguments and nothing changed. Read the actual state first, or change your strategy.", a.seen[sig]))
	}
	if a.idle >= IdleLimit {
		a.rejections++
		return false, a.hint(fmt.Sprintf("The last %d calls changed nothing in the workspace. Check the state of the files, or change your strategy.", a.idle))
	}
	return true, ""
}

// Observe records whether a call that ran changed the workspace or produced
// new output.
func (a *LoopTracker) Observe(progress bool) {
	if progress {
		a.idle = 0
		return
	}
	a.idle++
}

// Reset clears the counts for a fresh start (rung 2). The refusal count stays,
// so a stronger hint survives the reset.
func (a *LoopTracker) Reset() {
	a.seen = map[string]int{}
	a.idle = 0
}

// hint makes the message stronger from the third refusal on.
func (a *LoopTracker) hint(base string) string {
	if a.rejections >= 3 {
		return base + " Stop repeating this approach and take a different action now."
	}
	return base
}
