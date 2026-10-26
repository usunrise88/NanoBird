package mech

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/usunrise88/nanobird/internal/model"
)

// maxRescued bounds how many calls one reply may yield (SPEC §5.5).
const maxRescued = 4

// Rescue reads tool calls the model wrote as text (M5, SPEC §5.5). It reads three
// shapes, and only calls to tools that known reports as present:
//   - a JSON object with "name" and "arguments" (or "parameters"), in prose or in
//     a fenced block;
//   - a placeholder-quoted call, name(key:<|"|>value<|"|>) or name{...}, in prose,
//     where <|"|> is the tokenizer's quote token;
//   - a function expression, name(key="value"), when it is the whole reply.
//
// Anything else is not a call; there is no speculative parsing.
func Rescue(text string, known func(name string) bool) []model.ToolCall {
	var hits []rescued
	if name, args, ok := functionExpression(strings.TrimSpace(text)); ok && known(name) {
		hits = []rescued{{name: name, args: args}}
	} else {
		hits = append(jsonCalls(text, known), placeholderCalls(text, known)...)
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	}
	var calls []model.ToolCall
	for i, h := range hits {
		if i == maxRescued {
			break
		}
		calls = append(calls, model.ToolCall{ID: fmt.Sprintf("rescued_%d", i+1), Name: h.name, Arguments: h.args})
	}
	return calls
}

// rescued is one call found in a reply, and the byte where its text starts.
type rescued struct {
	at   int
	name string
	args string
}

// jsonCalls reads the JSON tool objects of a reply.
func jsonCalls(text string, known func(name string) bool) []rescued {
	var calls []rescued
	for i := 0; i < len(text); i++ {
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
		calls = append(calls, rescued{at: i, name: o.Name, args: string(args)})
		i += int(dec.InputOffset()) - 1
	}
	return calls
}

// placeholderForms are the two forms of a placeholder-quoted call, with one
// argument each: name(key:<|"|>value<|"|>) and name{key:<|"|>value<|"|>}.
var placeholderForms = []*regexp.Regexp{
	regexp.MustCompile(`(?s)\b([A-Za-z_][A-Za-z0-9_.]*)\(\s*([A-Za-z_]\w*)\s*:\s*<\|"\|>(.*?)<\|"\|>\s*\)`),
	regexp.MustCompile(`(?s)\b([A-Za-z_][A-Za-z0-9_.]*)\{\s*([A-Za-z_]\w*)\s*:\s*<\|"\|>(.*?)<\|"\|>\s*\}`),
}

// placeholderCalls reads the placeholder-quoted calls of a reply, in prose or not.
func placeholderCalls(text string, known func(name string) bool) []rescued {
	var calls []rescued
	for _, form := range placeholderForms {
		for _, m := range form.FindAllStringSubmatchIndex(text, -1) {
			name := text[m[2]:m[3]]
			if !known(name) {
				continue
			}
			args, err := json.Marshal(map[string]string{text[m[4]:m[5]]: strings.TrimSpace(text[m[6]:m[7]])})
			if err != nil {
				continue
			}
			calls = append(calls, rescued{at: m[0], name: name, args: string(args)})
		}
	}
	return calls
}

// The argument values of a function expression: a quoted string, a number,
// true, false or null.
const argValue = `"[^"]*"|-?\d+(?:\.\d+)?|true|false|null`

var (
	// callExpr matches a reply that is one call, name(...).
	callExpr = regexp.MustCompile(`(?s)^([A-Za-z_][A-Za-z0-9_.]*)\((.*)\)$`)
	// argPair matches one key=value pair of an argument list.
	argPair = regexp.MustCompile(`([A-Za-z_]\w*)\s*=\s*(` + argValue + `)`)
	// argList matches a whole argument list of pairs separated by commas.
	argList = regexp.MustCompile(`^\s*(?:` + keyValue + `(?:\s*,\s*` + keyValue + `)*)?\s*$`)
)

// keyValue is one key=value argument of a function expression.
const keyValue = `[A-Za-z_]\w*\s*=\s*(?:` + argValue + `)`

// functionExpression reads a reply that is one call written as name(key="value"),
// and nothing else. It returns the name and the arguments as a JSON object.
func functionExpression(text string) (string, string, bool) {
	m := callExpr.FindStringSubmatch(text)
	if m == nil || !argList.MatchString(m[2]) {
		return "", "", false
	}
	args := map[string]any{}
	for _, p := range argPair.FindAllStringSubmatch(m[2], -1) {
		args[p[1]] = literal(p[2])
	}
	b, err := json.Marshal(args)
	if err != nil {
		return "", "", false
	}
	return m[1], string(b), true
}

// literal is the JSON value of one argument of a function expression.
func literal(v string) any {
	switch {
	case v == "true":
		return true
	case v == "false":
		return false
	case v == "null":
		return nil
	case strings.HasPrefix(v, `"`):
		return v[1 : len(v)-1]
	default:
		return json.Number(v)
	}
}

// Attempted names the first of the given tools that the reply tries to call in a
// shape Rescue does not read (M5). It returns "" when there is none. A try is a
// name followed by a bracket and an argument, or a JSON "name" field that holds it.
func Attempted(text string, names []string) string {
	best, at := "", -1
	for _, name := range names {
		q := regexp.QuoteMeta(name)
		re := regexp.MustCompile(`\b` + q + `\s*[({]\s*(?:\w+\s*[:=]|["<])|"name"\s*:\s*"` + q + `"`)
		if loc := re.FindStringIndex(text); loc != nil && (at < 0 || loc[0] < at) {
			best, at = name, loc[0]
		}
	}
	return best
}

// Rejection is the answer to a call written as text that Rescue did not read.
// The call did not run. The answer gives the smallest valid arguments of the
// tool, and from the second refusal of one tool it says that the call repeats.
func Rejection(name, example string, count int) string {
	if count > 1 {
		return fmt.Sprintf("Your call to %s is still written as text, so it has not run, and this is the %d time. Make it through the tool interface, with the arguments exactly like this: %s.", name, count, example)
	}
	return fmt.Sprintf("Your call to %s was written as text, so it did not run. Make it through the tool interface, with the arguments as a JSON object. The smallest valid arguments are %s.", name, example)
}

// Malformed counts, for one task, the calls to each tool that were written as
// text and not read.
type Malformed struct {
	counts map[string]int
}

// NewMalformed returns a count with nothing in it.
func NewMalformed() *Malformed {
	return &Malformed{counts: map[string]int{}}
}

// Count records one more refused call to name and returns how many there are.
func (m *Malformed) Count(name string) int {
	m.counts[name]++
	return m.counts[name]
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
