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
		MaxTurns:        10,
		OutputCapTokens: 8192,
		ResultCapBytes:  16384,
	}
}

// finishWith is a reply that calls finish with the summary.
func finishWith(summary string) fake.Reply {
	args, _ := json.Marshal(map[string]string{"summary": summary})
	return fake.Reply{Calls: []fake.Call{{Name: "finish", Arguments: string(args)}}}
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

func TestEndsWhenFinishIsAccepted(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(), finishWith("all done"))
	res := h.loop.Run(context.Background(), "do nothing")
	if res.Stop != StopEndTurn || res.Summary != "all done" || res.Turns != 1 {
		t.Fatalf("result = %+v", res)
	}
}

func TestRetriesAFailedCallWithBackoff(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(),
		fake.Reply{Error: "engine busy"}, fake.Reply{Error: "engine busy"}, finishWith("ok"))
	res := h.loop.Run(context.Background(), "task")
	if res.Stop != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	if n := len(h.client.Requests); n != 3 {
		t.Fatalf("%d requests, want 3", n)
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
	h := newHarness(t, t.TempDir(), defaultConfig(), fake.Reply{Text: "I will look at it."}, finishWith("ok"))
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
		fake.Reply{Calls: []fake.Call{{Name: "frobnicate", Arguments: "{}"}}}, finishWith("ok"))
	if res := h.loop.Run(context.Background(), "task"); res.Stop != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	msgs := h.client.Requests[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != model.RoleTool || !strings.Contains(last.Content, `unknown tool "frobnicate"`) {
		t.Fatalf("the unknown tool was not answered: %+v", last)
	}
}

func TestBrokenArgumentsAreAnAnswer(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(),
		fake.Reply{Calls: []fake.Call{{Name: "create_file", Arguments: `{"path": "a.txt", `}}}, finishWith("ok"))
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
