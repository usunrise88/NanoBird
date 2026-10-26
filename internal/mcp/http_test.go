package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeStreamable is a streamable HTTP server for the tests. It needs the bearer
// token, and after initialize the session and the protocol version on every
// request. A tools/call answers on an event stream, after a ping from the server,
// so the client's answer goes through too. pinged records that it arrived.
func fakeStreamable(t *testing.T, token string, pinged *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		var m rpc
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, "not JSON-RPC", http.StatusBadRequest)
			return
		}
		if m.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "session-1")
			writeReply(w, m.ID, map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}})
			return
		}
		if r.Header.Get("Mcp-Session-Id") != "session-1" || r.Header.Get("MCP-Protocol-Version") != protocolVersion {
			http.Error(w, "no session", http.StatusBadRequest)
			return
		}
		switch m.Method {
		case "":
			pinged.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeReply(w, m.ID, map[string]any{"tools": []map[string]any{
				{"name": "echo", "description": "Echo the text.", "inputSchema": map[string]any{"type": "object"}},
			}})
		case "tools/call":
			var p struct {
				Arguments struct {
					Text string `json:"text"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(m.Params, &p)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"ping-1\",\"method\":\"ping\"}\n\n")
			fmt.Fprintf(w, "data: %s\n\n", reply(m.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": p.Arguments.Text}}}))
		default:
			http.Error(w, "unknown method", http.StatusNotFound)
		}
	}))
}

// reply is the JSON-RPC response to id with the given result.
func reply(id json.RawMessage, result any) string {
	data, _ := json.Marshal(rpc{JSONRPC: "2.0", ID: id, Result: marshal(result)})
	return string(data)
}

// writeReply answers with one JSON-RPC response.
func writeReply(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, reply(id, result))
}

func TestStreamableServerListsAndCallsTools(t *testing.T) {
	t.Setenv("NB_TEST_TOKEN", "secret")
	var pinged atomic.Bool
	srv := fakeStreamable(t, "secret", &pinged)
	defer srv.Close()
	ctx := context.Background()
	c, err := Start(ctx, "web", Spec{URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer $NB_TEST_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tools, err := c.ListTools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("ListTools = %+v, %v", tools, err)
	}
	out, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || out != "hi" {
		t.Fatalf("CallTool = %q, %v", out, err)
	}
	if !pinged.Load() {
		t.Fatal("the server's ping was not answered")
	}
}

func TestStreamableServerErrorsAreReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down for maintenance", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	_, err := Start(context.Background(), "web", Spec{URL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "HTTP 503: down for maintenance") {
		t.Fatalf("Start error = %v", err)
	}
}

func TestStartNeedsExactlyOneTransport(t *testing.T) {
	for _, spec := range []Spec{{}, {Command: "a", URL: "http://127.0.0.1:1"}} {
		if _, err := Start(context.Background(), "x", spec); err == nil {
			t.Fatalf("Start(%+v) succeeded", spec)
		}
	}
}

func TestManagerLoadsAStreamableServerFlat(t *testing.T) {
	var pinged atomic.Bool
	srv := fakeStreamable(t, "t", &pinged)
	defer srv.Close()
	path := writeConfig(t, map[string]Spec{"web": {URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer t"}, Trusted: true}})
	m, err := Load(context.Background(), path, 12, 10*time.Second, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if got := strings.Join(toolNames(m), ","); got != "web.echo" {
		t.Fatalf("tools = %q, warnings = %v", got, m.Warnings)
	}
}
