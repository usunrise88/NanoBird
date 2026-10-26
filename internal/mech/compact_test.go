package mech

import (
	"strings"
	"testing"
)

func TestDigestQuotesTheFirstLine(t *testing.T) {
	d := Digest("read_file", "first line\nsecond line\n")
	if !strings.Contains(d, "first line: first line;") || !strings.Contains(d, "dropped") {
		t.Fatalf("digest = %q", d)
	}
	long := Digest("run_shell", strings.Repeat("é", 300))
	if !strings.Contains(long, strings.Repeat("é", digestLimit)+";") {
		t.Fatal("the quoted line is not cut at the limit in runes")
	}
}

func TestTokensOfIsBytesOverFour(t *testing.T) {
	if TokensOf(400) != 100 {
		t.Fatalf("TokensOf(400) = %d", TokensOf(400))
	}
}
