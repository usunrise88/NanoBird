package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/usunrise88/nanobird/internal/model"
)

// stream reads one reply from an SSE body. The caller uses it from one
// goroutine; only the stall timer runs on its own.
type stream struct {
	body    io.ReadCloser
	r       *bufio.Reader
	timer   *time.Timer
	timeout time.Duration
	cancel  context.CancelFunc
	stalled atomic.Bool

	queue  []model.Event
	calls  map[int]*partialCall
	finish string
	usage  model.Usage
	done   bool
	err    error
}

// partialCall collects the fragments of one tool call.
type partialCall struct {
	id   string
	name string
	args strings.Builder
}

// Next returns the next event of the reply, io.EOF after the Final event, or
// the error that cut the reply short. The error is sticky.
func (s *stream) Next() (model.Event, error) {
	for len(s.queue) == 0 {
		if s.err != nil {
			return model.Event{}, s.err
		}
		if s.done {
			return model.Event{}, io.EOF
		}
		s.fill()
	}
	ev := s.queue[0]
	s.queue = s.queue[1:]
	return ev, nil
}

// Close ends the stream early and releases the connection.
func (s *stream) Close() error {
	s.shutdown()
	return nil
}

// fill reads one line of the body and queues what it carries.
func (s *stream) fill() {
	line, err := s.r.ReadString('\n')
	s.line(strings.TrimRight(line, "\r\n"))
	if err == nil || s.done || s.err != nil {
		return
	}
	switch {
	case errors.Is(err, io.EOF) && s.finish != "":
		s.finalize() // the engine closed the body without [DONE]
	case errors.Is(err, io.EOF):
		s.err = errors.New("model: stream ended before the final event")
	default:
		s.err = s.failure("read", err)
	}
}

// line handles one line of the SSE body. Only data counts as progress;
// heartbeats are comment lines that keep the connection open and nothing more.
func (s *stream) line(l string) {
	data, ok := strings.CutPrefix(l, "data:")
	if !ok {
		return
	}
	s.touch()
	data = strings.TrimPrefix(data, " ")
	if data == "[DONE]" {
		s.finalize()
		return
	}
	s.chunk(data)
}

// chunk turns one data payload into events.
func (s *stream) chunk(data string) {
	var c streamChunk
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		s.err = fmt.Errorf("model: decode stream chunk: %w", err)
		return
	}
	if c.Error != nil {
		s.err = fmt.Errorf("model: engine error: %s", c.Error.Message)
		return
	}
	if c.Usage != nil {
		s.usage = toUsage(*c.Usage)
	}
	for _, ch := range c.Choices {
		s.delta(ch.Delta)
		if ch.FinishReason != nil {
			s.finish = *ch.FinishReason
		}
	}
}

// delta queues the events of one delta: reasoning, then text, then the
// tool-call fragments in the order they came.
func (s *stream) delta(d streamDelta) {
	if d.ReasoningContent != "" {
		s.queue = append(s.queue, model.Event{Reasoning: d.ReasoningContent})
	}
	if d.Content != "" {
		s.queue = append(s.queue, model.Event{Text: d.Content})
	}
	for _, tc := range d.ToolCalls {
		pc, ok := s.calls[tc.Index]
		if !ok {
			pc = &partialCall{}
			s.calls[tc.Index] = pc
		}
		if tc.ID != "" {
			pc.id = tc.ID
		}
		if tc.Function.Name != "" {
			pc.name = tc.Function.Name
		}
		pc.args.WriteString(tc.Function.Arguments)
		s.queue = append(s.queue, model.Event{ToolCall: &model.ToolCallDelta{
			Index:          tc.Index,
			ID:             tc.ID,
			Name:           tc.Function.Name,
			ArgumentsDelta: tc.Function.Arguments,
		}})
	}
}

// finalize queues the Final event. Nothing more will arrive, so the connection
// is released now.
func (s *stream) finalize() {
	s.queue = append(s.queue, model.Event{Final: &model.Final{
		FinishReason: s.finish,
		ToolCalls:    s.toolCalls(),
		Usage:        s.usage,
	}})
	s.done = true
	s.shutdown()
}

// toolCalls returns the complete calls in index order, or nil when there are none.
func (s *stream) toolCalls() []model.ToolCall {
	if len(s.calls) == 0 {
		return nil
	}
	out := make([]model.ToolCall, 0, len(s.calls))
	for _, i := range slices.Sorted(maps.Keys(s.calls)) {
		pc := s.calls[i]
		out = append(out, model.ToolCall{ID: pc.id, Name: pc.name, Arguments: pc.args.String()})
	}
	return out
}

// touch restarts the stall timer. Only data restarts it (SPEC §7.5).
func (s *stream) touch() {
	s.timer.Reset(s.timeout)
}

// onStall runs when the engine has been silent for the whole stall timeout.
func (s *stream) onStall() {
	s.stalled.Store(true)
	s.cancel()
}

// failure names what ended a request or a read. A stall cancels the request,
// so a failure after a stall is reported as the stall.
func (s *stream) failure(op string, err error) error {
	if s.stalled.Load() {
		return fmt.Errorf("model: no output for %s; the turn is treated as stalled", s.timeout)
	}
	return fmt.Errorf("model: %s: %w", op, err)
}

// shutdown stops the timer, cancels the request and closes the body. It is
// safe to call more than once.
func (s *stream) shutdown() {
	s.timer.Stop()
	s.cancel()
	if s.body != nil {
		s.body.Close()
	}
}

// streamChunk is one data payload of a streamed reply.
type streamChunk struct {
	Choices []streamChoice `json:"choices"`
	Usage   *wireUsage     `json:"usage"`
	Error   *apiError      `json:"error"`
}

type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Content          string           `json:"content"`
	ReasoningContent string           `json:"reasoning_content"`
	ToolCalls        []streamToolCall `json:"tool_calls"`
}

type streamToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	Details          *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func toUsage(u wireUsage) model.Usage {
	out := model.Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens}
	if u.Details != nil {
		out.ReasoningTokens = u.Details.ReasoningTokens
	}
	return out
}

// apiError is the payload of the OpenAI error envelope.
type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}
