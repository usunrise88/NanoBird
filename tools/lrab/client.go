package main

import (
	"context"
	"sync"

	sdk "github.com/coder/acp-go-sdk"
)

// client is the ACP client side of a cell. It answers permission requests by policy,
// refuses the file system and terminal methods that the runner does not offer, and
// counts the mechanism firings that the agent reports in _meta.nanobird (SPEC §8.8).
type client struct {
	allow bool // answer a permission request with allow once; otherwise reject it

	mu      sync.Mutex
	firings map[string]int
}

func newClient(allow bool) *client {
	return &client{allow: allow, firings: map[string]int{}}
}

// counts returns the firings of each mechanism so far.
func (c *client) counts() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.firings))
	for k, v := range c.firings {
		out[k] = v
	}
	return out
}

// RequestPermission answers by the policy of the run. The runner offers no file system
// or terminal, so the agent asks only for the calls its safety rings stopped (SPEC §5.11).
func (c *client) RequestPermission(_ context.Context, p sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	want := sdk.PermissionOptionKindRejectOnce
	if c.allow {
		want = sdk.PermissionOptionKindAllowOnce
	}
	for _, o := range p.Options {
		if o.Kind == want {
			return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeSelected(o.OptionId)}, nil
		}
	}
	return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeCancelled()}, nil
}

// SessionUpdate counts the mechanism firings that a tool-call update reports.
func (c *client) SessionUpdate(_ context.Context, n sdk.SessionNotification) error {
	u := n.Update.ToolCallUpdate
	if u == nil {
		return nil
	}
	nb, ok := u.Meta["nanobird"].(map[string]any)
	if !ok {
		return nil
	}
	id, ok := nb["mechanism"].(string)
	if !ok {
		return nil
	}
	c.mu.Lock()
	c.firings[id]++
	c.mu.Unlock()
	return nil
}

// The runner advertises no file system and no terminal, so the agent must not call these.

func (c *client) ReadTextFile(context.Context, sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error) {
	return sdk.ReadTextFileResponse{}, sdk.NewMethodNotFound("fs/read_text_file")
}

func (c *client) WriteTextFile(context.Context, sdk.WriteTextFileRequest) (sdk.WriteTextFileResponse, error) {
	return sdk.WriteTextFileResponse{}, sdk.NewMethodNotFound("fs/write_text_file")
}

func (c *client) CreateTerminal(context.Context, sdk.CreateTerminalRequest) (sdk.CreateTerminalResponse, error) {
	return sdk.CreateTerminalResponse{}, sdk.NewMethodNotFound("terminal/create")
}

func (c *client) KillTerminal(context.Context, sdk.KillTerminalRequest) (sdk.KillTerminalResponse, error) {
	return sdk.KillTerminalResponse{}, sdk.NewMethodNotFound("terminal/kill")
}

func (c *client) TerminalOutput(context.Context, sdk.TerminalOutputRequest) (sdk.TerminalOutputResponse, error) {
	return sdk.TerminalOutputResponse{}, sdk.NewMethodNotFound("terminal/output")
}

func (c *client) ReleaseTerminal(context.Context, sdk.ReleaseTerminalRequest) (sdk.ReleaseTerminalResponse, error) {
	return sdk.ReleaseTerminalResponse{}, sdk.NewMethodNotFound("terminal/release")
}

func (c *client) WaitForTerminalExit(context.Context, sdk.WaitForTerminalExitRequest) (sdk.WaitForTerminalExitResponse, error) {
	return sdk.WaitForTerminalExitResponse{}, sdk.NewMethodNotFound("terminal/wait_for_exit")
}
