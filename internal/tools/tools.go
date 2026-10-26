package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/safety"
)

// Env is what the tools act on: the workspace, the shell, the clock and the
// safety settings of the run.
type Env struct {
	Workspace *safety.Workspace
	Switches  mech.Switches
	Shell     Shell
	// ShellTimeout is the default limit of run_shell (SPEC §6.1).
	ShellTimeout time.Duration
	// Unsafe turns rings 1 to 3 off (NANOBIRD_UNSAFE). Ring 4 stays on.
	Unsafe bool
	// Unattended turns the rings' permission asks into denials (SPEC §5.11).
	Unattended bool
	Clock      func() time.Time
	// Plan is the todo list the finish gate reads (SPEC §5.3).
	Plan Plan
}

// Tool is one built-in tool: its name and schema as the model sees them, and
// the call that runs it.
type Tool struct {
	Name        string
	Description string
	// Parameters is the JSON Schema of the arguments.
	Parameters json.RawMessage
	run        func(ctx context.Context, env *Env, args json.RawMessage) (string, error)
}

// Run executes one call with the arguments the model wrote, as JSON. The
// returned text is the result. A returned error is shown to the model as a
// failed call.
func (t Tool) Run(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	return t.run(ctx, env, args)
}

// Builtins returns the built-in tools in the order the model sees them.
func Builtins() []Tool {
	return []Tool{
		readFileTool, listDirTool, createFileTool, editFileTool,
		appendFileTool, deleteFileTool, rollbackTool, runShellTool,
		todoTool, finishTool,
	}
}

// Registry holds the tools a run may offer.
type Registry struct {
	tools  []Tool
	byName map[string]Tool
}

// NewRegistry builds a registry. Tool names must be unique.
func NewRegistry(list []Tool) *Registry {
	r := &Registry{byName: make(map[string]Tool, len(list))}
	for _, t := range list {
		if _, dup := r.byName[t.Name]; dup {
			panic(fmt.Sprintf("tools: duplicate tool %q", t.Name))
		}
		r.tools = append(r.tools, t)
		r.byName[t.Name] = t
	}
	return r
}

// Lookup finds a tool by name.
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Names lists the tool names in registry order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for _, t := range r.tools {
		names = append(names, t.Name)
	}
	return names
}

// Schemas returns the tools the model is offered, in registry order.
func (r *Registry) Schemas() []model.Tool {
	out := make([]model.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, model.Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	return out
}

// Cap shortens a result to limit bytes for the model: the first three quarters
// and the last quarter, with a note of what was left out (SPEC §6.3). The full
// output stays in the transcript.
func Cap(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	head := limit * 3 / 4
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	start := len(s) - (limit - head)
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[:head] + fmt.Sprintf("\n[... %d bytes left out; the full output is in the transcript ...]\n", start-head) + s[start:]
}

// resolve maps a path the model wrote to the workspace (SPEC §5.8).
func (e *Env) resolve(p string) (string, error) {
	return e.Workspace.Resolve(p)
}

// decode reads the arguments of a call. No arguments at all count as an empty
// object.
func decode(args json.RawMessage, v any) error {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("arguments do not match the schema: %w", err)
	}
	return nil
}

// schema builds the JSON Schema of an object from its properties.
func schema(properties string, required ...string) json.RawMessage {
	s := struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
		Required   []string        `json:"required,omitempty"`
	}{"object", json.RawMessage(properties), required}
	b, err := json.Marshal(s)
	if err != nil {
		panic(fmt.Sprintf("tools: schema: %v", err))
	}
	return b
}
