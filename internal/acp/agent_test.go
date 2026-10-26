package acp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/coder/acp-go-sdk"
	"github.com/usunrise88/nanobird/internal/loop"
	"github.com/usunrise88/nanobird/internal/model/fake"
	"github.com/usunrise88/nanobird/internal/prefill"
	"github.com/usunrise88/nanobird/internal/safety"
	"github.com/usunrise88/nanobird/internal/session"
	"github.com/usunrise88/nanobird/internal/tools"
)

// fixture is a client connected to an agent over in-memory pipes.
type fixture struct {
	conn *sdk.ClientSideConnection
	rec  *recorder
	dir  string
}

// newFixture serves an agent opened with open, in a new workspace.
func newFixture(t *testing.T, open Open) *fixture {
	t.Helper()
	return newFixtureIn(t, t.TempDir(), open)
}

// newFixtureIn serves an agent opened with open, in the workspace dir, and
// initializes the connection. The workspace is registered before the agent, so
// it is removed after the agent has closed its sessions.
func newFixtureIn(t *testing.T, dir string, open Open) *fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	served := make(chan struct{})
	go func() {
		_ = Serve(ctx, NewAgent(open, "test"), agentIn, agentOut)
		close(served)
	}()
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = agentOut.Close()
		<-served
	})
	rec := &recorder{}
	conn := sdk.NewClientSideConnection(rec, clientOut, clientIn)
	if _, err := conn.Initialize(ctx, sdk.InitializeRequest{ProtocolVersion: sdk.ProtocolVersionNumber}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return &fixture{conn: conn, rec: rec, dir: dir}
}

// newSession opens a session in the fixture's workspace.
func (f *fixture) newSession(t *testing.T) sdk.SessionId {
	t.Helper()
	resp, err := f.conn.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: f.dir, McpServers: []sdk.McpServer{}})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return resp.SessionId
}

// prompt sends one text prompt and returns the stop reason.
func (f *fixture) prompt(t *testing.T, id sdk.SessionId, text string) sdk.StopReason {
	t.Helper()
	resp, err := f.conn.Prompt(context.Background(), sdk.PromptRequest{SessionId: id, Prompt: []sdk.ContentBlock{sdk.TextBlock(text)}})
	if err != nil {
		t.Fatalf("Prompt(%q): %v", text, err)
	}
	return resp.StopReason
}

// recorder is the client side of the tests. It keeps every session update, and it
// refuses the client methods that the agent does not ask for.
type recorder struct {
	updates []sdk.SessionUpdate
}

func (r *recorder) SessionUpdate(_ context.Context, n sdk.SessionNotification) error {
	r.updates = append(r.updates, n.Update)
	return nil
}

func (r *recorder) RequestPermission(context.Context, sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeCancelled()}, nil
}

func (r *recorder) ReadTextFile(context.Context, sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error) {
	return sdk.ReadTextFileResponse{}, sdk.NewMethodNotFound("fs/read_text_file")
}

func (r *recorder) WriteTextFile(context.Context, sdk.WriteTextFileRequest) (sdk.WriteTextFileResponse, error) {
	return sdk.WriteTextFileResponse{}, sdk.NewMethodNotFound("fs/write_text_file")
}

func (r *recorder) CreateTerminal(context.Context, sdk.CreateTerminalRequest) (sdk.CreateTerminalResponse, error) {
	return sdk.CreateTerminalResponse{}, sdk.NewMethodNotFound("terminal/create")
}

func (r *recorder) KillTerminal(context.Context, sdk.KillTerminalRequest) (sdk.KillTerminalResponse, error) {
	return sdk.KillTerminalResponse{}, sdk.NewMethodNotFound("terminal/kill")
}

func (r *recorder) TerminalOutput(context.Context, sdk.TerminalOutputRequest) (sdk.TerminalOutputResponse, error) {
	return sdk.TerminalOutputResponse{}, sdk.NewMethodNotFound("terminal/output")
}

func (r *recorder) ReleaseTerminal(context.Context, sdk.ReleaseTerminalRequest) (sdk.ReleaseTerminalResponse, error) {
	return sdk.ReleaseTerminalResponse{}, sdk.NewMethodNotFound("terminal/release")
}

func (r *recorder) WaitForTerminalExit(context.Context, sdk.WaitForTerminalExitRequest) (sdk.WaitForTerminalExitResponse, error) {
	return sdk.WaitForTerminalExitResponse{}, sdk.NewMethodNotFound("terminal/wait_for_exit")
}

// seen names the kind of each update the client got, in order.
func (r *recorder) seen() []string {
	var out []string
	for _, u := range r.updates {
		switch {
		case u.UserMessageChunk != nil:
			out = append(out, "user")
		case u.AgentMessageChunk != nil:
			out = append(out, "text")
		case u.ToolCall != nil:
			out = append(out, "tool")
		case u.ToolCallUpdate != nil:
			out = append(out, "tool_update")
		case u.Plan != nil:
			out = append(out, "plan")
		default:
			out = append(out, "other")
		}
	}
	return out
}

// loopOpen opens sessions on the real loop, over one scripted model client. The
// test keeps the client, so it can read the requests that the loop sent.
func loopOpen(client *fake.Client) Open {
	return func(workspace, id string, obs loop.Observer) (Runner, error) {
		ws, err := safety.NewWorkspace(workspace)
		if err != nil {
			return nil, err
		}
		now := time.Now
		var log *session.Log
		if id != "" {
			log, err = session.Open(ws.Root(), id, now)
		} else {
			id = session.NewID(now())
			log, err = session.Create(ws.Root(), id, now)
		}
		if err != nil {
			return nil, err
		}
		shell := tools.DetectShell()
		env := &tools.Env{Workspace: ws, Shell: shell, ShellTimeout: 30 * time.Second, Clock: now}
		cfg := loop.Config{System: prefill.System(shell.Name), MaxTurns: 10, OutputCapTokens: 8192, ResultCapBytes: 16384}
		lp := loop.New(client, tools.NewRegistry(tools.Builtins()), env, log, cfg).WithObserver(obs)
		if id != "" {
			if err := lp.Restore(); err != nil {
				_ = log.Close()
				return nil, err
			}
		}
		return &loopRunner{id: id, lp: lp, log: log}, nil
	}
}

// loopRunner is a Runner over the real loop.
type loopRunner struct {
	id  string
	lp  *loop.Loop
	log *session.Log
}

func (r *loopRunner) ID() string { return r.id }

func (r *loopRunner) Prompt(ctx context.Context, task string) loop.Result { return r.lp.Run(ctx, task) }

func (r *loopRunner) Close() { _ = r.log.Close() }

// stubRunner is a Runner whose prompt is the function it was given.
type stubRunner struct {
	run func(ctx context.Context, task string) loop.Result
}

func (s stubRunner) ID() string { return "stub-1" }

func (s stubRunner) Prompt(ctx context.Context, task string) loop.Result { return s.run(ctx, task) }

func (s stubRunner) Close() {}

// finishWith is a reply that calls finish with the summary.
func finishWith(summary string) fake.Reply {
	args, _ := json.Marshal(map[string]string{"summary": summary})
	return fake.Reply{Calls: []fake.Call{{ID: "finish-" + summary, Name: "finish", Arguments: string(args)}}}
}

// listDir is a reply that calls list_dir on the workspace root.
func listDir(id string) fake.Reply {
	return fake.Reply{Calls: []fake.Call{{ID: id, Name: "list_dir", Arguments: "{}"}}}
}

func TestPromptStreamsTheReplyAndTheCalls(t *testing.T) {
	client := fake.New(
		fake.Reply{Text: "Let me look.", Calls: []fake.Call{{ID: "call_1", Name: "list_dir", Arguments: "{}"}}},
		finishWith("Listed the workspace."),
		finishWith("Listed the workspace."),
	)
	f := newFixture(t, loopOpen(client))
	id := f.newSession(t)
	if stop := f.prompt(t, id, "Look around."); stop != sdk.StopReasonEndTurn {
		t.Fatalf("stop reason = %s", stop)
	}
	if got, want := f.rec.seen(), []string{"text", "tool", "tool_update", "text"}; !equal(got, want) {
		t.Fatalf("updates = %v, want %v", got, want)
	}
	start := f.rec.updates[1].ToolCall
	if start == nil || start.Title != "list_dir" || start.Kind != sdk.ToolKindRead || start.Status != sdk.ToolCallStatusInProgress {
		t.Fatalf("tool call = %+v", start)
	}
	done := f.rec.updates[2].ToolCallUpdate
	if done == nil || done.Status == nil || *done.Status != sdk.ToolCallStatusCompleted {
		t.Fatalf("tool call update = %+v", done)
	}
}

func TestEditShowsItsDiff(t *testing.T) {
	client := fake.New(
		fake.Reply{Calls: []fake.Call{{ID: "edit_1", Name: "edit_file", Arguments: `{"path": "notes.txt", "old": "beta", "new": "delta"}`}}},
		finishWith("Changed beta to delta."),
		finishWith("Changed beta to delta."),
	)
	f := newFixture(t, loopOpen(client))
	if err := os.WriteFile(filepath.Join(f.dir, "notes.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := f.newSession(t)
	f.prompt(t, id, "Change beta.")
	var diff *sdk.ToolCallContent
	for _, u := range f.rec.updates {
		if u.ToolCallUpdate != nil && len(u.ToolCallUpdate.Content) > 0 && u.ToolCallUpdate.Content[0].Diff != nil {
			diff = &u.ToolCallUpdate.Content[0]
		}
	}
	if diff == nil {
		t.Fatalf("no diff in %v", f.rec.seen())
	}
	if want := filepath.Join(f.dir, "notes.txt"); diff.Diff.Path != want || diff.Diff.NewText != "delta" {
		t.Fatalf("diff = %+v, want path %s", diff.Diff, want)
	}
}

func TestSecondPromptContinuesTheConversation(t *testing.T) {
	client := fake.New(
		listDir("call_1"), finishWith("first"), finishWith("first"),
		listDir("call_2"), finishWith("second"), finishWith("second"),
	)
	f := newFixture(t, loopOpen(client))
	id := f.newSession(t)
	f.prompt(t, id, "first task")
	first := len(client.Requests)
	f.prompt(t, id, "second task")
	if first+1 > len(client.Requests) {
		t.Fatalf("the second prompt sent no request")
	}
	msgs := client.Requests[first].Messages
	if len(msgs) < 3 || msgs[1].Content != "first task" || msgs[len(msgs)-1].Content != "second task" {
		t.Fatalf("the second prompt did not continue the first: %+v", msgs)
	}
}

func TestCancelAnswersCancelled(t *testing.T) {
	started := make(chan struct{})
	open := func(string, string, loop.Observer) (Runner, error) {
		return stubRunner{run: func(ctx context.Context, _ string) loop.Result {
			close(started)
			<-ctx.Done()
			return loop.Result{Stop: loop.StopCancelled}
		}}, nil
	}
	f := newFixture(t, open)
	id := f.newSession(t)
	ctx := context.Background()
	answer := make(chan string, 1)
	go func() {
		resp, err := f.conn.Prompt(ctx, sdk.PromptRequest{SessionId: id, Prompt: []sdk.ContentBlock{sdk.TextBlock("work")}})
		if err != nil {
			answer <- "error: " + err.Error()
			return
		}
		answer <- string(resp.StopReason)
	}()
	<-started
	if err := f.conn.Cancel(ctx, sdk.CancelNotification{SessionId: id}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := <-answer; got != string(sdk.StopReasonCancelled) {
		t.Fatalf("stop reason = %s, want cancelled", got)
	}
}

func TestNewSessionNeedsAnAbsolutePath(t *testing.T) {
	f := newFixture(t, loopOpen(fake.New()))
	if _, err := f.conn.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "relative", McpServers: []sdk.McpServer{}}); err == nil {
		t.Fatal("a relative cwd opened a session")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoadReplaysTheHistoryAndTheNextPromptContinuesIt(t *testing.T) {
	client := fake.New(
		listDir("call_1"), finishWith("first"), finishWith("first"),
		listDir("call_2"), finishWith("second"), finishWith("second"),
	)
	first := newFixture(t, loopOpen(client))
	id := first.newSession(t)
	first.prompt(t, id, "first task")
	second := newFixtureIn(t, first.dir, loopOpen(client))
	if _, err := second.conn.LoadSession(context.Background(), sdk.LoadSessionRequest{SessionId: id, Cwd: first.dir, McpServers: []sdk.McpServer{}}); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got, want := second.rec.seen(), []string{"user", "tool", "tool_update", "text"}; !equal(got, want) {
		t.Fatalf("replay = %v, want %v", got, want)
	}
	if text := second.rec.updates[0].UserMessageChunk; text == nil {
		t.Fatalf("the first replayed update is not the task")
	}
	second.prompt(t, id, "second task")
	last := client.Requests[len(client.Requests)-1].Messages
	if len(last) < 3 || last[1].Content != "first task" {
		t.Fatalf("the loaded session did not continue its conversation: %+v", last)
	}
}

func TestResumeContinuesWithoutReplay(t *testing.T) {
	client := fake.New(
		listDir("call_1"), finishWith("first"), finishWith("first"),
		listDir("call_2"), finishWith("second"), finishWith("second"),
	)
	first := newFixture(t, loopOpen(client))
	id := first.newSession(t)
	first.prompt(t, id, "first task")
	second := newFixtureIn(t, first.dir, loopOpen(client))
	if _, err := second.conn.ResumeSession(context.Background(), sdk.ResumeSessionRequest{SessionId: id, Cwd: first.dir, McpServers: []sdk.McpServer{}}); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if n := len(second.rec.updates); n != 0 {
		t.Fatalf("resume replayed %d updates", n)
	}
	second.prompt(t, id, "second task")
	if last := client.Requests[len(client.Requests)-1].Messages; len(last) < 3 || last[1].Content != "first task" {
		t.Fatalf("the resumed session did not continue its conversation: %+v", last)
	}
}

func TestListSessionsShowsTheFirstTaskOfEach(t *testing.T) {
	client := fake.New(listDir("call_1"), finishWith("done"), finishWith("done"))
	f := newFixture(t, loopOpen(client))
	id := f.newSession(t)
	f.prompt(t, id, "first task")
	resp, err := f.conn.ListSessions(context.Background(), sdk.ListSessionsRequest{Cwd: &f.dir})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(resp.Sessions) != 1 || resp.Sessions[0].SessionId != id || resp.Sessions[0].Title == nil || *resp.Sessions[0].Title != "first task" {
		t.Fatalf("sessions = %+v", resp.Sessions)
	}
}

func TestCloseEndsTheSession(t *testing.T) {
	f := newFixture(t, loopOpen(fake.New()))
	id := f.newSession(t)
	if _, err := f.conn.CloseSession(context.Background(), sdk.CloseSessionRequest{SessionId: id}); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if _, err := f.conn.Prompt(context.Background(), sdk.PromptRequest{SessionId: id, Prompt: []sdk.ContentBlock{sdk.TextBlock("late")}}); err == nil {
		t.Fatal("a closed session took a prompt")
	}
}
