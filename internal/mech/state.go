package mech

// The saved states of the loop-side mechanisms, so a checkpoint can restore
// them (SPEC §9.3). Each State method copies; each Restore function rebuilds.

// LoopState is the saved state of a LoopTracker.
type LoopState struct {
	Seen       map[string]int `json:"seen"`
	Idle       int            `json:"idle"`
	Rejections int            `json:"rejections"`
}

// State copies the tracker's counts.
func (a *LoopTracker) State() LoopState {
	seen := make(map[string]int, len(a.seen))
	for k, v := range a.seen {
		seen[k] = v
	}
	return LoopState{Seen: seen, Idle: a.idle, Rejections: a.rejections}
}

// RestoreLoopTracker rebuilds a tracker from its saved counts.
func RestoreLoopTracker(s LoopState) *LoopTracker {
	t := &LoopTracker{seen: make(map[string]int, len(s.Seen)), idle: s.Idle, rejections: s.Rejections}
	for k, v := range s.Seen {
		t.seen[k] = v
	}
	return t
}

// LadderState is the saved state of a Ladder.
type LadderState struct {
	Failed int `json:"failed"`
	Resets int `json:"resets"`
}

// State copies the ladder's counts.
func (l Ladder) State() LadderState {
	return LadderState{Failed: l.failed, Resets: l.resets}
}

// RestoreLadder rebuilds a ladder from its saved counts.
func RestoreLadder(s LadderState) Ladder {
	return Ladder{failed: s.Failed, resets: s.Resets}
}

// GateState is the saved state of a Gate.
type GateState struct {
	Rejects   map[string]int `json:"rejects"`
	SelfCheck bool           `json:"self_check"`
}

// State copies the gate's counts.
func (g *Gate) State() GateState {
	rejects := make(map[string]int, len(g.rejects))
	for k, v := range g.rejects {
		rejects[k] = v
	}
	return GateState{Rejects: rejects, SelfCheck: g.selfCheck}
}

// RestoreGate rebuilds a gate from its saved counts.
func RestoreGate(s GateState) *Gate {
	g := &Gate{rejects: make(map[string]int, len(s.Rejects)), selfCheck: s.SelfCheck}
	for k, v := range s.Rejects {
		g.rejects[k] = v
	}
	return g
}

// ConvState is the saved state of a Convergence.
type ConvState struct {
	Versions map[string][]string `json:"versions"`
}

// State copies the recent versions of each file.
func (c *Convergence) State() ConvState {
	versions := make(map[string][]string, len(c.versions))
	for k, v := range c.versions {
		versions[k] = append([]string(nil), v...)
	}
	return ConvState{Versions: versions}
}

// RestoreConvergence rebuilds a convergence tracker from its saved versions.
func RestoreConvergence(s ConvState) *Convergence {
	c := &Convergence{versions: make(map[string][]string, len(s.Versions))}
	for k, v := range s.Versions {
		c.versions[k] = append([]string(nil), v...)
	}
	return c
}

// MalformedState is the saved state of a Malformed count.
type MalformedState struct {
	Counts map[string]int `json:"counts"`
}

// State copies the counts.
func (m *Malformed) State() MalformedState {
	counts := make(map[string]int, len(m.counts))
	for k, v := range m.counts {
		counts[k] = v
	}
	return MalformedState{Counts: counts}
}

// RestoreMalformed rebuilds a count from its saved counts.
func RestoreMalformed(s MalformedState) *Malformed {
	m := NewMalformed()
	for k, v := range s.Counts {
		m.counts[k] = v
	}
	return m
}
