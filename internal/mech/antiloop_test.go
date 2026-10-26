package mech

import (
	"strings"
	"testing"
)

func TestSignatureIsCanonical(t *testing.T) {
	a := Signature("read_file", `{"path":"./a//b.txt","offset":1}`)
	b := Signature("read_file", `{"offset": 1, "path": "a/b.txt"}`)
	if a != b {
		t.Fatalf("signatures differ: %q vs %q", a, b)
	}
	if Signature("read_file", `{"path":"a.txt"}`) == a {
		t.Fatal("different arguments share a signature")
	}
}

func TestSixthIdenticalCallIsRefused(t *testing.T) {
	lt := NewLoopTracker()
	sig := Signature("list_dir", "{}")
	for i := 1; i <= 5; i++ {
		if ok, _ := lt.Before(sig); !ok {
			t.Fatalf("call %d was refused", i)
		}
		lt.Observe(true)
	}
	ok, hint := lt.Before(sig)
	if ok || !strings.Contains(hint, "6 times") {
		t.Fatalf("sixth call: ok=%v hint=%q", ok, hint)
	}
}

func TestIdleCallsEndAtTheLimitAndResetClears(t *testing.T) {
	lt := NewLoopTracker()
	for i := 0; i < IdleLimit; i++ {
		lt.Before(Signature("read_file", `{"path":"x`+string(rune('a'+i))+`"}`))
		lt.Observe(false)
	}
	if ok, _ := lt.Before(Signature("list_dir", "{}")); ok {
		t.Fatal("a call after 15 idle calls was allowed")
	}
	lt.Reset()
	if ok, _ := lt.Before(Signature("list_dir", "{}")); !ok {
		t.Fatal("Reset did not clear the idle count")
	}
}

func TestHintBecomesStrongerFromTheThirdRefusal(t *testing.T) {
	lt := NewLoopTracker()
	sig := Signature("list_dir", "{}")
	var last string
	for i := 0; i < SameCallLimit+3; i++ {
		_, last = lt.Before(sig)
	}
	if !strings.Contains(last, "different action") {
		t.Fatalf("the hint stayed weak: %q", last)
	}
}

func TestLadderResetsThenExits(t *testing.T) {
	var l Ladder
	for i := 1; i < LadderLimit; i++ {
		if a := l.Failed(); a != Continue {
			t.Fatalf("failure %d: action %d", i, a)
		}
	}
	if a := l.Failed(); a != Reset {
		t.Fatalf("failure %d: action %d, want Reset", LadderLimit, a)
	}
	for i := 1; i < LadderLimit; i++ {
		l.Failed()
	}
	if a := l.Failed(); a != Exit {
		t.Fatalf("second ladder: action %d, want Exit", a)
	}
}

func TestProgressClearsTheLadderCount(t *testing.T) {
	var l Ladder
	for i := 0; i < LadderLimit-1; i++ {
		l.Failed()
	}
	l.Progressed()
	if a := l.Failed(); a != Continue {
		t.Fatalf("after progress the next failure reached rung %d", a)
	}
}

func TestConvergenceNoticesAnUndo(t *testing.T) {
	c := NewConvergence()
	if c.Observe("a.txt", "one") != "" || c.Observe("a.txt", "two") != "" {
		t.Fatal("the first two writes drew a notice")
	}
	if c.Observe("a.txt", "one") == "" {
		t.Fatal("returning to the version of two writes ago drew no notice")
	}
	for i := 0; i < 3; i++ {
		if notice := c.Observe("b.txt", "same"); notice != "" {
			t.Fatalf("repeating the same content drew a notice on write %d", i+1)
		}
	}
}
