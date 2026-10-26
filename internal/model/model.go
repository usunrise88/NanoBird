package model

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrUnsupported means the backend does not offer the feature. Callers fall
// back as SPEC §7.2 describes.
var ErrUnsupported = errors.New("model: not supported by this backend")

// Role names the author of a message, with the names chat APIs use.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn of the conversation sent to the model.
type Message struct {
	Role    Role
	Content string
	// ToolCalls is set on an assistant turn that called tools.
	ToolCalls []ToolCall
	// ToolCallID is set on a tool turn: the call its result answers.
	ToolCallID string
}

// ToolCall is one call the model made. Arguments is the JSON text the model
// wrote, kept as it arrived.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Tool is a function the model may call. Parameters is a JSON Schema object.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Request is one model turn.
type Request struct {
	Messages []Message
	Tools    []Tool
	// Temperature is sent as given. Runs keep it at 0 unless configured
	// otherwise (SPEC §4.6).
	Temperature float64
	// MaxTokens caps the reply (SPEC §6.3). Zero leaves the cap to the engine.
	MaxTokens int
}

// Event is one piece of a streamed reply. Exactly one field is set.
type Event struct {
	Text      string
	Reasoning string
	ToolCall  *ToolCallDelta
	Final     *Final
}

// ToolCallDelta is one fragment of a tool call. Index tells the calls of one
// reply apart; ID and Name arrive once, the arguments in pieces.
type ToolCallDelta struct {
	Index          int
	ID             string
	Name           string
	ArgumentsDelta string
}

// Final ends a reply; no event follows it.
type Final struct {
	FinishReason string // "stop", "length", "tool_calls", ...
	ToolCalls    []ToolCall
	Usage        Usage
}

// Usage counts the tokens of one turn, as the engine reports them.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	ReasoningTokens  int
}

// Stream delivers one reply as events. Next returns io.EOF after the Final
// event. Close releases the connection and may be called at any time.
type Stream interface {
	Next() (Event, error)
	Close() error
}

// Caps lists the optional features of an endpoint. False means not offered.
type Caps struct {
	Tools    bool
	Tokenize bool
	Prefix   bool
}

// PrefixHandle names a prompt prefix the engine keeps in its cache (SPEC §7.2).
type PrefixHandle string

// Client is one model endpoint (SPEC §7.1).
type Client interface {
	Capabilities(ctx context.Context) (Caps, error)
	Complete(ctx context.Context, req Request) (Stream, error)
	Tokenize(ctx context.Context, text string) (int, error)
	Prefix(ctx context.Context, msgs []Message, tools []Tool) (PrefixHandle, error)
	Release(ctx context.Context, h PrefixHandle) error
}
