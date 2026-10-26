package mech

import (
	"regexp"
	"strings"
)

// Check names. A rejection is counted under the name of its check (SPEC §5.3).
const (
	CheckNoWork    = "no_work"
	CheckPlan      = "plan_sync"
	CheckClaims    = "claims"
	CheckTests     = "tests"
	CheckSelfCheck = "self_check"
)

// rejectionLimit is how many rejections of one check a run gets before the
// next finish is accepted as unverified (SPEC §5.3).
const rejectionLimit = 3

// pathClaim matches the file names a summary may name.
var pathClaim = regexp.MustCompile(`[\w./-]+\.(?:go|md|txt|json|ya?ml|py|js|ts|rs|toml|sh|html|css|mod|sum)\b`)

// Claim is what the gate knows when the model calls finish.
type Claim struct {
	// Task is the prompt. The self-check re-reads it (M3b).
	Task string
	// Summary is the summary the finish call carries.
	Summary string
	// Productive counts the calls that ran and did work since the prompt.
	Productive int
	// Open lists the plan items not done yet (plan sync).
	Open []string
	// Exists reports whether a path names a non-empty file in the workspace (claim check).
	Exists func(path string) bool
	// Tests runs the workspace's tests (test guard). ran is false when no runner applies.
	Tests func() (ran, passed bool, output string)
	// Verify puts the test output into the rejection (M3c).
	Verify bool
	// SelfCheck turns the self-check on (M3b).
	SelfCheck bool
}

// Decision is the gate's answer to one finish call.
type Decision struct {
	Accept bool
	// Unverified marks a finish accepted after repeated rejections. Message then
	// names the checks still open.
	Unverified bool
	// Check is the check that rejected the call.
	Check string
	// Message is what the model reads when the finish is rejected.
	Message string
}

// Gate judges the finish calls of one task. Its counts last for that task.
type Gate struct {
	rejects   map[string]int
	selfCheck bool
}

// NewGate returns a gate for one task.
func NewGate() *Gate {
	return &Gate{rejects: map[string]int{}}
}

// Judge runs the checks in the order of SPEC §5.3 and returns the first
// rejection. A check that has used up its rejections lets the finish through as
// unverified.
func (g *Gate) Judge(c Claim) Decision {
	if c.Productive == 0 {
		return g.reject(CheckNoWork, "No work has been done for this task yet. Start with the first concrete step: read the files the task names, then make the change.", "no work done yet")
	}
	if len(c.Open) > 0 {
		list := strings.Join(c.Open, "; ")
		return g.reject(CheckPlan, "The plan still has open items: "+list+". Finish them, mark them done with todo, then call finish again.", "open plan items: "+list)
	}
	if missing := missingPaths(c.Summary, c.Exists); len(missing) > 0 {
		list := strings.Join(missing, ", ")
		return g.reject(CheckClaims, "The summary names files that are missing or empty: "+list+". Create or fix them, or correct the summary, then call finish again.", "missing files: "+list)
	}
	if c.Tests != nil {
		if ran, passed, out := c.Tests(); ran && !passed {
			msg := "The test run failed. Fix the failing code, then call finish again."
			if c.Verify && out != "" {
				msg += "\n" + out
			}
			return g.reject(CheckTests, msg, "tests failing")
		}
	}
	if c.SelfCheck && !g.selfCheck {
		g.selfCheck = true
		return Decision{Check: CheckSelfCheck, Message: "Before you finish, check the task point by point against the workspace. The task is:\n" + c.Task + "\nAnswer each point with what you verified, then call finish again."}
	}
	return Decision{Accept: true}
}

// reject counts a rejection of check, or accepts the finish as unverified once
// the check has used up its rejections.
func (g *Gate) reject(check, msg, open string) Decision {
	if g.rejects[check] >= rejectionLimit {
		return Decision{Accept: true, Unverified: true, Check: check, Message: "accepted unverified after repeated rejections; still open: " + open}
	}
	g.rejects[check]++
	return Decision{Check: check, Message: msg}
}

// missingPaths lists the file names a summary names that the workspace does not
// hold as non-empty files.
func missingPaths(summary string, exists func(string) bool) []string {
	if exists == nil {
		return nil
	}
	seen := map[string]bool{}
	var missing []string
	for _, p := range pathClaim.FindAllString(summary, -1) {
		if seen[p] {
			continue
		}
		seen[p] = true
		if !exists(p) {
			missing = append(missing, p)
		}
	}
	return missing
}
