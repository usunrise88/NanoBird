package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/usunrise88/nanobird/internal/model/fake"
)

// engine pairs a fake model with a fixed model list.
type engine struct {
	*fake.Client
	models []string
	err    error
}

func (e engine) ListModels(context.Context) ([]string, error) {
	return e.models, e.err
}

func text(s string) fake.Reply {
	return fake.Reply{Text: s}
}

func call(args string) fake.Reply {
	return fake.Reply{Calls: []fake.Call{{Name: "echo_args", Arguments: args}}}
}

func find(t *testing.T, results []Result, name string) Result {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no result named %q in %+v", name, results)
	return Result{}
}

func TestAllProbesPass(t *testing.T) {
	script := []fake.Reply{text("12:00")}
	script = append(script, call(`{"filter":"jq '.items[] | {id, name}'"}`))
	for i := 0; i < emptyRuns; i++ {
		script = append(script, fake.Reply{Calls: []fake.Call{{Name: "get_time", Arguments: "{}"}}})
	}
	e := engine{Client: fake.New(script...), models: []string{"gemma-4-e4b-qat"}}
	results := Run(context.Background(), e, "gemma-4-e4b-qat")
	for _, name := range []string{"endpoint", "generation after a tool result", "tool-call argument round trip", "empty completions"} {
		if r := find(t, results, name); r.Status != Pass {
			t.Errorf("%s: %s (%s)", name, r.Status, r.Detail)
		}
	}
	if r := find(t, results, "context size"); r.Status != Skip {
		t.Errorf("context size must be a skip until the engine reports it, got %s", r.Status)
	}
}

func TestEndpointFailsWhenTheModelIsMissing(t *testing.T) {
	e := engine{Client: fake.New(), models: []string{"other-model"}}
	r := find(t, Run(context.Background(), e, "gemma-4-e4b-qat"), "endpoint")
	if r.Status != Fail || !strings.Contains(r.Detail, "other-model") {
		t.Fatalf("endpoint = %+v", r)
	}
}

func TestEndpointFailsWhenUnreachable(t *testing.T) {
	e := engine{Client: fake.New(), err: errors.New("connection refused")}
	if r := find(t, Run(context.Background(), e, "m"), "endpoint"); r.Status != Fail {
		t.Fatalf("endpoint = %+v", r)
	}
}

func TestEmptyReplyAfterToolResultFails(t *testing.T) {
	e := engine{Client: fake.New(fake.Reply{})}
	if r := find(t, Run(context.Background(), e, "m"), "generation after a tool result"); r.Status != Fail {
		t.Fatalf("generation = %+v", r)
	}
}

func TestMangledArgumentFails(t *testing.T) {
	e := engine{Client: fake.New(text("ok"), call(`{"filter":"jq"}`))}
	if r := find(t, Run(context.Background(), e, "m"), "tool-call argument round trip"); r.Status != Fail {
		t.Fatalf("argument probe = %+v", r)
	}
}

func TestOneEmptyCompletionFailsTheRate(t *testing.T) {
	script := []fake.Reply{text("ok"), call(`{"filter":"jq '.items[] | {id, name}'"}`)}
	for i := 0; i < emptyRuns; i++ {
		if i == 7 {
			script = append(script, fake.Reply{})
			continue
		}
		script = append(script, fake.Reply{Calls: []fake.Call{{Name: "get_time", Arguments: "{}"}}})
	}
	e := engine{Client: fake.New(script...)}
	r := find(t, Run(context.Background(), e, "m"), "empty completions")
	if r.Status != Fail || !strings.Contains(r.Detail, "1 of 20") {
		t.Fatalf("empty completions = %+v", r)
	}
}
