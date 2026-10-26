package mech

import "testing"

func TestUndoingTheLastEditIsNoticed(t *testing.T) {
	c := NewConvergence()
	if hint := c.ObserveEdit("a.txt", Edit{Old: "beta", New: "delta"}); hint != "" {
		t.Fatalf("the first edit drew a notice: %q", hint)
	}
	if hint := c.ObserveEdit("a.txt", Edit{Old: "delta", New: "beta"}); hint == "" {
		t.Fatal("the undo drew no notice")
	}
}

func TestUndoingAnEarlierEditInTheSameFileIsNoticed(t *testing.T) {
	c := NewConvergence()
	c.ObserveEdit("a.txt", Edit{Old: "x", New: "y"})
	c.ObserveEdit("a.txt", Edit{Old: "p", New: "q"})
	if hint := c.ObserveEdit("a.txt", Edit{Old: "y", New: "x"}); hint == "" {
		t.Fatal("undoing an earlier edit drew no notice")
	}
}

func TestProgressAndOtherFilesAreNotNoticed(t *testing.T) {
	c := NewConvergence()
	c.ObserveEdit("a.txt", Edit{Old: "a", New: "b"})
	if hint := c.ObserveEdit("a.txt", Edit{Old: "b", New: "c"}); hint != "" {
		t.Fatalf("progress drew a notice: %q", hint)
	}
	if hint := c.ObserveEdit("b.txt", Edit{Old: "c", New: "b"}); hint != "" {
		t.Fatalf("an edit to another file drew a notice: %q", hint)
	}
}

func TestEditHistorySurvivesASave(t *testing.T) {
	c := NewConvergence()
	c.ObserveEdit("a.txt", Edit{Old: "beta", New: "delta"})
	restored := RestoreConvergence(c.State())
	if hint := restored.ObserveEdit("a.txt", Edit{Old: "delta", New: "beta"}); hint == "" {
		t.Fatal("the restored tracker forgot the last edit")
	}
}
