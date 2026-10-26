package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/session"
	"github.com/usunrise88/nanobird/internal/tools"
)

// maxRetries is how many times a failed model call is repeated (SPEC §5.7).
const maxRetries = 3

// writes names the tools that change files (SPEC §6.1).
var writes = map[string]bool{
	"create_file": true, "edit_file": true, "append_file": true,
	"delete_file": true, "rollback": true,
}

// editing names the tools whose result the convergence notice reads (M6).
var editing = map[string]bool{"create_file": true, "edit_file": true, "append_file": true}

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
	// ContextTokens is the context budget of the model (SPEC §4.4). Zero means 32,768.
	ContextTokens int
	// CompactionAt is the share of the context at which compaction runs (SPEC §4.4).
	CompactionAt float64
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
	// Unverified is set when the finish was accepted after repeated rejections (SPEC §5.3).
	Unverified bool
	// Blocker names what stopped a run that the model could not finish (SPEC §4.3, rung 3).
	Blocker string
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
	env.Catalog = reg
	return &Loop{client: client, tools: reg, env: env, log: log, cfg: cfg}
}

// state is what one run keeps between its turns.
type state struct {
	task string
	msgs []model.Message
	// lastPrompt is the prompt size the engine reported for the last request, and
	// sentLen is how many messages that request carried (SPEC §4.4).
	lastPrompt    int
	sentLen       int
	anti          *mech.LoopTracker
	ladder        mech.Ladder
	gate          *mech.Gate
	conv          *mech.Convergence
	productive    int
	lastShell     string
	lastRejection string
}

// Run executes one prompt. It returns when a finish is accepted, when a stop
// condition of SPEC §4.3 is met, or when ctx is cancelled.
func (l *Loop) Run(ctx context.Context, task string) Result {
	st := &state{
		task: task,
		msgs: []model.Message{
			{Role: model.RoleSystem, Content: l.cfg.System},
			{Role: model.RoleUser, Content: task},
		},
		anti: mech.NewLoopTracker(),
		gate: mech.NewGate(),
		conv: mech.NewConvergence(),
	}
	l.write("task", taskRecord{Text: task})
	l.route(task)
	if l.on(mech.Layering) && Classify(task) == ModeChat {
		l.write("classify", classifyRecord{Mode: string(ModeChat)})
		return l.chat(ctx, task)
	}
	l.write("classify", classifyRecord{Mode: string(ModeTask)})
	for turn := 1; turn <= l.cfg.MaxTurns; turn++ {
		if ctx.Err() != nil {
			return l.stop(Result{Stop: StopCancelled, Turns: turn - 1, Err: ctx.Err()})
		}
		if err := l.compactIfNeeded(ctx, turn, st); err != nil {
			return l.stop(Result{Stop: StopError, Turns: turn, Err: err})
		}
		r, err := l.generate(ctx, turn, st.msgs, l.tools.Offered(l.env.Enabled))
		if err != nil {
			if ctx.Err() != nil {
				return l.stop(Result{Stop: StopCancelled, Turns: turn, Err: err})
			}
			return l.stop(Result{Stop: StopError, Turns: turn, Err: err})
		}
		st.lastPrompt, st.sentLen = r.Usage.PromptTokens, len(st.msgs)
		if len(r.Calls) == 0 && r.Finish == "length" {
			return l.stop(Result{Stop: StopMaxTokens, Turns: turn})
		}
		if len(r.Calls) == 0 {
			r.Calls = l.rescue(r.Text, turn)
		}
		if len(r.Calls) == 0 {
			st.msgs = append(st.msgs, model.Message{Role: model.RoleAssistant, Content: r.Text}, l.nudge(turn, r.Text))
			if res, done := l.failedTurn(st, turn); done {
				return res
			}
			continue
		}
		st.msgs = append(st.msgs, model.Message{Role: model.RoleAssistant, Content: r.Text, ToolCalls: r.Calls})
		executed := 0
		for _, call := range r.Calls {
			if call.Name == "finish" {
				if res, done := l.finish(ctx, st, turn, call); done {
					return res
				}
				continue
			}
			if hint, ok := l.guard(st, turn, call); !ok {
				st.msgs = append(st.msgs, toolMessage(call.ID, hint))
				continue
			}
			executed++
			out := l.observe(st, turn, call, l.execute(ctx, turn, call))
			st.msgs = append(st.msgs, toolMessage(call.ID, tools.Cap(out.full, l.cfg.ResultCapBytes)))
		}
		if l.err != nil {
			return l.stop(Result{Stop: StopError, Turns: turn, Err: l.err})
		}
		if executed == 0 {
			if res, done := l.failedTurn(st, turn); done {
				return res
			}
			continue
		}
		st.ladder.Progressed()
	}
	return l.stop(Result{Stop: StopMaxTurns, Turns: l.cfg.MaxTurns})
}

// reply is one model turn as the loop reads it.
type reply = model.Reply

// generate asks the model for one reply, offering the given tools. With the
// resilience mechanism on (M7), a failed call is retried with doubling backoff.
func (l *Loop) generate(ctx context.Context, turn int, msgs []model.Message, offered []model.Tool) (reply, error) {
	thinking := l.cfg.Thinking
	req := model.Request{
		Messages:    msgs,
		Tools:       offered,
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
			return reply{}, err
		}
		if !l.on(mech.Resilience) {
			return reply{}, err
		}
		if attempt == maxRetries {
			return reply{}, fmt.Errorf("model call failed after %d attempts: %w", attempt+1, err)
		}
		l.write("mechanism", mechanismRecord{ID: mech.Resilience, Turn: turn, Event: "retry", Attempt: attempt + 1, Error: err.Error()})
		select {
		case <-ctx.Done():
			return reply{}, ctx.Err()
		case <-time.After(l.cfg.RetryBackoff << attempt):
		}
	}
}

// once runs one streamed call and collects its reply.
func (l *Loop) once(ctx context.Context, req model.Request) (reply, error) {
	s, err := l.client.Complete(ctx, req)
	if err != nil {
		return reply{}, err
	}
	return model.Collect(s)
}

// rescue reads tool calls the model wrote as text (M5, SPEC §5.5).
func (l *Loop) rescue(text string, turn int) []model.ToolCall {
	if !l.on(mech.FormatRescue) {
		return nil
	}
	calls := mech.Rescue(text, func(name string) bool {
		_, ok := l.tools.Lookup(name)
		return ok
	})
	if len(calls) > 0 {
		l.write("mechanism", mechanismRecord{ID: mech.FormatRescue, Turn: turn, Event: "rescued", Count: len(calls)})
	}
	return calls
}

// nudge answers a reply without a tool call. The message is recorded as an
// intervention, so it can never be mistaken for the user's text (SPEC §5.13).
func (l *Loop) nudge(turn int, text string) model.Message {
	kind := "no_tool_call"
	msg := "Your reply had no tool call, so the task is not finished. Use a tool, or call finish with a summary of what changed."
	if strings.TrimSpace(text) == "" {
		kind = "empty_reply"
		msg = "Your reply was empty. Use a tool, or call finish with a summary of what changed."
	}
	l.write("intervention", interventionRecord{Turn: turn, Kind: kind, Message: msg})
	return model.Message{Role: model.RoleUser, Content: msg}
}

// failedTurn applies the escalation ladder to a turn that made no progress
// (SPEC §5.4). It returns true, with the result, when the run has to end.
func (l *Loop) failedTurn(st *state, turn int) (Result, bool) {
	if !l.on(mech.AntiLoop) {
		return Result{}, false
	}
	switch st.ladder.Failed() {
	case mech.Reset:
		l.write("mechanism", mechanismRecord{ID: mech.AntiLoop, Turn: turn, Event: "reset"})
		st.msgs = l.resetMessages(st)
		st.anti.Reset()
	case mech.Exit:
		blocker := "the model made no progress across two escalations; last refusal: " + st.lastRejection
		return l.stop(Result{Stop: StopEndTurn, Turns: turn, Blocker: blocker}), true
	}
	return Result{}, false
}

// resetMessages drops the failed turns. The system prompt and the task stay,
// and the plan is re-injected (rung 2, M3d).
func (l *Loop) resetMessages(st *state) []model.Message {
	text := "Earlier attempts at this task were dropped after repeated failures. The task is:\n" + st.task
	if len(l.env.Plan.Items) > 0 {
		text += "\n\nThe plan so far:\n" + l.env.Plan.Render()
	}
	return []model.Message{st.msgs[0], {Role: model.RoleUser, Content: text}}
}

// finish judges a finish call (SPEC §5.3). It returns the result when the finish
// is accepted. A rejection goes back to the model as the result of the call.
func (l *Loop) finish(ctx context.Context, st *state, turn int, call model.ToolCall) (Result, bool) {
	var args struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(arguments(call.Arguments)), &args); err != nil || strings.TrimSpace(args.Summary) == "" {
		st.msgs = append(st.msgs, toolMessage(call.ID, "error: finish needs a summary of what changed"))
		return Result{}, false
	}
	decision := mech.Decision{Accept: true}
	if l.on(mech.FinishGate) {
		decision = st.gate.Judge(mech.Claim{
			Task:       st.task,
			Summary:    args.Summary,
			Productive: st.productive,
			Open:       l.env.Plan.Open(),
			Exists:     l.hasContent,
			Tests:      func() (bool, bool, string) { return l.runTests(ctx) },
			Verify:     l.on(mech.VerifyFeedback),
			SelfCheck:  l.on(mech.SelfCheck),
		})
	}
	l.write("judgement", judgementRecord{Turn: turn, Check: decision.Check, Accepted: decision.Accept, Unverified: decision.Unverified, Message: decision.Message})
	if decision.Accept {
		return l.stop(Result{Stop: StopEndTurn, Turns: turn, Summary: args.Summary, Unverified: decision.Unverified}), true
	}
	st.lastRejection = decision.Message
	st.msgs = append(st.msgs, toolMessage(call.ID, decision.Message))
	return Result{}, false
}

// guard refuses a call before it runs when the anti-loop check says so (M4).
func (l *Loop) guard(st *state, turn int, call model.ToolCall) (string, bool) {
	if !l.on(mech.AntiLoop) {
		return "", true
	}
	ok, hint := st.anti.Before(mech.Signature(call.Name, arguments(call.Arguments)))
	if ok {
		return "", true
	}
	st.lastRejection = hint
	l.write("mechanism", mechanismRecord{ID: mech.AntiLoop, Turn: turn, Event: "refused", Error: hint})
	return hint, false
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
		err = l.unknownTool(call.Name)
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

// unknownTool answers a name the registry does not know. With tool_name_fix on,
// the closest known tools come back with their descriptions (N4, SPEC §5.12).
func (l *Loop) unknownTool(name string) error {
	if !l.on(mech.ToolNameFix) {
		return fmt.Errorf("unknown tool %q; the tools are: %s", name, strings.Join(l.tools.Names(), ", "))
	}
	var known []mech.Suggestion
	for _, s := range l.tools.Schemas() {
		known = append(known, mech.Suggestion{Name: s.Name, Description: s.Description})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "unknown tool %q. The closest tools are:", name)
	for _, s := range mech.Closest(name, known) {
		fmt.Fprintf(&b, "\n- %s: %s", s.Name, s.Description)
	}
	return errors.New(b.String())
}

// observe runs the after-call hooks: the progress that the anti-loop count reads
// (M4), the productive count of the finish gate, and the convergence notice on
// edits (M6).
func (l *Loop) observe(st *state, turn int, call model.ToolCall, out outcome) outcome {
	progress := false
	switch {
	case !out.ok:
		// A failed call changed nothing.
	case writes[call.Name]:
		progress = true
	case call.Name == "run_shell":
		progress = out.full != st.lastShell
		st.lastShell = out.full
	}
	if out.ok && call.Name != "todo" {
		st.productive++
	}
	if l.on(mech.AntiLoop) {
		st.anti.Observe(progress)
	}
	if out.ok && editing[call.Name] && l.on(mech.EditSafety) {
		if p := argPath(call.Arguments); p != "" {
			if content, err := l.readFile(p); err == nil {
				if hint := st.conv.Observe(p, content); hint != "" {
					out.full += "\n" + hint
					l.write("mechanism", mechanismRecord{ID: mech.EditSafety, Turn: turn, Event: "notice", Error: hint})
				}
			}
		}
	}
	return out
}

// hasContent reports whether rel names a non-empty file in the workspace (the
// claim check, SPEC §5.3).
func (l *Loop) hasContent(rel string) bool {
	abs, err := l.env.Workspace.Resolve(rel)
	if err != nil {
		return false
	}
	info, err := os.Stat(abs)
	return err == nil && !info.IsDir() && info.Size() > 0
}

// readFile returns the content of a file in the workspace.
func (l *Loop) readFile(rel string) (string, error) {
	abs, err := l.env.Workspace.Resolve(rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	return string(data), err
}

// runTests is the test guard (SPEC §5.3, check 4). ran is false when no runner
// applies or the runner cannot start, and the guard then steps aside.
func (l *Loop) runTests(ctx context.Context) (bool, bool, string) {
	runner, ok := tools.DetectRunner(l.env.Workspace.Root())
	if !ok {
		return false, true, ""
	}
	passed, out, err := tools.RunTests(ctx, l.env, runner)
	if err != nil {
		l.write("mechanism", mechanismRecord{ID: mech.FinishGate, Event: "tests_skipped", Error: err.Error()})
		return false, true, ""
	}
	return true, passed, out
}

// argPath reads the path argument of a call, or "" when there is none.
func argPath(args string) string {
	var a struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal([]byte(arguments(args)), &a)
	return a.Path
}

// toolMessage is the result of one call, as the model reads it.
func toolMessage(id, content string) model.Message {
	return model.Message{Role: model.RoleTool, ToolCallID: id, Content: content}
}

// on reports whether a mechanism runs in this run.
func (l *Loop) on(id string) bool {
	return l.env.Switches.On(id)
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
	l.write("stop", stopRecord{Reason: string(r.Stop), Turns: r.Turns, Summary: r.Summary, Unverified: r.Unverified, Blocker: r.Blocker, Error: errorText(r.Err)})
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
