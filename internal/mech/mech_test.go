package mech

import "testing"

func TestParseSwitchesOff(t *testing.T) {
	s, err := Parse(" resilience, write_guard ,")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.On(Resilience) || s.On(WriteGuard) {
		t.Fatal("listed mechanisms are still on")
	}
	var zero Switches
	if !zero.On(Resilience) {
		t.Fatal("the zero value must leave mechanisms on")
	}
}

func TestParseRefusesUnknownAndUnbuiltIds(t *testing.T) {
	for _, list := range []string{"bogus", "finish_gate", "resilience,anti_loop"} {
		if _, err := Parse(list); err == nil {
			t.Errorf("Parse(%q) was accepted", list)
		}
	}
}
