package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/usunrise88/nanobird/internal/model"
)

// serve starts a fake engine for one test.
func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL
}

// sse writes one SSE data line per payload and flushes after each, the way the
// engine streams a reply.
func sse(w http.ResponseWriter, payloads ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for _, p := range payloads {
		fmt.Fprintf(w, "data: %s\n\n", p)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func newTestClient(t *testing.T, endpoint string, stall time.Duration) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: endpoint, Model: "gemma-4-e4b", StallTimeout: stall})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// events reads a stream to its end. Any error other than io.EOF fails the test.
func events(t *testing.T, s model.Stream) []model.Event {
	t.Helper()
	defer s.Close()
	var out []model.Event
	for {
		ev, err := s.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, ev)
	}
}

// sameJSON fails the test unless got, a decoded JSON value, equals want.
func sameJSON(t *testing.T, got any, want string) {
	t.Helper()
	var expected any
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatalf("bad expectation: %v", err)
	}
	if !reflect.DeepEqual(got, expected) {
		pretty, _ := json.MarshalIndent(got, "", "  ")
		t.Errorf("request body mismatch; got:\n%s", pretty)
	}
}

func TestCompleteStreamsText(t *testing.T) {
	bodies := make(chan any, 1)
	endpoint := serve(t, func(w http.ResponseWriter, r *http.Request) {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		bodies <- body
		sse(w,
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":2}}`,
			`[DONE]`)
	})
	s, err := newTestClient(t, endpoint, time.Minute).Complete(context.Background(), model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "Be brief."},
			{Role: model.RoleUser, Content: "Say hello."},
		},
		MaxTokens: 8192,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got := events(t, s)
	want := []model.Event{
		{Text: "Hel"},
		{Text: "lo"},
		{Final: &model.Final{FinishReason: "stop", Usage: model.Usage{PromptTokens: 12, CompletionTokens: 2}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events:\n got: %+v\nwant: %+v", got, want)
	}
	sameJSON(t, <-bodies, `{
		"model": "gemma-4-e4b",
		"messages": [
			{"role": "system", "content": "Be brief."},
			{"role": "user", "content": "Say hello."}
		],
		"stream": true,
		"stream_options": {"include_usage": true},
		"temperature": 0,
		"max_tokens": 8192
	}`)
}

func TestCompleteAccumulatesToolCalls(t *testing.T) {
	endpoint := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"main.go\"}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"list_dir","arguments":"{}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`)
	})
	s, err := newTestClient(t, endpoint, time.Minute).Complete(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "read it"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got := events(t, s)
	if len(got) == 0 || got[len(got)-1].Final == nil {
		t.Fatalf("the reply does not end with a Final event: %+v", got)
	}
	final := got[len(got)-1].Final
	if final.FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls", final.FinishReason)
	}
	wantCalls := []model.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"main.go"}`},
		{ID: "call_2", Name: "list_dir", Arguments: "{}"},
	}
	if !reflect.DeepEqual(final.ToolCalls, wantCalls) {
		t.Errorf("tool calls:\n got: %+v\nwant: %+v", final.ToolCalls, wantCalls)
	}
	deltas := 0
	for _, ev := range got {
		if ev.ToolCall != nil {
			deltas++
		}
	}
	if deltas != 4 {
		t.Errorf("tool call deltas = %d, want 4", deltas)
	}
}

func TestCompleteEncodesToolHistory(t *testing.T) {
	type received struct {
		body any
		auth string
	}
	got := make(chan received, 1)
	endpoint := serve(t, func(w http.ResponseWriter, r *http.Request) {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		got <- received{body: body, auth: r.Header.Get("Authorization")}
		sse(w, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, `[DONE]`)
	})
	c, err := New(Config{BaseURL: endpoint, Model: "gemma-4-e4b", APIKey: "secret", StallTimeout: time.Minute})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s, err := c.Complete(context.Background(), model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "read main.go"},
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"main.go"}`}}},
			{Role: model.RoleTool, ToolCallID: "call_1", Content: "package main"},
		},
		Tools: []model.Tool{{Name: "read_file", Description: "Read a file.", Parameters: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	events(t, s)
	req := <-got
	if req.auth != "Bearer secret" {
		t.Errorf("Authorization = %q, want the bearer key", req.auth)
	}
	sameJSON(t, req.body, `{
		"model": "gemma-4-e4b",
		"messages": [
			{"role": "user", "content": "read main.go"},
			{"role": "assistant", "content": "", "tool_calls": [
				{"id": "call_1", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\":\"main.go\"}"}}
			]},
			{"role": "tool", "content": "package main", "tool_call_id": "call_1"}
		],
		"tools": [
			{"type": "function", "function": {"name": "read_file", "description": "Read a file.", "parameters": {"type": "object"}}}
		],
		"stream": true,
		"stream_options": {"include_usage": true},
		"temperature": 0
	}`)
}

func TestCompleteReportsHTTPError(t *testing.T) {
	endpoint := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"model is not loaded","type":"invalid_request_error"}}`)
	})
	_, err := newTestClient(t, endpoint, time.Minute).Complete(context.Background(), model.Request{})
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "model is not loaded") {
		t.Fatalf("Complete error = %v, want the status and the engine message", err)
	}
}

func TestMidStreamErrorEndsTheStream(t *testing.T) {
	endpoint := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`{"choices":[{"index":0,"delta":{"content":"par"},"finish_reason":null}]}`,
			`{"error":{"message":"context length exceeded","type":"invalid_request_error"}}`)
	})
	s, err := newTestClient(t, endpoint, time.Minute).Complete(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	defer s.Close()
	if ev, err := s.Next(); err != nil || ev.Text != "par" {
		t.Fatalf("first event = %+v, %v; want text par", ev, err)
	}
	for i := 1; i <= 2; i++ { // the error is sticky
		if _, err := s.Next(); err == nil || !strings.Contains(err.Error(), "context length exceeded") {
			t.Fatalf("Next #%d error = %v, want the engine message", i, err)
		}
	}
}

func TestStreamEndingWithoutDoneIsAnError(t *testing.T) {
	endpoint := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"index":0,"delta":{"content":"half"},"finish_reason":null}]}`)
	})
	s, err := newTestClient(t, endpoint, time.Minute).Complete(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	defer s.Close()
	if _, err := s.Next(); err != nil {
		t.Fatalf("first event: %v", err)
	}
	if _, err := s.Next(); err == nil || errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "before the final event") {
		t.Fatalf("Next after the cut = %v, want a truncation error", err)
	}
}

func TestStallIsReportedAsStall(t *testing.T) {
	endpoint := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"index":0,"delta":{"content":"start"},"finish_reason":null}]}`)
		<-r.Context().Done() // the engine goes silent and never finishes
	})
	s, err := newTestClient(t, endpoint, 300*time.Millisecond).Complete(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	defer s.Close()
	if _, err := s.Next(); err != nil {
		t.Fatalf("first event: %v", err)
	}
	if _, err := s.Next(); err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("Next after silence = %v, want a stall error", err)
	}
}

// TestHeartbeatsDoNotHideAStall: heartbeats keep the connection open but show
// no progress, so a turn that only heartbeats still stalls.
func TestHeartbeatsDoNotHideAStall(t *testing.T) {
	endpoint := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				fmt.Fprint(w, ": keep-alive\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	})
	s, err := newTestClient(t, endpoint, 200*time.Millisecond).Complete(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	defer s.Close()
	if _, err := s.Next(); err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("Next = %v, want a stall error", err)
	}
}

// TestTokensKeepTheTurnAlive: every data line restarts the stall timer, so a
// reply that keeps producing text outlasts the timeout.
func TestTokensKeepTheTurnAlive(t *testing.T) {
	endpoint := serve(t, func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 5; i++ { // about 500 ms in total; the stall timeout is 250 ms
			sse(w, fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":"%d"},"finish_reason":null}]}`, i))
			time.Sleep(100 * time.Millisecond)
		}
		sse(w, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, `[DONE]`)
	})
	s, err := newTestClient(t, endpoint, 250*time.Millisecond).Complete(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got := events(t, s)
	if len(got) != 6 || got[0].Text != "0" || got[4].Text != "4" || got[5].Final == nil {
		t.Fatalf("events = %+v, want five text events and a final one", got)
	}
}

func TestOpenAIDialectHasNoTokenizeOrPrefix(t *testing.T) {
	c := newTestClient(t, "http://127.0.0.1:1", time.Minute) // nothing listens here; these calls never reach the network
	ctx := context.Background()
	caps, err := c.Capabilities(ctx)
	if err != nil || !caps.Tools || caps.Tokenize || caps.Prefix {
		t.Errorf("Capabilities = %+v, %v; want tools only", caps, err)
	}
	if _, err := c.Tokenize(ctx, "text"); !errors.Is(err, model.ErrUnsupported) {
		t.Errorf("Tokenize error = %v, want ErrUnsupported", err)
	}
	if _, err := c.Prefix(ctx, nil, nil); !errors.Is(err, model.ErrUnsupported) {
		t.Errorf("Prefix error = %v, want ErrUnsupported", err)
	}
	if err := c.Release(ctx, ""); !errors.Is(err, model.ErrUnsupported) {
		t.Errorf("Release error = %v, want ErrUnsupported", err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	for _, cfg := range []Config{
		{BaseURL: "127.0.0.1:8080", Model: "m"},
		{BaseURL: "http://", Model: "m"},
		{BaseURL: "http://127.0.0.1:8080"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) accepted an invalid config", cfg)
		}
	}
}
