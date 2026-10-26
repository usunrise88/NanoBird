package fake

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/usunrise88/nanobird/internal/model"
)

// Reply is one scripted turn. Calls are the tool calls in the order the model
// made them. Finish defaults to "tool_calls" when there are calls and "stop"
// otherwise. A non-empty Error makes Complete fail with that message.
type Reply struct {
	Text      string `json:"text,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	Calls     []Call `json:"calls,omitempty"`
	Finish    string `json:"finish,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Call is one scripted tool call. Arguments is the JSON text the model writes.
type Call struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

var _ model.Client = (*Client)(nil)

// Client plays its script in order. It is not safe for concurrent use.
type Client struct {
	script []Reply
	// Requests holds every request the client received, in order.
	Requests []model.Request
}

// New returns a client that plays the replies in order.
func New(script ...Reply) *Client {
	return &Client{script: script}
}

// Remaining counts the scripted replies that have not been played.
func (c *Client) Remaining() int {
	return len(c.script)
}

// Capabilities reports what the OpenAI dialect offers: tools, and neither
// tokenize nor prefix handles.
func (c *Client) Capabilities(context.Context) (model.Caps, error) {
	return model.Caps{Tools: true}, nil
}

// Complete plays the next scripted reply. It fails when the script is used up,
// so a run that asks for more turns than a test scripted is caught.
func (c *Client) Complete(ctx context.Context, req model.Request) (model.Stream, error) {
	c.Requests = append(c.Requests, req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(c.script) == 0 {
		return nil, errors.New("fake: the script has no reply left")
	}
	r := c.script[0]
	c.script = c.script[1:]
	if r.Error != "" {
		return nil, errors.New(r.Error)
	}
	return &stream{events: events(r)}, nil
}

// Tokenize is not part of the OpenAI dialect (SPEC §7.2).
func (c *Client) Tokenize(context.Context, string) (int, error) {
	return 0, model.ErrUnsupported
}

// Prefix is not part of the OpenAI dialect (SPEC §7.2).
func (c *Client) Prefix(context.Context, []model.Message, []model.Tool) (model.PrefixHandle, error) {
	return "", model.ErrUnsupported
}

// Release is not part of the OpenAI dialect (SPEC §7.2).
func (c *Client) Release(context.Context, model.PrefixHandle) error {
	return model.ErrUnsupported
}

// events turns a scripted reply into the events a stream would deliver.
func events(r Reply) []model.Event {
	var out []model.Event
	if r.Reasoning != "" {
		out = append(out, model.Event{Reasoning: r.Reasoning})
	}
	if r.Text != "" {
		out = append(out, model.Event{Text: r.Text})
	}
	var calls []model.ToolCall
	for i, c := range r.Calls {
		id := c.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i+1)
		}
		out = append(out, model.Event{ToolCall: &model.ToolCallDelta{Index: i, ID: id, Name: c.Name, ArgumentsDelta: c.Arguments}})
		calls = append(calls, model.ToolCall{ID: id, Name: c.Name, Arguments: c.Arguments})
	}
	finish := r.Finish
	if finish == "" {
		finish = "stop"
		if len(calls) > 0 {
			finish = "tool_calls"
		}
	}
	return append(out, model.Event{Final: &model.Final{FinishReason: finish, ToolCalls: calls}})
}

type stream struct {
	events []model.Event
}

func (s *stream) Next() (model.Event, error) {
	if len(s.events) == 0 {
		return model.Event{}, io.EOF
	}
	ev := s.events[0]
	s.events = s.events[1:]
	return ev, nil
}

func (s *stream) Close() error {
	s.events = nil
	return nil
}
