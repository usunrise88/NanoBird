package loop

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/model/fake"
	"github.com/usunrise88/nanobird/internal/prefill"
	"github.com/usunrise88/nanobird/internal/safety"
	"github.com/usunrise88/nanobird/internal/session"
	"github.com/usunrise88/nanobird/internal/tools"
)

// fixedClock advances one second per call, so transcripts reproduce byte for byte.
func fixedClock() func() time.Time {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time {
		now = now.Add(time.Second)
		return now
	}
}

// harness is one run wired the way the CLI wires it, with a fake model.
type harness struct {
	env    *tools.Env
	client *fake.Client
	log    *session.Log
	loop   *Loop
}

// newHarness builds a run over the workspace in dir, with the fake model
// playing script.
func newHarness(t *testing.T, dir string, cfg Config, script ...fake.Reply) *harness {
	t.Helper()
	ws, err := safety.NewWorkspace(dir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	log, err := session.Create(ws.Root(), "test", fixedClock())
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })
	env := &tools.Env{
		Workspace:    ws,
		Shell:        tools.Shell{Name: "bash", Argv: []string{"bash", "-c"}},
		ShellTimeout: 30 * time.Second,
		Clock:        fixedClock(),
	}
	client := fake.New(script...)
	return &harness{
		env:    env,
		client: client,
		log:    log,
		loop:   New(client, tools.NewRegistry(tools.Builtins()), env, log, cfg),
	}
}

func defaultConfig() Config {
	return Config{
		System:          prefill.System("bash"),
		MaxTurns:        20,
		OutputCapTokens: 8192,
		ResultCapBytes:  16384,
	}
}

// finishWith is a reply that calls finish with the summary.
func finishWith(summary string) fake.Reply {
	args, _ := json.Marshal(map[string]string{"summary": summary})
	return fake.Reply{Calls: []fake.Call{{Name: "finish", Arguments: string(args)}}}
}

// listDir is a reply that lists the workspace: productive work the gate accepts.
func listDir() fake.Reply {
	return fake.Reply{Calls: []fake.Call{{Name: "list_dir", Arguments: "{}"}}}
}

// kinds lists the record kinds of a transcript, in order.
func kinds(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("transcript line is not JSON: %v", err)
		}
		out = append(out, rec.Kind)
	}
	return out
}

func count(list []string, kind string) int {
	n := 0
	for _, k := range list {
		if k == kind {
			n++
		}
	}
	return n
}

// transcript returns the text of a transcript.
func transcript(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEndsWhenFinishIsAccepted(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(), listDir(), finishWith("all done"), finishWith("all done"))
	res := h.loop.Run(context.Background(), "list the files, then finish")
	if res.Stop != StopEndTurn || res.Summary != "all done" || res.Turns != 3 || res.Unverified {
		t.Fatalf("result = %+v", res)
	}
}

func TestRetriesAFailedCallWithBackoff(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(),
		fake.Reply{Error: "engine busy"}, fake.Reply{Error: "engine busy"}, listDir(), finishWith("ok"), finishWith("ok"))
	res := h.loop.Run(context.Background(), "task")
	if res.Stop != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	if n := len(h.client.Requests); n != 5 {
		t.Fatalf("%d requests, want 5", n)
	}
	if n := count(kinds(t, h.log.Path()), "mechanism"); n != 2 {
		t.Fatalf("%d retry records, want 2", n)
	}
}

func TestGivesUpAfterThreeRetries(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(),
		fake.Reply{Error: "down"}, fake.Reply{Error: "down"}, fake.Reply{Error: "down"}, fake.Reply{Error: "down"})
	res := h.loop.Run(context.Background(), "task")
	if res.Stop != StopError || res.Err == nil || !strings.Contains(res.Err.Error(), "after 4 attempts") {
		t.Fatalf("result = %+v", res)
	}
	if n := len(h.client.Requests); n != 4 {
		t.Fatalf("%d requests, want 4", n)
	}
}

func TestResilienceSwitchOffFailsAtOnce(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(), fake.Reply{Error: "down"}, finishWith("ok"))
	off, err := mech.Parse(mech.Resilience)
	if err != nil {
		t.Fatal(err)
	}
	h.env.Switches = off
	if res := h.loop.Run(context.Background(), "task"); res.Stop != StopError {
		t.Fatalf("result = %+v", res)
	}
	if n := len(h.client.Requests); n != 1 {
		t.Fatalf("%d requests with resilience off, want 1", n)
	}
}

func TestStopsAtTheTurnBudget(t *testing.T) {
	cfg := defaultConfig()
	cfg.MaxTurns = 2
	read := fake.Reply{Calls: []fake.Call{{Name: "read_file", Arguments: `{"path":"missing.txt"}`}}}
	h := newHarness(t, t.TempDir(), cfg, read, read)
	res := h.loop.Run(context.Background(), "task")
	if res.Stop != StopMaxTurns || res.Turns != 2 {
		t.Fatalf("result = %+v", res)
	}
}

func TestTextOnlyReplyIsNudged(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(), fake.Reply{Text: "I will look at it."}, listDir(), finishWith("ok"), finishWith("ok"))
	if res := h.loop.Run(context.Background(), "task"); res.Stop != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	second := h.client.Requests[1].Messages
	last := second[len(second)-1]
	if last.Role != model.RoleUser || !strings.Contains(last.Content, "no tool call") {
		t.Fatalf("the nudge was not sent as a user message: %+v", last)
	}
	if n := count(kinds(t, h.log.Path()), "intervention"); n != 1 {
		t.Fatalf("%d intervention records, want 1", n)
	}
}

func TestLengthCutEndsTheRun(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(), fake.Reply{Text: "partial", Finish: "length"})
	if res := h.loop.Run(context.Background(), "task"); res.Stop != StopMaxTokens {
		t.Fatalf("result = %+v", res)
	}
}

func TestCancelledRunSendsNothing(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(), finishWith("ok"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := h.loop.Run(ctx, "task")
	if res.Stop != StopCancelled || len(h.client.Requests) != 0 {
		t.Fatalf("result = %+v, requests = %d", res, len(h.client.Requests))
	}
}

func TestUnknownToolIsAnAnswer(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(),
		fake.Reply{Calls: []fake.Call{{Name: "frobnicate", Arguments: "{}"}}}, listDir(), finishWith("ok"), finishWith("ok"))
	if res := h.loop.Run(context.Background(), "task"); res.Stop != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	msgs := h.client.Requests[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != model.RoleTool || !strings.Contains(last.Content, `unknown tool "frobnicate"`) || !strings.Contains(last.Content, "closest tools") {
		t.Fatalf("the unknown tool was not answered: %+v", last)
	}
}

func TestBrokenArgumentsAreAnAnswer(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(),
		fake.Reply{Calls: []fake.Call{{Name: "create_file", Arguments: `{"path": "a.txt", `}}},
		fake.Reply{Calls: []fake.Call{{Name: "create_file", Arguments: `{"path":"b.txt","content":"x"}`}}},
		finishWith("ok"), finishWith("ok"))
	if res := h.loop.Run(context.Background(), "task"); res.Stop != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	msgs := h.client.Requests[1].Messages
	if last := msgs[len(msgs)-1]; !strings.Contains(last.Content, "not valid JSON") {
		t.Fatalf("broken arguments were not reported: %q", last.Content)
	}
}

func TestTranscriptFailureStopsTheRun(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(), finishWith("ok"))
	if err := h.log.Close(); err != nil {
		t.Fatal(err)
	}
	res := h.loop.Run(context.Background(), "task")
	if res.Stop != StopError || res.Err == nil {
		t.Fatalf("a run with a broken transcript went on: %+v", res)
	}
}

func TestRepeatedCallIsRefusedFromTheSixth(t *testing.T) {
	script := []fake.Reply{}
	for i := 0; i < mech.SameCallLimit; i++ {
		script = append(script, listDir())
	}
	script = append(script, finishWith("ok"), finishWith("ok"))
	h := newHarness(t, t.TempDir(), defaultConfig(), script...)
	if res := h.loop.Run(context.Background(), "task"); res.Stop != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	if n := count(kinds(t, h.log.Path()), "mechanism"); n != 1 {
		t.Fatalf("%d refusals, want 1", n)
	}
}

func TestFinishGateNamesAMissingFileThenAccepts(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(),
		fake.Reply{Calls: []fake.Call{{Name: "create_file", Arguments: `{"path":"a.txt","content":"x"}`}}},
		finishWith("Wrote report.md."),
		fake.Reply{Calls: []fake.Call{{Name: "create_file", Arguments: `{"path":"report.md","content":"Done.\n"}`}}},
		finishWith("Wrote report.md."), finishWith("Wrote report.md."))
	res := h.loop.Run(context.Background(), "write the report")
	if res.Stop != StopEndTurn || res.Unverified {
		t.Fatalf("result = %+v", res)
	}
	text := transcript(t, h.log.Path())
	if !strings.Contains(text, "report.md") || !strings.Contains(text, `"check":"claims"`) {
		t.Fatalf("the missing file was not judged:\n%s", text)
	}
}

func TestFinishAfterThreeRejectionsIsUnverified(t *testing.T) {
	script := []fake.Reply{
		{Calls: []fake.Call{{Name: "create_file", Arguments: `{"path":"a.txt","content":"a"}`}}},
	}
	for i := 0; i < 4; i++ {
		script = append(script, finishWith("Updated missing.md."))
	}
	h := newHarness(t, t.TempDir(), defaultConfig(), script...)
	res := h.loop.Run(context.Background(), "update the files")
	if res.Stop != StopEndTurn || !res.Unverified {
		t.Fatalf("result = %+v", res)
	}
}

func TestFormatRescueRunsTheCall(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(),
		fake.Reply{Text: `I will look: {"name": "list_dir", "arguments": {}}`}, finishWith("ok"), finishWith("ok"))
	if res := h.loop.Run(context.Background(), "look at the workspace"); res.Stop != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	text := transcript(t, h.log.Path())
	if !strings.Contains(text, `"event":"rescued"`) || !strings.Contains(text, `"name":"list_dir"`) {
		t.Fatalf("the rescued call did not run:\n%s", text)
	}
}

func TestLadderResetsThenExitsWithABlocker(t *testing.T) {
	var script []fake.Reply
	for i := 0; i < 2*mech.LadderLimit; i++ {
		script = append(script, fake.Reply{})
	}
	h := newHarness(t, t.TempDir(), defaultConfig(), script...)
	res := h.loop.Run(context.Background(), "task")
	if res.Stop != StopEndTurn || res.Blocker == "" {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(transcript(t, h.log.Path()), `"event":"reset"`) {
		t.Fatal("the reset rung is missing from the transcript")
	}
}

func TestUndoingAnEditDrawsANotice(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(),
		fake.Reply{Calls: []fake.Call{{Name: "create_file", Arguments: `{"path":"a.txt","content":"one\n"}`}}},
		fake.Reply{Calls: []fake.Call{{Name: "edit_file", Arguments: `{"path":"a.txt","old":"one","new":"two"}`}}},
		fake.Reply{Calls: []fake.Call{{Name: "edit_file", Arguments: `{"path":"a.txt","old":"two","new":"one"}`}}},
		finishWith("ok"), finishWith("ok"))
	if res := h.loop.Run(context.Background(), "task"); res.Stop != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(transcript(t, h.log.Path()), `"event":"notice"`) {
		t.Fatal("the undo drew no notice")
	}
}
