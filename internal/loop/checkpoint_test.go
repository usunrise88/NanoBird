package loop

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/model/fake"
	"github.com/usunrise88/nanobird/internal/safety"
	"github.com/usunrise88/nanobird/internal/session"
	"github.com/usunrise88/nanobird/internal/tools"
)

// resumedLoop opens the session "test" in dir for a resumed run.
func resumedLoop(t *testing.T, dir string, cfg Config, script ...fake.Reply) (*Loop, *fake.Client) {
	t.Helper()
	ws, err := safety.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	log, err := session.Open(ws.Root(), "test", fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	env := &tools.Env{
		Workspace:    ws,
		Shell:        tools.Shell{Name: "bash", Argv: []string{"bash", "-c"}},
		ShellTimeout: 30 * time.Second,
		Clock:        fixedClock(),
	}
	client := fake.New(script...)
	return New(client, tools.NewRegistry(tools.Builtins()), env, log, cfg), client
}

func TestResumeContinuesAfterTheLastCheckpoint(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.MaxTurns = 2
	h := newHarness(t, dir, cfg, listDir(), listDir())
	h.env.Plan.Items = []tools.PlanItem{{Text: "list the files", Done: true}}
	if res := h.loop.Run(context.Background(), "list the files, then finish"); res.Stop != StopMaxTurns {
		t.Fatalf("first run: %+v", res)
	}
	if err := h.log.Close(); err != nil {
		t.Fatal(err)
	}
	loop2, client2 := resumedLoop(t, dir, defaultConfig(), finishWith("ok"), finishWith("ok"))
	res := loop2.Resume(context.Background())
	if res.Stop != StopEndTurn || res.Turns != 3 {
		t.Fatalf("resumed run: %+v", res)
	}
	msgs := client2.Requests[0].Messages
	if len(msgs) != 4 || msgs[1].Content != "list the files, then finish" || msgs[3].Role != model.RoleTool {
		t.Fatalf("the resumed run did not start from the checkpoint: %+v", msgs)
	}
	if got := loop2.env.Plan.Items; len(got) != 1 || got[0].Text != "list the files" || !got[0].Done {
		t.Fatalf("the plan was not restored: %+v", got)
	}
	if n := count(kinds(t, h.log.Path()), "resume"); n != 1 {
		t.Fatalf("%d resume records, want 1", n)
	}
}

func TestResumeWithoutACheckpointIsAnError(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig())
	if err := h.log.Close(); err != nil {
		t.Fatal(err)
	}
	loop2, _ := resumedLoop(t, h.env.Workspace.Root(), defaultConfig())
	res := loop2.Resume(context.Background())
	if res.Stop != StopError || res.Err == nil || !strings.Contains(res.Err.Error(), "nothing to resume") {
		t.Fatalf("result = %+v", res)
	}
}
