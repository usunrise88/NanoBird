package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Plan is the todo list of a run (SPEC §6.1). The finish gate reads it (M3).
type Plan struct {
	Items []PlanItem
}

// PlanItem is one step of the plan.
type PlanItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// Open lists the items that are not done.
func (p *Plan) Open() []string {
	var open []string
	for _, it := range p.Items {
		if !it.Done {
			open = append(open, it.Text)
		}
	}
	return open
}

// Render lists the plan, one numbered item per line.
func (p *Plan) Render() string {
	if len(p.Items) == 0 {
		return "(no plan)"
	}
	var b strings.Builder
	for i, it := range p.Items {
		mark := " "
		if it.Done {
			mark = "x"
		}
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, mark, it.Text)
	}
	return b.String()
}

// find returns the index of the item whose text or 1-based number is s, or -1.
func (p *Plan) find(s string) int {
	for i, it := range p.Items {
		if it.Text == s || s == strconv.Itoa(i+1) {
			return i
		}
	}
	return -1
}

var todoTool = Tool{
	Name:        "todo",
	Description: "Keep the plan of the task. set replaces the plan with items. update marks items done, by their text or number, or every item with all=true. show lists the plan.",
	Parameters: schema(`{
		"action": {"type": "string", "enum": ["set", "update", "show"], "description": "What to do with the plan"},
		"items": {"type": "array", "items": {"type": "string"}, "description": "Items for set, or the items to mark done for update"},
		"all": {"type": "boolean", "description": "Mark every item done (update)"}
	}`, "action"),
	run: runTodo,
}

func runTodo(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Action string   `json:"action"`
		Items  []string `json:"items"`
		All    bool     `json:"all"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	p := &env.Plan
	switch a.Action {
	case "set":
		if len(a.Items) == 0 {
			return "", errors.New("set needs items")
		}
		p.Items = nil
		for _, t := range a.Items {
			p.Items = append(p.Items, PlanItem{Text: strings.TrimSpace(t)})
		}
	case "update":
		if a.All {
			for i := range p.Items {
				p.Items[i].Done = true
			}
			break
		}
		var unknown []string
		for _, t := range a.Items {
			i := p.find(strings.TrimSpace(t))
			if i < 0 {
				unknown = append(unknown, t)
				continue
			}
			p.Items[i].Done = true
		}
		if len(unknown) > 0 {
			return "", fmt.Errorf("no plan item matches %s; the plan is:\n%s", strings.Join(unknown, ", "), p.Render())
		}
	case "show":
	default:
		return "", fmt.Errorf("unknown action %q; use set, update or show", a.Action)
	}
	return p.Render(), nil
}

var finishTool = Tool{
	Name:        "finish",
	Description: "End the task with a summary of what changed. The harness checks the claim before it accepts the finish.",
	Parameters: schema(`{
		"summary": {"type": "string", "description": "What was done, in English"}
	}`, "summary"),
	run: runFinish,
}

// runFinish returns the summary. The loop decides whether the finish is
// accepted, not this call.
func runFinish(_ context.Context, _ *Env, args json.RawMessage) (string, error) {
	var a struct {
		Summary string `json:"summary"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	return a.Summary, nil
}
