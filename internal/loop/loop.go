package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/session"
	"github.com/usunrise88/nanobird/internal/tools"
)

// maxRetries is how many times a failed model call is repeated (SPEC §5.7).
const maxRetries = 3

// Config is the part of the run settings the loop reads (SPEC §9.1, loop).
type Config struct {
	// System is the system prompt (SPEC §5.13, from the prefill package).
	System string
	// MaxTurns is the turn budget per prompt (SPEC §4.3).
	MaxTurns int
	// OutputCapTokens caps one reply (SPEC §6.3).
	OutputCapTokens int
	// Temperature is sent with every request (SPEC §4.6).
	Temperature float64
	// Thinking turns thinking on for tool turns. It is off by default (SPEC §4.5).
	Thinking bool
	// ResultCapBytes caps one tool result sent to the model (SPEC §6.3).
	ResultCapBytes int
	// RetryBackoff is the wait before the first retry. It doubles per retry (SPEC §5.7).
	RetryBackoff time.Duration
}

// StopReason says why a run ended, in the terms of SPEC §4.3.
type StopReason string

// The stop reasons of SPEC §4.3.
const (
	StopEndTurn   StopReason = "end_turn"
	StopMaxTurns  StopReason = "max_turn_requests"
	StopMaxTokens StopReason = "max_tokens"
	StopCancelled StopReason = "cancelled"
	StopError     StopReason = "error"
)

// Result is the outcome of one run.
type Result struct {
	Stop StopReason
	// Turns is the number of model turns the run used.
	Turns int
	// Summary is what the accepted finish said.
	Summary string
	// Err says why the run failed, when Stop is StopError.
	Err error
}

// Loop runs prompts against one model client, one tool registry and one
// transcript. It is not safe for concurrent use.
type Loop struct {
	client model.Client
	tools  *tools.Registry
	env    *tools.Env
	log    *session.Log
	cfg    Config
	// err is the first transcript write that failed. A run with holes in its
	// transcript cannot support a benchmark number, so it stops.
	err error
}

// New prepares a loop. The mechanism switches come from env.
func New(client model.Client, reg *tools.Registry, env *tools.Env, log *session.Log, cfg Config) *Loop {
	return &Loop{client: client, tools: reg, env: env, log: log, cfg: cfg}
}

// Run executes one prompt. It returns when a finish is accepted, when a stop
// condition of SPEC §4.3 is met, or when ctx is cancelled.
func (l *Loop) Run(ctx context.Context, task string) Result {
	msgs := []model.Message{
		{Role: model.RoleSystem, Content: l.cfg.System},
		{Role: model.RoleUser, Content: task},
	}
	l.write("task", taskRecord{Text: task})
	for turn := 1; turn <= l.cfg.MaxTurns; turn++ {
		if ctx.Err() != nil {
			return l.stop(Result{Stop: StopCancelled, Turns: turn - 1, Err: ctx.Err()})
		}
		r, err := l.generate(ctx, turn, msgs)
		if err != nil {
			if ctx.Err() != nil {
				return l.stop(Result{Stop: StopCancelled, Turns: turn, Err: err})
			}
			return l.stop(Result{Stop: StopError, Turns: turn, Err: err})
		}
		if len(r.Calls) == 0 {
			if r.Finish == "length" {
				return l.stop(Result{Stop: StopMaxTokens, Turns: turn})
			}
			msgs = l.nudge(msgs, turn, r)
			continue
		}
		msgs = append(msgs, model.Message{Role: model.RoleAssistant, Content: r.Text, ToolCalls: r.Calls})
		for _, call := range r.Calls {
			res := l.execute(ctx, turn, call)
			if call.Name == "finish" && res.ok {
				return l.stop(Result{Stop: StopEndTurn, Turns: turn, Summary: res.full})
			}
			msgs = append(msgs, model.Message{Role: model.RoleTool, ToolCallID: call.ID, Content: tools.Cap(res.full, l.cfg.ResultCapBytes)})
		}
		if l.err != nil {
			return l.stop(Result{Stop: StopError, Turns: turn, Err: l.err})
		}
	}
	return l.stop(Result{Stop: StopMaxTurns, Turns: l.cfg.MaxTurns})
}

// generate asks the model for one reply. With the resilience mechanism on (M7),
// a failed call is retried with doubling backoff.
func (l *Loop) generate(ctx context.Context, turn int, msgs []model.Message) (model.Reply, error) {
	thinking := l.cfg.Thinking
	req := model.Request{
		Messages:    msgs,
		Tools:       l.tools.Schemas(),
		Temperature: l.cfg.Temperature,
		MaxTokens:   l.cfg.OutputCapTokens,
		Thinking:    &thinking,
	}
	l.write("request", newRequestRecord(turn, req))
	for attempt := 0; ; attempt++ {
		r, err := l.once(ctx, req)
		if err == nil {
			for i := range r.Calls {
				if r.Calls[i].ID == "" {
					r.Calls[i].ID = fmt.Sprintf("call_%d_%d", turn, i+1)
				}
			}
			l.write("reply", newReplyRecord(turn, r))
			return r, nil
		}
		if ctx.Err() != nil {
			return model.Reply{}, err
		}
		if !l.env.Switches.On(mech.Resilience) {
			return model.Reply{}, err
		}
		if attempt == maxRetries {
			return model.Reply{}, fmt.Errorf("model call failed after %d attempts: %w", attempt+1, err)
		}
		l.write("mechanism", mechanismRecord{ID: mech.Resilience, Turn: turn, Event: "retry", Attempt: attempt + 1, Error: err.Error()})
		select {
		case <-ctx.Done():
			return model.Reply{}, ctx.Err()
		case <-time.After(l.cfg.RetryBackoff << attempt):
		}
	}
}

// once runs one streamed call and collects its reply.
func (l *Loop) once(ctx context.Context, req model.Request) (model.Reply, error) {
	s, err := l.client.Complete(ctx, req)
	if err != nil {
		return model.Reply{}, err
	}
	return model.Collect(s)
}

// nudge answers a reply without a tool call. The message goes to the model and
// is recorded as an intervention, so it can never be mistaken for the user's
// text (SPEC §5.13).
func (l *Loop) nudge(msgs []model.Message, turn int, r model.Reply) []model.Message {
	kind := "no_tool_call"
	text := "Your reply had no tool call, so the task is not finished. Use a tool, or call finish with a summary of what changed."
	if strings.TrimSpace(r.Text) == "" {
		kind = "empty_reply"
		text = "Your reply was empty. Use a tool, or call finish with a summary of what changed."
	}
	l.write("intervention", interventionRecord{Turn: turn, Kind: kind, Message: text})
	return append(msgs,
		model.Message{Role: model.RoleAssistant, Content: r.Text},
		model.Message{Role: model.RoleUser, Content: text},
	)
}

// outcome is what one call produced: the full output, and whether it succeeded.
type outcome struct {
	full string
	ok   bool
}

// execute runs one call the model made. The full output goes to the transcript;
// the caller caps what the model sees.
func (l *Loop) execute(ctx context.Context, turn int, call model.ToolCall) outcome {
	l.write("tool_call", toolCallRecord{Turn: turn, ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	var out string
	var err error
	tool, ok := l.tools.Lookup(call.Name)
	switch {
	case !ok:
		err = fmt.Errorf("unknown tool %q; the tools are: %s", call.Name, strings.Join(l.tools.Names(), ", "))
	case !json.Valid([]byte(arguments(call.Arguments))):
		err = errors.New("the arguments are not valid JSON")
	default:
		out, err = tool.Run(ctx, l.env, json.RawMessage(arguments(call.Arguments)))
	}
	res := outcome{full: out, ok: err == nil}
	if err != nil {
		res.full = "error: " + err.Error()
	}
	l.write("tool_result", toolResultRecord{Turn: turn, ID: call.ID, Name: call.Name, OK: res.ok, Output: res.full})
	return res
}

// arguments returns the argument text of a call, with an empty one read as an
// empty object.
func arguments(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

// stop records how the run ended and returns the result.
func (l *Loop) stop(r Result) Result {
	l.write("stop", stopRecord{Reason: string(r.Stop), Turns: r.Turns, Summary: r.Summary, Error: errorText(r.Err)})
	if l.err != nil {
		r.Stop = StopError
		r.Err = l.err
	}
	return r
}

// write records one event. After the first failure nothing more is written.
func (l *Loop) write(kind string, data any) {
	if l.err != nil {
		return
	}
	if err := l.log.Write(kind, data); err != nil {
		l.err = err
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
