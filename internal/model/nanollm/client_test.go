package nanollm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/usunrise88/nanobird/internal/model"
)

// fakeEngine answers the native API the way NanoLLM does: the capability probe, the
// chat stream as named server-sent events, tokenize and the model list.
type fakeEngine struct {
	formats []string
	reply   string

	mu     sync.Mutex
	probes int
	chat   map[string]any
}

func (f *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/tiny/capabilities":
		f.probes++
		writeJSON(w, map[string]any{
			"thinking": true, "tool_formats": f.formats, "reasoning_budget": true,
			"parse_report": true, "assistant_prefix": true, "prefix_cache": true, "tokenize": true,
		})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/absent/capabilities":
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"title":"model not found","detail":"model \"absent\" is not in the catalog","status":404}`)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/chat":
		f.chat = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&f.chat)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, f.reply)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tokenize":
		writeJSON(w, map[string]any{"tokens": []int{1, 2, 3}, "count": 3})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
		writeJSON(w, map[string]any{"models": []map[string]any{{"id": "tiny"}, {"id": "other"}}})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeEngine) requestBody() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chat
}

func (f *fakeEngine) probeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.probes
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// sse writes one named server-sent event.
func sse(name, data string) string {
	return "event: " + name + "\ndata: " + data + "\n\n"
}

func newClient(t *testing.T, eng *fakeEngine, format string) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(eng)
	c, err := New(Config{BaseURL: srv.URL, Model: "tiny", ToolFormat: format})
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	return c, srv.Close
}

func TestCompleteStreamsAndCollectsTheReply(t *testing.T) {
	eng := &fakeEngine{formats: []string{"strict", "rescue"}, reply: sse("delta", `{"content":"Hi"}`) +
		sse("delta", `{"reasoning_content":"plan"}`) +
		sse("done", `{"message":{"content":"Hi","reasoning_content":"plan","tool_calls":[{"id":"call_1","name":"get_time","arguments":"{}"}]},"finish_reason":"tool_calls","usage":{"prompt_tokens":10,"completion_tokens":5,"reasoning_tokens":2},"parse":{"status":"ok"}}`)}
	c, done := newClient(t, eng, "strict")
	defer done()
	think := true
	s, err := c.Complete(context.Background(), model.Request{
		Messages:        []model.Message{{Role: model.RoleUser, Content: "Hi"}},
		Tools:           []model.Tool{{Name: "get_time", Description: "The time.", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Thinking:        &think,
		ReasoningBudget: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := model.Collect(s)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "Hi" || reply.Reasoning != "plan" || len(reply.Calls) != 1 || reply.Calls[0].Name != "get_time" ||
		reply.Finish != "tool_calls" || reply.Usage.PromptTokens != 10 {
		t.Fatalf("reply %+v", reply)
	}
	body := eng.requestBody()
	if body["tool_format"] != "strict" || body["cache_prompt"] != true || body["enable_thinking"] != true ||
		body["max_reasoning_tokens"] != float64(1024) || body["stream"] != true {
		t.Fatalf("request %v", body)
	}
}

func TestAFailedParseKeepsTheRawTextAndDropsTheCalls(t *testing.T) {
	raw := "<|tool_call>call:get_time{ broken<tool_call|>"
	eng := &fakeEngine{formats: []string{"rescue"}, reply: sse("delta", `{"content":"partial"}`) +
		sse("done", `{"message":{"content":"`+raw+`","tool_calls":[]},"finish_reason":"stop","usage":{"prompt_tokens":1,"completion_tokens":1},"parse":{"status":"failed","raw":"`+raw+`","error":"gemma4: broken call"}}`)}
	c, done := newClient(t, eng, "rescue")
	defer done()
	s, err := c.Complete(context.Background(), model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "Hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := model.Collect(s)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != raw || len(reply.Calls) != 0 {
		t.Fatalf("reply text %q, calls %v: a failed parse keeps the raw text and no calls", reply.Text, reply.Calls)
	}
}

func TestAnEngineWithoutStrictFallsBackToRescue(t *testing.T) {
	eng := &fakeEngine{formats: []string{"rescue"}, reply: sse("done", `{"message":{"content":"ok"},"finish_reason":"stop"}`)}
	c, done := newClient(t, eng, "strict")
	defer done()
	s, err := c.Complete(context.Background(), model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "Hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if got := eng.requestBody()["tool_format"]; got != "rescue" {
		t.Fatalf("tool_format %v, want the rescue fallback", got)
	}
}

func TestCapabilitiesAreProbedOnceAndMapped(t *testing.T) {
	eng := &fakeEngine{formats: []string{"strict", "rescue"}, reply: sse("done", `{"message":{"content":"ok"},"finish_reason":"stop"}`)}
	c, done := newClient(t, eng, "rescue")
	defer done()
	for i := 0; i < 2; i++ {
		caps, err := c.Capabilities(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !caps.Tools || !caps.Tokenize || caps.Prefix {
			t.Fatalf("caps %+v: tools and tokenize are offered, prefix handles are not", caps)
		}
	}
	if n := eng.probeCount(); n != 1 {
		t.Fatalf("the model was probed %d times, want once", n)
	}
}

func TestTokenizeCountsWithTheEngine(t *testing.T) {
	c, done := newClient(t, &fakeEngine{formats: []string{"rescue"}}, "rescue")
	defer done()
	n, err := c.Tokenize(context.Background(), "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("count %d, want 3", n)
	}
}

func TestListModelsReturnsTheIDs(t *testing.T) {
	c, done := newClient(t, &fakeEngine{formats: []string{"rescue"}}, "rescue")
	defer done()
	ids, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []string{"tiny", "other"}) {
		t.Fatalf("ids %v", ids)
	}
}

func TestAnAbsentModelIsAProblemNotAnEmptyProbe(t *testing.T) {
	srv := httptest.NewServer(&fakeEngine{})
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL, Model: "absent", ToolFormat: "rescue"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Capabilities(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not in the catalog") {
		t.Fatalf("error %v, want the engine's detail", err)
	}
}

func TestAnErrorEventEndsTheStreamWithAnError(t *testing.T) {
	eng := &fakeEngine{formats: []string{"rescue"}, reply: sse("error", `{"title":"engine failed","detail":"out of memory"}`)}
	c, done := newClient(t, eng, "rescue")
	defer done()
	s, err := c.Complete(context.Background(), model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "Hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.Collect(s); err == nil || !strings.Contains(err.Error(), "out of memory") {
		t.Fatalf("error %v, want the engine's detail", err)
	}
}

func TestNewRefusesABadConfiguration(t *testing.T) {
	if _, err := New(Config{BaseURL: "not a url", Model: "tiny", ToolFormat: "rescue"}); err == nil {
		t.Fatal("a bad endpoint was accepted")
	}
	if _, err := New(Config{BaseURL: "http://127.0.0.1:8081", ToolFormat: "rescue"}); err == nil {
		t.Fatal("an empty model name was accepted")
	}
	if _, err := New(Config{BaseURL: "http://127.0.0.1:8081", Model: "tiny", ToolFormat: "loose"}); err == nil {
		t.Fatal("an unknown tool format was accepted")
	}
}
