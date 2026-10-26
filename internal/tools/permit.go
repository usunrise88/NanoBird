package tools

import (
	"context"
	"strings"
)

// Answer is what the client said about a permission request (SPEC §8.5).
type Answer int

const (
	// Deny refuses the call. A cancelled prompt is a refusal too.
	Deny Answer = iota
	// AllowOnce runs this call.
	AllowOnce
	// AllowAlways runs this call, and the calls of its command family for the rest
	// of the session. Only a ring 2 ask offers it (SPEC §5.11).
	AllowAlways
)

// Permission is a call that a safety ring stopped, as the client shows it.
type Permission struct {
	// Tool is the tool of the call.
	Tool string
	// Command is the command text, when the call is a shell command.
	Command string
	// Reason says which ring asked, and why.
	Reason string
	// Family is the command family that allow_always covers. It is empty when
	// the ring does not offer allow_always.
	Family string
}

// permit reports whether a call with this permission may run. A family that the
// client allowed always runs without asking. Without a client to ask, the call is
// denied (SPEC §5.11).
func (e *Env) permit(ctx context.Context, p Permission) bool {
	if p.Family != "" && e.allowed[p.Family] {
		return true
	}
	if e.Permit == nil {
		return false
	}
	switch e.Permit(ctx, p) {
	case AllowOnce:
		return true
	case AllowAlways:
		if p.Family != "" {
			if e.allowed == nil {
				e.allowed = map[string]bool{}
			}
			e.allowed[p.Family] = true
		}
		return true
	}
	return false
}

// family is the command family of a shell command: its first word.
func family(command string) string {
	if fields := strings.Fields(command); len(fields) > 0 {
		return fields[0]
	}
	return ""
}
