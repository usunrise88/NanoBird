package mech

// Action is the next rung of the escalation ladder (SPEC §5.4).
type Action int

const (
	// Continue keeps the run going with the corrective messages only.
	Continue Action = iota
	// Reset drops the failed turns and re-injects the task and the plan (rung 2).
	Reset
	// Exit ends the run with the blocker named (rung 3).
	Exit
)

// Ladder counts failed turns in a row and says which rung comes next.
type Ladder struct {
	failed int
	resets int
}

// Failed records a turn that made no progress. Every LadderLimit failed turns
// in a row climb one rung: a reset first, then an exit.
func (l *Ladder) Failed() Action {
	l.failed++
	if l.failed < LadderLimit {
		return Continue
	}
	l.failed = 0
	if l.resets == 0 {
		l.resets++
		return Reset
	}
	return Exit
}

// Progressed records a turn that made progress, which clears the count.
func (l *Ladder) Progressed() {
	l.failed = 0
}
