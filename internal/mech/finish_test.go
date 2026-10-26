package mech

import (
	"strings"
	"testing"
)

func productive() Claim {
	return Claim{Task: "make the report", Summary: "Done.", Productive: 1, Exists: func(string) bool { return true }}
}

func TestNoWorkIsRejectedWithAFirstStep(t *testing.T) {
	g := NewGate()
	d := g.Judge(Claim{Summary: "Done."})
	if d.Accept || d.Check != CheckNoWork || !strings.Contains(d.Message, "first concrete step") {
		t.Fatalf("decision = %+v", d)
	}
}

func TestOpenPlanItemsAreListed(t *testing.T) {
	c := productive()
	c.Open = []string{"write report", "check report"}
	d := NewGate().Judge(c)
	if d.Accept || d.Check != CheckPlan || !strings.Contains(d.Message, "write report; check report") {
		t.Fatalf("decision = %+v", d)
	}
}

func TestMissingFilesInTheSummaryAreNamed(t *testing.T) {
	c := productive()
	c.Summary = "Wrote report.md and notes.txt."
	c.Exists = func(p string) bool { return p == "notes.txt" }
	d := NewGate().Judge(c)
	if d.Accept || d.Check != CheckClaims || !strings.Contains(d.Message, "report.md") || strings.Contains(d.Message, "notes.txt") {
		t.Fatalf("decision = %+v", d)
	}
}

func TestFailingTestsReportTheOutputOnlyWithVerify(t *testing.T) {
	c := productive()
	c.Tests = func() (bool, bool, string) { return true, false, "a_test.go:12: want 1, got 2" }
	c.Verify = true
	if d := NewGate().Judge(c); d.Accept || d.Check != CheckTests || !strings.Contains(d.Message, "a_test.go:12") {
		t.Fatalf("verify on: %+v", d)
	}
	c.Verify = false
	if d := NewGate().Judge(c); d.Accept || strings.Contains(d.Message, "a_test.go") {
		t.Fatalf("verify off: %+v", d)
	}
}

func TestSelfCheckIsOnceAndRereadsTheTask(t *testing.T) {
	c := productive()
	c.SelfCheck = true
	g := NewGate()
	d := g.Judge(c)
	if d.Accept || d.Check != CheckSelfCheck || !strings.Contains(d.Message, "make the report") {
		t.Fatalf("first finish: %+v", d)
	}
	if d := g.Judge(c); !d.Accept {
		t.Fatalf("second finish: %+v", d)
	}
}

func TestThreeRejectionsThenUnverified(t *testing.T) {
	c := Claim{Summary: "Done.", Productive: 0}
	g := NewGate()
	for i := 0; i < rejectionLimit; i++ {
		if d := g.Judge(c); d.Accept {
			t.Fatalf("rejection %d was accepted", i+1)
		}
	}
	d := g.Judge(c)
	if !d.Accept || !d.Unverified || !strings.Contains(d.Message, "no work done yet") {
		t.Fatalf("fourth finish: %+v", d)
	}
}
