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
	// Reasoning receives a piece of the model's thinking as it streams (SPEC §8.3).
	Reasoning(piece string)
	// Usage reports the tokens of the last reply, and the size of the context they
	// count against (SPEC §8.3).
	Usage(used, size int)
	// Mechanism reports that a mechanism changed what the run does, with its rung
	// on the ladder (SPEC §8.8).
	Mechanism(id string, rung int)
	// ReadText reads a file through the client, when the client offers its file
	// system (SPEC §8.6). handled is false when the loop reads the file itself.
	ReadText(ctx context.Context, abs string) (text string, handled bool, err error)
	// RunTerminal runs a command in a terminal of the client, when the client offers
	// one (SPEC §8.6). handled is false when the loop runs the command itself.
	RunTerminal(ctx context.Context, command string, args []string, cwd string) (output string, handled bool, err error)
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
func (nopObserver) ReadText(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (nopObserver) RunTerminal(context.Context, string, []string, string) (string, bool, error) {
	return "", false, nil
}
func (nopObserver) Mechanism(string, int) {}
func (nopObserver) Reasoning(string)      {}
func (nopObserver) Usage(int, int)        {}
func (nopObserver) Permit(context.Context, tools.Permission) tools.Answer {
	return tools.Deny
}

// WithObserver sends what the run does to o, and asks o for the permissions that
// the safety rings need (SPEC §8.5).
func (l *Loop) WithObserver(o Observer) *Loop {
	l.observer = o
	l.env.Permit = o.Permit
	l.env.ReadText = o.ReadText
	l.env.RunTerminal = o.RunTerminal
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
