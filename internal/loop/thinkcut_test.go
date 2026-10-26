package loop

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/model/fake"
	"github.com/usunrise88/nanobird/internal/tools"
)

// recorder keeps the requests that a scripted model received.
type recorder struct {
	*fake.Client
	mu   sync.Mutex
	reqs []model.Request
}

func (r *recorder) Complete(ctx context.Context, req model.Request) (model.Stream, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return r.Client.Complete(ctx, req)
}

// withRecorder runs the task with a model that records its requests.
func withRecorder(h *harness, cfg Config) *recorder {
	rec := &recorder{Client: h.client}
	h.loop = New(rec, tools.NewRegistry(tools.Builtins()), h.env, h.log, cfg)
	return rec
}

// hasCutNote reports whether a request carries the note of a cut thought.
func hasCutNote(req model.Request) bool {
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "reached its budget and was cut") {
			return true
		}
	}
	return false
}

func TestAThoughtPastItsBudgetIsCutAndTheTurnIsRetriedWithoutThinking(t *testing.T) {
	cfg := defaultConfig()
	cfg.ThinkingPlan = true
	cfg.ThinkingBudget = 8 // 32 characters at four to a token
	h := newHarness(t, t.TempDir(), cfg,
		fake.Reply{Reasoning: strings.Repeat("reasoning ", 20), Text: "never shown"},
		listDir(), finishWith("Done."), finishWith("Done."))
	rec := withRecorder(h, cfg)
	if res := h.loop.Run(context.Background(), "Think, then finish."); res.Err != nil {
		t.Fatalf("the run failed: %v", res.Err)
	}
	if len(rec.reqs) < 2 {
		t.Fatalf("%d requests, want the cut turn and its retry", len(rec.reqs))
	}
	first, retry := rec.reqs[0], rec.reqs[1]
	if first.Thinking == nil || !*first.Thinking || first.ReasoningBudget != 8 || hasCutNote(first) {
		t.Fatalf("first request thinking %v budget %d, want thinking with the budget and no note", first.Thinking, first.ReasoningBudget)
	}
	if retry.Thinking == nil || *retry.Thinking || retry.ReasoningBudget != 0 || !hasCutNote(retry) {
		t.Fatalf("retry thinking %v budget %d note %v, want no thinking and the cut thought as context",
			retry.Thinking, retry.ReasoningBudget, hasCutNote(retry))
	}
	if !strings.Contains(rec.reqs[1].Messages[len(rec.reqs[1].Messages)-1].Content, "reasoning reasoning") {
		t.Fatal("the note does not carry the thought so far")
	}
}

func TestAThoughtWithinItsBudgetIsNotCut(t *testing.T) {
	cfg := defaultConfig()
	cfg.ThinkingPlan = true
	cfg.ThinkingBudget = 8
	h := newHarness(t, t.TempDir(), cfg,
		fake.Reply{Reasoning: "short plan", Calls: []fake.Call{{Name: "list_dir", Arguments: "{}"}}},
		finishWith("Done."), finishWith("Done."))
	rec := withRecorder(h, cfg)
	if res := h.loop.Run(context.Background(), "Look, then finish."); res.Err != nil {
		t.Fatalf("the run failed: %v", res.Err)
	}
	for i, req := range rec.reqs {
		if hasCutNote(req) {
			t.Fatalf("request %d carries the note of a cut thought, although the thought was within its budget", i)
		}
	}
}

func TestAThoughtPastItsBudgetIsKeptWhenItsMechanismIsOff(t *testing.T) {
	cfg := defaultConfig()
	cfg.ThinkingPlan = true
	cfg.ThinkingBudget = 8
	h := newHarness(t, t.TempDir(), cfg,
		fake.Reply{Reasoning: strings.Repeat("reasoning ", 20), Calls: []fake.Call{{Name: "list_dir", Arguments: "{}"}}},
		finishWith("Done."), finishWith("Done."))
	rec := withRecorder(h, cfg)
	sw, err := mech.Parse(mech.ThinkBudget)
	if err != nil {
		t.Fatal(err)
	}
	h.loop.SetSwitches(sw)
	if res := h.loop.Run(context.Background(), "Look, then finish."); res.Err != nil {
		t.Fatalf("the run failed: %v", res.Err)
	}
	for i, req := range rec.reqs {
		if hasCutNote(req) {
			t.Fatalf("request %d carries the note of a cut thought, although the mechanism is off", i)
		}
	}
	if len(rec.reqs) == 0 || rec.reqs[0].ReasoningBudget != 8 {
		t.Fatal("the first request lost its reasoning budget; only the NanoBird cut should be off")
	}
}
