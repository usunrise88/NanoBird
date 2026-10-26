// Package doctor runs the engine probes of SPEC §7.4 before a benchmark and on
// demand. Each probe reports pass, fail or skip by name. A failed probe is never
// averaged into a score, and a skipped one is never a pass.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/usunrise88/nanobird/internal/model"
)

// Status is the outcome of one probe.
type Status string

// The outcomes of a probe.
const (
	Pass Status = "pass"
	Fail Status = "fail"
	Skip Status = "skip"
)

// Result is the outcome of one probe.
type Result struct {
	Name   string
	Status Status
	Detail string
}

// Engine is the endpoint under test. It is a model client that can also list
// the models it serves.
type Engine interface {
	model.Client
	ListModels(ctx context.Context) ([]string, error)
}

const (
	probeTimeout = 2 * time.Minute
	// emptyRuns is how many fixed tool-turn prompts the empty-completion probe
	// sends (SPEC §7.4, probe 3).
	emptyRuns = 20
	// filterText is the argument that must reach the engine intact (SPEC §7.4,
	// probe 2).
	filterText = "jq '.items[] | {id, name}'"
)

var (
	timeTool = model.Tool{
		Name:        "get_time",
		Description: "Return the current time.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
	echoTool = model.Tool{
		Name:        "echo_args",
		Description: "Echo the filter argument back.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"filter":{"type":"string"}},"required":["filter"]}`),
	}
	thinkingOff = false
)

// Run executes the probes in order against the model modelID on e.
func Run(ctx context.Context, e Engine, modelID string) []Result {
	return []Result{
		endpointProbe(ctx, e, modelID),
		generationProbe(ctx, e),
		argumentProbe(ctx, e),
		emptyProbe(ctx, e),
		{Name: "context size", Status: Skip, Detail: "the engine reports no context size yet (SPEC §7.2, RFC R1)"},
	}
}

func endpointProbe(ctx context.Context, e Engine, modelID string) Result {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	ids, err := e.ListModels(ctx)
	if err != nil {
		return Result{Name: "endpoint", Status: Fail, Detail: err.Error()}
	}
	if !slices.Contains(ids, modelID) {
		return Result{Name: "endpoint", Status: Fail, Detail: fmt.Sprintf("%s is not served; the engine lists: %s", modelID, strings.Join(ids, ", "))}
	}
	return Result{Name: "endpoint", Status: Pass, Detail: "serves " + modelID}
}

// generationProbe checks that the reply after a tool result is not empty
// (SPEC §7.4, probe 1). The probe runs with thinking off.
func generationProbe(ctx context.Context, e Engine) Result {
	const name = "generation after a tool result"
	r, err := complete(ctx, e, model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "What time is it? Use the get_time tool."},
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "probe_1", Name: "get_time", Arguments: "{}"}}},
			{Role: model.RoleTool, ToolCallID: "probe_1", Content: "12:00"},
		},
		Tools:     []model.Tool{timeTool},
		MaxTokens: 256,
		Thinking:  &thinkingOff,
	})
	if err != nil {
		return Result{Name: name, Status: Fail, Detail: err.Error()}
	}
	if isEmpty(r) {
		return Result{Name: name, Status: Fail, Detail: "the reply after the tool result was empty"}
	}
	return Result{Name: name, Status: Pass, Detail: "the reply is not empty"}
}

// argumentProbe checks that a tool argument with quotes, pipes and braces
// arrives intact (SPEC §7.4, probe 2).
func argumentProbe(ctx context.Context, e Engine) Result {
	const name = "tool-call argument round trip"
	r, err := complete(ctx, e, model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Call the echo_args tool with filter set to exactly this text: " + filterText},
		},
		Tools:     []model.Tool{echoTool},
		MaxTokens: 256,
		Thinking:  &thinkingOff,
	})
	if err != nil {
		return Result{Name: name, Status: Fail, Detail: err.Error()}
	}
	var got []string
	for _, c := range r.Calls {
		if c.Name != "echo_args" {
			continue
		}
		var a struct {
			Filter string `json:"filter"`
		}
		if json.Unmarshal([]byte(c.Arguments), &a) == nil && a.Filter == filterText {
			return Result{Name: name, Status: Pass, Detail: "the filter arrived intact"}
		}
		got = append(got, c.Arguments)
	}
	return Result{Name: name, Status: Fail, Detail: fmt.Sprintf("the call did not carry the filter intact; calls: %q", got)}
}

// emptyProbe counts empty completions over fixed tool-turn prompts at
// temperature 0. Any empty completion fails the probe (SPEC §7.4, probe 3).
func emptyProbe(ctx context.Context, e Engine) Result {
	const name = "empty completions"
	empty := 0
	for i := 1; i <= emptyRuns; i++ {
		r, err := complete(ctx, e, model.Request{
			Messages: []model.Message{
				{Role: model.RoleUser, Content: fmt.Sprintf("Call the get_time tool. Probe %d of %d.", i, emptyRuns)},
			},
			Tools:     []model.Tool{timeTool},
			MaxTokens: 256,
			Thinking:  &thinkingOff,
		})
		if err != nil {
			return Result{Name: name, Status: Fail, Detail: fmt.Sprintf("probe %d: %v", i, err)}
		}
		if isEmpty(r) {
			empty++
		}
	}
	if empty > 0 {
		return Result{Name: name, Status: Fail, Detail: fmt.Sprintf("%d of %d completions were empty", empty, emptyRuns)}
	}
	return Result{Name: name, Status: Pass, Detail: fmt.Sprintf("0 of %d completions were empty", emptyRuns)}
}

// complete runs one turn and returns the whole reply.
func complete(ctx context.Context, e Engine, req model.Request) (model.Reply, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	s, err := e.Complete(ctx, req)
	if err != nil {
		return model.Reply{}, err
	}
	return model.Collect(s)
}

// isEmpty reports a reply with neither text nor a tool call.
func isEmpty(r model.Reply) bool {
	return strings.TrimSpace(r.Text) == "" && len(r.Calls) == 0
}
