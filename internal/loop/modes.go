package loop

import (
	"context"
	"strings"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/prefill"
	"github.com/usunrise88/nanobird/internal/tools"
)

// route enables the categories the router picks for the task (M1). With the
// prefill mechanism off, every routed category loads, the flat control arm.
func (l *Loop) route(task string) {
	if l.env.Enabled == nil {
		l.env.Enabled = map[string]bool{}
	}
	picked := tools.Route(task)
	if !l.on(mech.Prefill) {
		picked = []string{"files", "code", "memory"}
	}
	if picked == nil {
		picked = []string{}
	}
	for _, c := range picked {
		l.env.Enabled[c] = true
	}
	l.write("route", routeRecord{Categories: picked})
}

// chat answers a chat-mode prompt with one reply and no tools. The loop ends
// there (SPEC §4.1).
func (l *Loop) chat(ctx context.Context, task string) Result {
	msgs := []model.Message{
		{Role: model.RoleSystem, Content: prefill.ChatSystem},
		{Role: model.RoleUser, Content: task},
	}
	r, err := l.generateWith(ctx, 1, msgs, nil, l.cfg.Thinking, 0, true)
	if err != nil {
		if ctx.Err() != nil {
			return l.stop(Result{Stop: StopCancelled, Turns: 1, Err: err})
		}
		return l.stop(Result{Stop: StopError, Turns: 1, Err: err})
	}
	return l.stop(Result{Stop: StopEndTurn, Turns: 1, Summary: strings.TrimSpace(r.Text)})
}

// Mode is the class of a prompt (SPEC §4.1).
type Mode string

// The two modes. A chat prompt gets one reply and no tools; a task gets the
// routed toolset and the full loop.
const (
	ModeChat Mode = "chat"
	ModeTask Mode = "task"
)

// greetings are messages that need no tools by themselves.
var greetings = map[string]bool{
	"hi": true, "hello": true, "hey": true, "thanks": true, "thank": true,
	"thx": true, "ok": true, "okay": true, "bye": true, "you": true, "great": true,
}

// questionWords open a question that a chat reply can answer.
var questionWords = map[string]bool{
	"what": true, "why": true, "how": true, "who": true, "when": true, "where": true,
	"which": true, "is": true, "are": true, "does": true, "do": true, "can": true,
	"could": true, "explain": true, "describe": true,
}

// taskVerbs are the words of an imperative: a request for work.
var taskVerbs = map[string]bool{
	"create": true, "write": true, "make": true, "fix": true, "add": true, "change": true,
	"edit": true, "update": true, "delete": true, "remove": true, "rename": true, "run": true,
	"build": true, "test": true, "implement": true, "refactor": true, "install": true,
	"move": true, "copy": true, "generate": true, "find": true, "search": true, "read": true,
	"list": true, "show": true, "open": true, "append": true, "replace": true, "debug": true,
	"deploy": true, "commit": true, "push": true, "summarize": true, "check": true,
	"rewrite": true, "convert": true, "finish": true, "save": true,
}

// workspaceWords refer to the files of the project, which only tools can read.
var workspaceWords = map[string]bool{"project": true, "workspace": true, "repo": true, "repository": true}

// Classify sorts a prompt into chat or task (SPEC §4.1). The rules come first: a
// greeting, or a question that names no work and no file, is chat. Anything else
// is task, because on doubt task wins and a real task must not lose its tools.
func Classify(task string) Mode {
	words := strings.FieldsFunc(strings.ToLower(task), func(r rune) bool {
		return r < 'a' || r > 'z'
	})
	if len(words) == 0 {
		return ModeTask
	}
	allGreetings, work, reference := true, false, false
	for _, w := range words {
		if !greetings[w] {
			allGreetings = false
		}
		if taskVerbs[w] {
			work = true
		}
		if workspaceWords[w] || len(tools.Route(w)) > 0 {
			reference = true
		}
	}
	if allGreetings {
		return ModeChat
	}
	question := questionWords[words[0]] || strings.HasSuffix(strings.TrimSpace(task), "?")
	if question && !work && !reference {
		return ModeChat
	}
	return ModeTask
}
