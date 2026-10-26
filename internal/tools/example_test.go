package tools

import "testing"

func TestExampleHoldsTheRequiredFieldsInSchemaOrder(t *testing.T) {
	got := Example(Tool{Parameters: schema(`{
		"path": {"type": "string"},
		"old": {"type": "string"},
		"new": {"type": "string"},
		"replace_all": {"type": "boolean"}
	}`, "path", "old", "new")})
	if want := `{"path": "<path>", "old": "<old>", "new": "<new>"}`; got != want {
		t.Fatalf("Example = %s, want %s", got, want)
	}
}

func TestExamplePlaceholdersFollowTheType(t *testing.T) {
	got := Example(Tool{Parameters: schema(`{"n": {"type": "integer"}, "on": {"type": "boolean"}, "list": {"type": "array"}}`, "n", "on", "list")})
	if want := `{"n": 0, "on": false, "list": []}`; got != want {
		t.Fatalf("Example = %s, want %s", got, want)
	}
}
