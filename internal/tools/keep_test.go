package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestToolResultReturnsTheNextPartOfAKeptOutput(t *testing.T) {
	env := &Env{}
	env.Keep("call_1", strings.Repeat("abc", 3000))
	out, err := runResult(context.Background(), env, json.RawMessage(`{"id":"call_1","offset":4000}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "[bytes 4000 to 8000 of 9000; read on with offset 8000]") {
		t.Fatalf("the part does not end with its position:\n%s", out)
	}
}

func TestToolResultRefusesAnUnknownId(t *testing.T) {
	_, err := runResult(context.Background(), &Env{}, json.RawMessage(`{"id":"call_9"}`))
	if err == nil || !strings.Contains(err.Error(), "no output is kept") {
		t.Fatalf("error = %v", err)
	}
}
