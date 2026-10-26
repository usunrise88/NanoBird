package mech

import "testing"

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
