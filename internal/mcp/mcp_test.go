package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary act as an MCP server when NB_MCP_FAKE is set.
// The tests start it as their server, so no other program is needed.
func TestMain(m *testing.M) {
	if os.Getenv("NB_MCP_FAKE") == "1" {
		serveFake()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// serveFake answers the requests of a small MCP server: an echo tool, and a
// slow tool that answers after two seconds.
func serveFake() {
	sc := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	for sc.Scan() {
		var m rpc
		if json.Unmarshal(sc.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue
		}
		var result any
		var rerr *rpcError
		switch m.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "fake"}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{
				{"name": "echo", "description": "Echo the text argument. Second sentence.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]string{"type": "string"}}}, "annotations": map[string]any{"readOnlyHint": true}},
				{"name": "slow", "description": "Wait two seconds, then answer.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
			}}
		case "tools/call":
			var p struct {
				Name      string `json:"name"`
				Arguments struct {
					Text string `json:"text"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(m.Params, &p)
			switch p.Name {
			case "echo":
				result = content(p.Arguments.Text)
			case "slow":
				time.Sleep(2 * time.Second)
				result = content("late")
			default:
				rerr = &rpcError{Code: -32602, Message: "unknown tool"}
			}
		default:
			rerr = &rpcError{Code: errorUnsupported, Message: "no such method"}
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": m.ID}
		if rerr != nil {
			resp["error"] = rerr
		} else {
			resp["result"] = result
		}
		data, _ := json.Marshal(resp)
		_, _ = out.Write(append(data, '\n'))
		_ = out.Flush()
	}
}

func content(text string) map[string]any {
	return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}
}

func fakeSpec(trusted bool) Spec {
	return Spec{Command: os.Args[0], Env: map[string]string{"NB_MCP_FAKE": "1"}, Trusted: trusted}
}

func writeConfig(t *testing.T, servers map[string]Spec) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"servers": servers})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func toolNames(m *Manager) []string {
	var names []string
	for _, t := range m.Tools() {
		names = append(names, t.Name)
	}
	return names
}

func TestClientListsAndCalls(t *testing.T) {
	ctx := context.Background()
	c, err := Start(ctx, "fake", fakeSpec(true))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()
	list, err := c.ListTools(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "echo" {
		t.Fatalf("tools = %+v, %v", list, err)
	}
	out, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || out != "hi" {
		t.Fatalf("echo = %q, %v", out, err)
	}
}

func TestManagerOffersTrustedToolsFlat(t *testing.T) {
	path := writeConfig(t, map[string]Spec{"fake": fakeSpec(true)})
	m, err := Load(context.Background(), path, 12, 5*time.Second, []string{"read_file"}, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer m.Close()
	if got := strings.Join(toolNames(m), ","); got != "fake.echo,fake.slow" {
		t.Fatalf("tools = %s", got)
	}
}

func TestUntrustedToolNeedsPermission(t *testing.T) {
	path := writeConfig(t, map[string]Spec{"fake": fakeSpec(false)})
	m, err := Load(context.Background(), path, 12, 5*time.Second, nil, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer m.Close()
	_, err = m.Tools()[0].Run(context.Background(), nil, json.RawMessage(`{"text":"hi"}`))
	if err == nil || !strings.Contains(err.Error(), "needs permission") {
		t.Fatalf("an untrusted call ran: %v", err)
	}
}

func TestAutoApproveLetsAnUntrustedToolRun(t *testing.T) {
	spec := fakeSpec(false)
	spec.AutoApprove = []string{"echo"}
	m, err := Load(context.Background(), writeConfig(t, map[string]Spec{"fake": spec}), 12, 5*time.Second, nil, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer m.Close()
	out, err := m.Tools()[0].Run(context.Background(), nil, json.RawMessage(`{"text":"ok"}`))
	if err != nil || out != "ok" {
		t.Fatalf("auto-approved echo = %q, %v", out, err)
	}
}

func TestFlatLimitLoadsNothingAndSaysSo(t *testing.T) {
	m, err := Load(context.Background(), writeConfig(t, map[string]Spec{"fake": fakeSpec(true)}), 1, 5*time.Second, nil, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer m.Close()
	if len(m.Tools()) != 0 || len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "flat_limit") {
		t.Fatalf("tools = %v, warnings = %v", toolNames(m), m.Warnings)
	}
}

func TestCurationAndBuiltinsWin(t *testing.T) {
	spec := fakeSpec(true)
	spec.Deny = []string{"slow"}
	m, err := Load(context.Background(), writeConfig(t, map[string]Spec{"fake": spec}), 12, 5*time.Second, []string{"fake.echo"}, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer m.Close()
	if len(m.Tools()) != 0 {
		t.Fatalf("denied or duplicated tools were loaded: %v", toolNames(m))
	}
}

func TestTwoTimeoutsLeaveTheRun(t *testing.T) {
	spec := fakeSpec(true)
	spec.AutoApprove = []string{"slow"}
	m, err := Load(context.Background(), writeConfig(t, map[string]Spec{"fake": spec}), 12, 300*time.Millisecond, nil, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer m.Close()
	var slow = m.Tools()[1]
	for i := 0; i < maxTimeouts; i++ {
		if _, err := slow.Run(context.Background(), nil, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if _, err := slow.Run(context.Background(), nil, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "left the run") {
		t.Fatalf("third call: %v", err)
	}
}

func TestTrustedReadOnlyToolRunsUnattended(t *testing.T) {
	m, err := Load(context.Background(), writeConfig(t, map[string]Spec{"fake": fakeSpec(true)}), 12, 5*time.Second, nil, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer m.Close()
	out, err := m.Tools()[0].Run(context.Background(), nil, json.RawMessage(`{"text":"ok"}`))
	if err != nil || out != "ok" {
		t.Fatalf("read-only echo = %q, %v", out, err)
	}
}

func TestTrustedToolWithoutReadOnlyHintNeedsPermission(t *testing.T) {
	m, err := Load(context.Background(), writeConfig(t, map[string]Spec{"fake": fakeSpec(true)}), 12, 5*time.Second, nil, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer m.Close()
	if _, err := m.Tools()[1].Run(context.Background(), nil, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "needs permission") {
		t.Fatalf("a tool without readOnlyHint ran unattended: %v", err)
	}
}

func TestDenialFollowsTrustAndAnnotations(t *testing.T) {
	yes, no := true, false
	read := ToolInfo{Name: "read", Annotations: Annotations{ReadOnlyHint: &yes}}
	for _, c := range []struct {
		name   string
		spec   Spec
		info   ToolInfo
		denied bool
	}{
		{"trusted and read-only", Spec{Trusted: true}, read, false},
		{"trusted without the hint", Spec{Trusted: true}, ToolInfo{Name: "write"}, true},
		{"trusted with readOnlyHint false", Spec{Trusted: true}, ToolInfo{Name: "write", Annotations: Annotations{ReadOnlyHint: &no}}, true},
		{"untrusted and read-only", Spec{}, read, true},
		{"auto-approved", Spec{AutoApprove: []string{"write"}}, ToolInfo{Name: "write"}, false},
	} {
		if got := denial(c.spec, c.info) != ""; got != c.denied {
			t.Errorf("%s: denied = %v, want %v", c.name, got, c.denied)
		}
	}
}

func TestOfflineHidesToolsThatReachOutside(t *testing.T) {
	yes := true
	open := ToolInfo{Name: "fetch", Annotations: Annotations{OpenWorldHint: &yes}}
	if reachable(open, true) {
		t.Fatal("an open-world tool was offered offline")
	}
	if !reachable(open, false) {
		t.Fatal("an open-world tool was hidden online")
	}
	if !reachable(ToolInfo{Name: "local"}, true) {
		t.Fatal("a tool without openWorldHint was hidden offline")
	}
}

func TestOfflineDoesNotStartURLServers(t *testing.T) {
	path := writeConfig(t, map[string]Spec{"web": {URL: "http://127.0.0.1:1", Trusted: true}})
	m, err := Load(context.Background(), path, 12, 5*time.Second, nil, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer m.Close()
	if len(m.Tools()) != 0 || len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "offline") {
		t.Fatalf("tools = %v, warnings = %v", toolNames(m), m.Warnings)
	}
}
