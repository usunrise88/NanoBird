package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/coder/acp-go-sdk"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("LRAB_FAKE_AGENT"); mode != "" {
		serveFakeAgent(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeAgent is an ACP agent for the runner's tests. In "block" mode its prompt waits for
// the cancel. In "finish" mode it reports two mechanism firings and ends the turn. In
// "resume" mode a new session blocks, as an agent does until it is killed, and a resumed
// session ends its turn.
type fakeAgent struct {
	sdk.Agent // only the methods below are used
	mode      string
	conn      *sdk.AgentSideConnection
	cancel    chan struct{}
	once      sync.Once
	resumed   atomic.Bool
}

func (f *fakeAgent) Initialize(context.Context, sdk.InitializeRequest) (sdk.InitializeResponse, error) {
	return sdk.InitializeResponse{ProtocolVersion: sdk.ProtocolVersionNumber}, nil
}

func (f *fakeAgent) NewSession(context.Context, sdk.NewSessionRequest) (sdk.NewSessionResponse, error) {
	return sdk.NewSessionResponse{SessionId: "fake-session"}, nil
}

func (f *fakeAgent) ResumeSession(context.Context, sdk.ResumeSessionRequest) (sdk.ResumeSessionResponse, error) {
	f.resumed.Store(true)
	return sdk.ResumeSessionResponse{}, nil
}

func (f *fakeAgent) Prompt(ctx context.Context, p sdk.PromptRequest) (sdk.PromptResponse, error) {
	if f.mode == "finish" || (f.mode == "resume" && f.resumed.Load()) {
		for i := 0; i < 2; i++ {
			update := sdk.SessionToolCallUpdate{
				ToolCallId: sdk.ToolCallId("call"),
				Meta:       map[string]any{"nanobird": map[string]any{"mechanism": "anti_loop", "rung": 1}},
			}
			note := sdk.SessionNotification{SessionId: p.SessionId, Update: sdk.SessionUpdate{ToolCallUpdate: &update}}
			if err := f.conn.SessionUpdate(ctx, note); err != nil {
				return sdk.PromptResponse{}, err
			}
		}
		return sdk.PromptResponse{StopReason: sdk.StopReasonEndTurn}, nil
	}
	select {
	case <-ctx.Done():
	case <-f.cancel:
	}
	return sdk.PromptResponse{StopReason: sdk.StopReasonCancelled}, nil
}

func (f *fakeAgent) Cancel(context.Context, sdk.CancelNotification) error {
	f.once.Do(func() { close(f.cancel) })
	return nil
}

// HandleExtensionMethod answers the stats that a cell asks for at its end.
func (f *fakeAgent) HandleExtensionMethod(context.Context, string, json.RawMessage) (any, error) {
	return map[string]any{"calls": 2}, nil
}

func serveFakeAgent(mode string) {
	f := &fakeAgent{mode: mode, cancel: make(chan struct{})}
	f.conn = sdk.NewAgentSideConnection(f, os.Stdout, os.Stdin)
	<-f.conn.Done()
}

// fixtureRoot is an LRAB directory with one file and one folder of fixtures.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "fixtures", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixtures", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixtures", "dir", "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFixturesAreCopiedByName(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work")
	if err := prepareWorkdir(fixtureRoot(t), []string{"fixtures/a.txt", "fixtures/dir"}, work); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a.txt", filepath.Join("dir", "x.txt")} {
		if _, err := os.Stat(filepath.Join(work, rel)); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
}

func TestTheDevSetHasItsEighteenTasksWithTheSpecBudgets(t *testing.T) {
	tasks, err := loadTasks("tasks", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 18 {
		t.Fatalf("%d tasks, want the 18 dev tasks", len(tasks))
	}
	for _, task := range tasks {
		want := 90 * time.Minute
		if strings.HasPrefix(task.ID, "LH-") {
			want = 180 * time.Minute
		}
		if got := task.budget(0); got != want {
			t.Fatalf("%s: budget %s, want %s", task.ID, got, want)
		}
		if strings.Contains(task.File, "heldout") {
			t.Fatalf("%s: a held-out task was loaded from %s", task.ID, task.File)
		}
	}
}

func TestATimeoutScoresZeroAndCancelsThePrompt(t *testing.T) {
	t.Setenv("LRAB_FAKE_AGENT", "block")
	cfg := cellConfig{agent: []string{os.Args[0]}, root: fixtureRoot(t), budget: 300 * time.Millisecond, out: t.TempDir()}
	task := Task{ID: "WF-99", Prompt: "wait", Fixtures: []string{"fixtures/a.txt"}}
	c, err := runCell(context.Background(), cfg, task, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !c.TimedOut || c.Score == nil || c.Score.Total != 0 || c.Stop != "cancelled" {
		t.Fatalf("cell = %+v, want a timeout that scores 0 after the cancel", c)
	}
}

func TestMechanismEventsAreCounted(t *testing.T) {
	t.Setenv("LRAB_FAKE_AGENT", "finish")
	cfg := cellConfig{agent: []string{os.Args[0]}, root: fixtureRoot(t), budget: 10 * time.Second, out: t.TempDir()}
	task := Task{ID: "WF-98", Prompt: "go", Fixtures: []string{"fixtures/a.txt"}}
	c, err := runCell(context.Background(), cfg, task, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.TimedOut || c.Stop != "end_turn" || c.Error != "" || c.Firings["anti_loop"] != 2 || c.Score != nil {
		t.Fatalf("cell = %+v, want end_turn with two anti_loop firings and no score (the scorer is off)", c)
	}
}

func TestAResumeTestKillsTheAgentAndResumesIt(t *testing.T) {
	t.Setenv("LRAB_FAKE_AGENT", "resume")
	cfg := cellConfig{agent: []string{os.Args[0]}, root: fixtureRoot(t), budget: 2 * time.Second, out: t.TempDir()}
	task := Task{ID: "LH-98", Prompt: "go", Fixtures: []string{"fixtures/a.txt"}, ResumeTest: true, KillAtPct: 50}
	c, err := runCell(context.Background(), cfg, task, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Resumed || c.KilledAt < 0.9 || c.KilledAt > 1.1 || c.Stop != "end_turn" || c.TimedOut || c.Error != "" {
		t.Fatalf("cell = %+v, want a kill at one second and a resumed end_turn", c)
	}
}

func TestARunThatEndsBeforeTheKillPointIsNotResumed(t *testing.T) {
	t.Setenv("LRAB_FAKE_AGENT", "finish")
	cfg := cellConfig{agent: []string{os.Args[0]}, root: fixtureRoot(t), budget: 10 * time.Second, out: t.TempDir()}
	task := Task{ID: "LH-97", Prompt: "go", Fixtures: []string{"fixtures/a.txt"}, ResumeTest: true}
	c, err := runCell(context.Background(), cfg, task, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Resumed || c.Stop != "end_turn" || c.KilledAt != 0 {
		t.Fatalf("cell = %+v, want one attempt that ends before the kill point", c)
	}
}

func TestSummaryAveragesEachTaskAndCountsTimeouts(t *testing.T) {
	cells := []cell{
		{Task: "WF-01", Score: &Score{Total: 0.5}},
		{Task: "WF-01", Score: &Score{Total: 1}},
		{Task: "WF-02", TimedOut: true, Score: &Score{}},
	}
	s := summarize(cells)
	if s.Cells != 3 || s.Timeouts != 1 || len(s.Tasks) != 2 || s.Tasks[0].Mean != 0.75 {
		t.Fatalf("summary = %+v", s)
	}
	if got := s.Mean; got < 0.49 || got > 0.51 {
		t.Fatalf("mean of the run = %v, want 0.5", got)
	}
}

func TestTheVendoredScorerRunsWithTheJudgeOff(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	tasks, err := loadTasks("tasks", []string{"WF-01"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("loadTasks: %v, %d tasks", err, len(tasks))
	}
	s, raw, err := score(context.Background(), python, filepath.Join("scoring", "score_task.py"), tasks[0], t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 0 || !strings.Contains(string(raw), `"task_id"`) {
		t.Fatalf("score = %+v, output %s", s, raw)
	}
}
