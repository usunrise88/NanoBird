package mech

import (
	"sort"
	"strings"
)

// Suggestion is a known tool that may be the one the model meant (SPEC §5.12).
type Suggestion struct {
	Name        string
	Description string
}

// Closest returns up to three known tools with the names nearest to name, each
// with the first sentence of its description (N4).
func Closest(name string, known []Suggestion) []Suggestion {
	type scored struct {
		s Suggestion
		d int
	}
	all := make([]scored, 0, len(known))
	for _, k := range known {
		all = append(all, scored{s: k, d: distance(strings.ToLower(name), strings.ToLower(k.Name))})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].d < all[j].d })
	var out []Suggestion
	for i := 0; i < len(all) && i < 3; i++ {
		s := all[i].s
		s.Description = firstSentence(s.Description)
		out = append(out, s)
	}
	return out
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// firstSentence cuts a description after its first sentence and keeps at most
// 120 runes.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	return s
}
