package loop

import (
	"github.com/usunrise88/nanobird/internal/model"
)

// The records of the transcript (SPEC §9.2). Each kind has its own struct so
// the field names stay fixed.

type taskRecord struct {
	Text string `json:"text"`
}

// classifyRecord is the mode a prompt was classified as (M2, SPEC §4.1).
type classifyRecord struct {
	Mode string `json:"mode"`
}

// routeRecord names the categories the router loaded for a task (M1).
type routeRecord struct {
	Categories []string `json:"categories"`
}

type callRecord struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type messageRecord struct {
	Role       string       `json:"role"`
	Content    string       `json:"content"`
	ToolCalls  []callRecord `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

// requestRecord is the exact request sent to the model.
type requestRecord struct {
	Turn        int             `json:"turn"`
	Messages    []messageRecord `json:"messages"`
	Tools       []string        `json:"tools"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens"`
	Thinking    bool            `json:"thinking"`
}

func newRequestRecord(turn int, req model.Request) requestRecord {
	rec := requestRecord{
		Turn:        turn,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Thinking:    req.Thinking != nil && *req.Thinking,
	}
	for _, m := range req.Messages {
		mr := messageRecord{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, c := range m.ToolCalls {
			mr.ToolCalls = append(mr.ToolCalls, callRecord{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
		}
		rec.Messages = append(rec.Messages, mr)
	}
	for _, t := range req.Tools {
		rec.Tools = append(rec.Tools, t.Name)
	}
	return rec
}

// replyRecord is one model reply, as the loop read it.
type replyRecord struct {
	Turn   int          `json:"turn"`
	Text   string       `json:"text"`
	Finish string       `json:"finish"`
	Calls  []callRecord `json:"calls"`
	Usage  usageRecord  `json:"usage"`
}

type usageRecord struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
	Reasoning  int `json:"reasoning_tokens"`
}

func newReplyRecord(turn int, r model.Reply) replyRecord {
	rec := replyRecord{
		Turn:   turn,
		Text:   r.Text,
		Finish: r.Finish,
		Calls:  []callRecord{},
		Usage:  usageRecord{Prompt: r.Usage.PromptTokens, Completion: r.Usage.CompletionTokens, Reasoning: r.Usage.ReasoningTokens},
	}
	for _, c := range r.Calls {
		rec.Calls = append(rec.Calls, callRecord{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
	}
	return rec
}

type interventionRecord struct {
	Turn    int    `json:"turn"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type mechanismRecord struct {
	ID      string `json:"id"`
	Turn    int    `json:"turn"`
	Event   string `json:"event"`
	Attempt int    `json:"attempt,omitempty"`
	Count   int    `json:"count,omitempty"`
	Error   string `json:"error,omitempty"`
}

// judgementRecord is the finish gate's decision on one finish call (SPEC §5.3).
type judgementRecord struct {
	Turn       int    `json:"turn"`
	Check      string `json:"check,omitempty"`
	Accepted   bool   `json:"accepted"`
	Unverified bool   `json:"unverified,omitempty"`
	Message    string `json:"message,omitempty"`
}

type toolCallRecord struct {
	Turn      int    `json:"turn"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolResultRecord struct {
	Turn   int    `json:"turn"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Output string `json:"output"`
}

type stopRecord struct {
	Reason     string `json:"reason"`
	Turns      int    `json:"turns"`
	Summary    string `json:"summary,omitempty"`
	Unverified bool   `json:"unverified,omitempty"`
	Blocker    string `json:"blocker,omitempty"`
	Error      string `json:"error,omitempty"`
}
