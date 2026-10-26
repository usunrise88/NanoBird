package main

import "testing"

func TestNonLatinFindsForeignLetters(t *testing.T) {
	cases := map[string]bool{
		"plain ASCII text":       false,
		"café and naïve, Zürich": false,
		"em dash — and ≥ and τ²": false,
		"Привет":                 true,
		"mixed 这是 text":          true,
	}
	for s, want := range cases {
		if _, got := nonLatin(s); got != want {
			t.Errorf("nonLatin(%q) = %v, want %v", s, got, want)
		}
	}
}
