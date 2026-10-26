package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/coder/acp-go-sdk"
)

// The replies of the scripted model: one reply is one server-sent event stream, in
// the OpenAI dialect that the engine client reads.
const (
	listReply = `data: {"choices":[{"delta":{"content":"Let me check."}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"list_dir","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}

data: [DONE]

`
	finishReply = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_2","function":{"name":"finish","arguments":"{\"summary\":\"Listed the workspace.\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":6}}

data: [DONE]

`
)

// scriptedModel stands in for the engine: each request gets the next reply, and the
// last reply repeats once the script runs out.
func scriptedModel(t *testing.T, replies ...string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	next := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		reply := replies[min(next, len(replies)-1)]
		next++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// testClient is the client side of the end-to-end test. It keeps the text and the
// tool calls of the session updates, and it refuses the client methods.
type testClient struct {
	mu    sync.Mutex
	text  []string
	tools []string
}

func (c *testClient) SessionUpdate(_ context.Context, n sdk.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case n.Update.AgentMessageChunk != nil && n.Update.AgentMessageChunk.Content.Text != nil:
		c.text = append(c.text, n.Update.AgentMessageChunk.Content.Text.Text)
	case n.Update.ToolCall != nil:
		c.tools = append(c.tools, n.Update.ToolCall.Title)
	}
	return nil
}

func (c *testClient) RequestPermission(context.Context, sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeCancelled()}, nil
}

func (c *testClient) ReadTextFile(context.Context, sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error) {
	return sdk.ReadTextFileResponse{}, sdk.NewMethodNotFound("fs/read_text_file")
}

func (c *testClient) WriteTextFile(context.Context, sdk.WriteTextFileRequest) (sdk.WriteTextFileResponse, error) {
	return sdk.WriteTextFileResponse{}, sdk.NewMethodNotFound("fs/write_text_file")
}

func (c *testClient) CreateTerminal(context.Context, sdk.CreateTerminalRequest) (sdk.CreateTerminalResponse, error) {
	return sdk.CreateTerminalResponse{}, sdk.NewMethodNotFound("terminal/create")
}

func (c *testClient) KillTerminal(context.Context, sdk.KillTerminalRequest) (sdk.KillTerminalResponse, error) {
	return sdk.KillTerminalResponse{}, sdk.NewMethodNotFound("terminal/kill")
}

func (c *testClient) TerminalOutput(context.Context, sdk.TerminalOutputRequest) (sdk.TerminalOutputResponse, error) {
	return sdk.TerminalOutputResponse{}, sdk.NewMethodNotFound("terminal/output")
}

func (c *testClient) ReleaseTerminal(context.Context, sdk.ReleaseTerminalRequest) (sdk.ReleaseTerminalResponse, error) {
	return sdk.ReleaseTerminalResponse{}, sdk.NewMethodNotFound("terminal/release")
}

func (c *testClient) WaitForTerminalExit(context.Context, sdk.WaitForTerminalExitRequest) (sdk.WaitForTerminalExitResponse, error) {
	return sdk.WaitForTerminalExitResponse{}, sdk.NewMethodNotFound("terminal/wait_for_exit")
}

// TestAcpServesAPromptOverTheEngineClient runs the command the way a client does,
// over stdio, with the engine client talking HTTP to a scripted model.
func TestAcpServesAPromptOverTheEngineClient(t *testing.T) {
	t.Setenv("NANOBIRD_MODEL_KEY", "test-key")
	workspace := t.TempDir()
	model := scriptedModel(t, listReply, finishReply, finishReply)
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	done := make(chan int, 1)
	go func() {
		done <- acpCommand([]string{"--endpoint", model.URL, "--model", "scripted", "--backend", "openai"}, agentIn, agentOut, io.Discard, "test")
	}()
	t.Cleanup(func() {
		_ = clientOut.Close()
		_ = agentOut.Close()
		<-done
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := &testClient{}
	conn := sdk.NewClientSideConnection(client, clientOut, clientIn)
	if _, err := conn.Initialize(ctx, sdk.InitializeRequest{ProtocolVersion: sdk.ProtocolVersionNumber}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	sess, err := conn.NewSession(ctx, sdk.NewSessionRequest{Cwd: workspace, McpServers: []sdk.McpServer{}})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	resp, err := conn.Prompt(ctx, sdk.PromptRequest{SessionId: sess.SessionId, Prompt: []sdk.ContentBlock{sdk.TextBlock("List the workspace.")}})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if resp.StopReason != sdk.StopReasonEndTurn {
		t.Fatalf("stop reason = %s", resp.StopReason)
	}
	if !strings.Contains(strings.Join(client.text, "|"), "Let me check.") || !strings.Contains(strings.Join(client.text, "|"), "Listed the workspace.") {
		t.Fatalf("text = %q", client.text)
	}
	if len(client.tools) != 1 || client.tools[0] != "list_dir" {
		t.Fatalf("tool calls = %q", client.tools)
	}
}
