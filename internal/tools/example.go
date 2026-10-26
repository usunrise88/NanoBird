package tools

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Example returns the smallest valid arguments of a tool: its required fields in
// the order of the schema, each with a placeholder of its type. A refused call is
// shown this example (SPEC §5.5).
func Example(t Tool) string {
	var s struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(t.Parameters, &s); err != nil {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{")
	for i, field := range s.Required {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(jsonText(field))
		b.WriteString(": ")
		b.WriteString(jsonText(placeholder(field, s.Properties[field].Type)))
	}
	b.WriteString("}")
	return b.String()
}

// placeholder is an example value of a JSON type, named after its field.
func placeholder(field, typ string) any {
	switch typ {
	case "string":
		return "<" + field + ">"
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "array":
		return []any{}
	default:
		return map[string]any{}
	}
}

// jsonText is v as JSON, without the escaping of <, > and & that json.Marshal adds.
func jsonText(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "null"
	}
	return strings.TrimSpace(b.String())
}
