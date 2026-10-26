package mech

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usunrise88/nanobird/internal/model"
)

// maxRescued bounds how many calls one reply may yield (SPEC §5.5).
const maxRescued = 4

// Rescue reads tool calls the model wrote as text (M5). It accepts one shape: a
// JSON object with the name of a known tool and an object "arguments" (or
// "parameters"), in prose or in a fenced block. Anything else is not a call;
// there is no speculative parsing.
func Rescue(text string, known func(name string) bool) []model.ToolCall {
	var calls []model.ToolCall
	for i := 0; i < len(text) && len(calls) < maxRescued; i++ {
		if text[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		var o struct {
			Name       string          `json:"name"`
			Arguments  json.RawMessage `json:"arguments"`
			Parameters json.RawMessage `json:"parameters"`
		}
		if err := dec.Decode(&o); err != nil {
			continue
		}
		args := argumentsOf(o.Arguments, o.Parameters)
		if args == nil || !known(o.Name) {
			continue
		}
		calls = append(calls, model.ToolCall{ID: fmt.Sprintf("rescued_%d", len(calls)+1), Name: o.Name, Arguments: string(args)})
		i += int(dec.InputOffset()) - 1
	}
	return calls
}

// argumentsOf returns the JSON object that a call's arguments hold, or nil.
// Arguments sent as a JSON string holding an object count too.
func argumentsOf(raws ...json.RawMessage) []byte {
	for _, raw := range raws {
		b := []byte(strings.TrimSpace(string(raw)))
		if len(b) == 0 {
			continue
		}
		if b[0] == '"' {
			var s string
			if json.Unmarshal(b, &s) != nil {
				continue
			}
			b = []byte(strings.TrimSpace(s))
		}
		if len(b) > 0 && b[0] == '{' && json.Valid(b) {
			return b
		}
	}
	return nil
}
