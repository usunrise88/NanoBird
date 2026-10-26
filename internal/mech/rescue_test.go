package mech

import (
	"strings"
	"testing"
)

func knownTools(names ...string) func(string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(n string) bool { return set[n] }
}

func TestRescueReadsJSONInProse(t *testing.T) {
	calls := Rescue(`I will read it: {"name": "read_file", "arguments": {"path": "a.txt"}} and then decide.`, knownTools("read_file"))
	if len(calls) != 1 || calls[0].Name != "read_file" || calls[0].Arguments != `{"path": "a.txt"}` {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestRescueReadsFencedBlocks(t *testing.T) {
	text := "```json\n{\"name\":\"list_dir\",\"parameters\":{}}\n```"
	calls := Rescue(text, knownTools("list_dir"))
	if len(calls) != 1 || calls[0].Arguments != "{}" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestRescueAcceptsArgumentsAsAString(t *testing.T) {
	calls := Rescue(`{"name":"read_file","arguments":"{\"path\":\"b\"}"}`, knownTools("read_file"))
	if len(calls) != 1 || calls[0].Arguments != `{"path":"b"}` {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestRescueIgnoresUnknownNamesAndBrokenJSON(t *testing.T) {
	if calls := Rescue(`{"name":"frobnicate","arguments":{}}`, knownTools("read_file")); len(calls) != 0 {
		t.Fatalf("an unknown tool was rescued: %+v", calls)
	}
	if calls := Rescue(`{"name":"read_file","arguments":{"path":`, knownTools("read_file")); len(calls) != 0 {
		t.Fatalf("broken JSON was rescued: %+v", calls)
	}
}

func TestRescueKeepsEachCallApart(t *testing.T) {
	text := `{"name":"read_file","arguments":{"path":"a"}} then {"name":"read_file","arguments":{"path":"b"}}`
	calls := Rescue(text, knownTools("read_file"))
	if len(calls) != 2 || calls[0].ID != "rescued_1" || calls[1].ID != "rescued_2" || calls[1].Arguments != `{"path":"b"}` {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestRescueReadsPlaceholderQuotedCalls(t *testing.T) {
	calls := Rescue(`Reading now: read_file(path:<|"|>a.txt<|"|>) and then more.`, knownTools("read_file"))
	if len(calls) != 1 || calls[0].Name != "read_file" || calls[0].Arguments != `{"path":"a.txt"}` {
		t.Fatalf("calls = %+v", calls)
	}
	calls = Rescue(`call:list_dir{path:<|"|>src<|"|>}`, knownTools("list_dir"))
	if len(calls) != 1 || calls[0].Arguments != `{"path":"src"}` {
		t.Fatalf("braced calls = %+v", calls)
	}
}

func TestRescueReadsADoubleQuotedFunctionExpression(t *testing.T) {
	calls := Rescue(`search_files(pattern="TODO", max=5)`, knownTools("search_files"))
	if len(calls) != 1 || calls[0].Arguments != `{"max":5,"pattern":"TODO"}` {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestRescueRefusesWhatItDoesNotRead(t *testing.T) {
	known := knownTools("read_file")
	for _, text := range []string{
		`read_file(path=a.txt)`,
		`Next I read_file(path="a.txt") then.`,
		`read_file("a.txt")`,
		`read_file(path="a", extra={"x": 1})`,
	} {
		if calls := Rescue(text, known); len(calls) != 0 {
			t.Errorf("%q was rescued: %+v", text, calls)
		}
	}
}

func TestAttemptedNamesTheFirstTryAtAKnownTool(t *testing.T) {
	names := []string{"list_dir", "read_file"}
	if got := Attempted(`I will call read_file(path=a.txt) now`, names); got != "read_file" {
		t.Fatalf("Attempted = %q", got)
	}
	if got := Attempted(`{"name": "list_dir", "arguments": "path"}`, names); got != "list_dir" {
		t.Fatalf("Attempted for a JSON name = %q", got)
	}
	if got := Attempted("Plain prose about read_file and list_dir.", names); got != "" {
		t.Fatalf("prose named a call: %q", got)
	}
}

func TestRejectionShowsTheExampleAndSaysWhenItRepeats(t *testing.T) {
	example := `{"path": "<path>"}`
	if first := Rejection("read_file", example, 1); !strings.Contains(first, example) || strings.Contains(first, "still") {
		t.Fatalf("first refusal = %q", first)
	}
	if again := Rejection("read_file", example, 2); !strings.Contains(again, "still written as text") || !strings.Contains(again, example) {
		t.Fatalf("repeat refusal = %q", again)
	}
}
