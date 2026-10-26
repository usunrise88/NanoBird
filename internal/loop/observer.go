package loop

import (
	"context"

	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/tools"
)

// Observer hears what a run does as it happens, for a client that shows it (SPEC
// §8.3). A headless run has none. The loop calls it from the goroutine of the run.
type Observer interface {
	// Text receives a piece of the model's reply as it streams.
	Text(piece string)
	// ToolCall announces a call before the loop checks or runs it.
	ToolCall(call model.ToolCall)
	// ToolDone reports how a call ended. ok is false for a refused or failed call.
	// The output is whole; the model sees it capped (SPEC §6.3).
	ToolDone(call model.ToolCall, ok bool, output string)
	// Plan reports the plan after the todo tool changed it.
	Plan(items []tools.PlanItem)
	// Mode reports the decision for a prompt: chat or task (SPEC §4.1).
	Mode(mode string)
	// Permit asks whether a call that a safety ring stopped may run (SPEC §5.11,
	// §8.5). A run with no one to ask denies.
	Permit(ctx context.Context, p tools.Permission) tools.Answer
}

// nopObserver is the observer of a run that nobody watches.
type nopObserver struct{}

func (nopObserver) Text(string)                           {}
func (nopObserver) ToolCall(model.ToolCall)               {}
func (nopObserver) ToolDone(model.ToolCall, bool, string) {}
func (nopObserver) Plan([]tools.PlanItem)                 {}
func (nopObserver) Mode(string)                           {}
func (nopObserver) Permit(context.Context, tools.Permission) tools.Answer {
	return tools.Deny
}

// WithObserver sends what the run does to o, and asks o for the permissions that
// the safety rings need (SPEC §8.5).
func (l *Loop) WithObserver(o Observer) *Loop {
	l.observer = o
	l.env.Permit = o.Permit
	return l
}

// conversation is the message list that a prompt continues: the system prompt
// and the messages of the prompts before it, then the new task (SPEC §8.2).
func (l *Loop) conversation(task string) []model.Message {
	msgs := l.history
	if len(msgs) == 0 {
		msgs = []model.Message{{Role: model.RoleSystem, Content: l.cfg.System}}
	}
	return append(append([]model.Message(nil), msgs...), model.Message{Role: model.RoleUser, Content: task})
}
