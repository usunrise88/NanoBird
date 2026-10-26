package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/usunrise88/nanobird/internal/tools"
)

// startTimeout bounds the start and the tool listing of one server.
const startTimeout = 30 * time.Second

// maxTimeouts is how many calls may time out before a server leaves the run
// (SPEC §6.4).
const maxTimeouts = 2

// file is the layout of mcp.json.
type file struct {
	Servers map[string]Spec `json:"servers"`
}

// candidate is one tool that passed curation, with the connection it runs on.
type candidate struct {
	server string
	spec   Spec
	info   ToolInfo
	client *Client
	state  *serverState
}

// serverState counts the timeouts of one server during the run.
type serverState struct {
	timeouts int
}

// Manager runs the configured servers and offers their tools flat, as
// server.tool (SPEC §6.4). Scoped disclosure above the flat limit is not built
// yet. Above the limit no MCP tool loads, and Warnings says so.
type Manager struct {
	clients  []*Client
	tools    []tools.Tool
	ask      Ask
	always   map[string]bool
	Warnings []string
}

// Ask asks the client whether a call that needs a permission may run (SPEC §6.4). The
// client answers allow once, allow always, or reject.
type Ask func(ctx context.Context, p tools.Permission) tools.Answer

// SetAsk sets the hook that asks the client about calls that need a permission. Without
// one, such calls are denied, as in a run with no one to ask (SPEC §6.4, §5.11).
func (m *Manager) SetAsk(ask Ask) {
	m.ask = ask
}

// Load is LoadWith without the servers that a client passed to its session.
func Load(ctx context.Context, path string, flatLimit int, callTimeout time.Duration, builtin []string, offline bool) (*Manager, error) {
	return LoadWith(ctx, path, nil, flatLimit, callTimeout, builtin, offline)
}

// LoadWith starts the servers of the config file and the extra servers that a
// client passed to its session, and lists their tools. A missing file means no
// servers. A server that fails to start is a warning, not an error. An extra server
// with the name of a configured one is skipped with a warning (SPEC §6.4). builtin
// names the built-in tools; an MCP tool with the same name is hidden, because the
// built-in wins (SPEC §6.4). Offline, tools that reach outside the machine are
// hidden too.
func LoadWith(ctx context.Context, path string, extra map[string]Spec, flatLimit int, callTimeout time.Duration, builtin []string, offline bool) (*Manager, error) {
	m := &Manager{}
	servers, err := readServers(path)
	if err != nil {
		return nil, err
	}
	for _, name := range slices.Sorted(maps.Keys(extra)) {
		if _, ok := servers[name]; ok {
			m.Warnings = append(m.Warnings, fmt.Sprintf("mcp server %s from the client is already in mcp.json, so the client's entry is skipped", name))
			continue
		}
		servers[name] = extra[name]
	}
	var listed []candidate
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		spec := servers[name]
		if offline && spec.URL != "" {
			m.Warnings = append(m.Warnings, fmt.Sprintf("mcp server %s is reached over a URL, which offline mode does not start", name))
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, startTimeout)
		c, err := Start(sctx, name, spec)
		if err != nil {
			cancel()
			m.Warnings = append(m.Warnings, fmt.Sprintf("mcp server %s did not start: %v", name, err))
			continue
		}
		infos, err := c.ListTools(sctx)
		cancel()
		if err != nil {
			c.Close()
			m.Warnings = append(m.Warnings, fmt.Sprintf("mcp server %s did not list its tools: %v", name, err))
			continue
		}
		m.clients = append(m.clients, c)
		state := &serverState{}
		for _, info := range infos {
			if allowed(spec, info.Name) && reachable(info, offline) {
				listed = append(listed, candidate{server: name, spec: spec, info: info, client: c, state: state})
			}
		}
	}
	if len(listed) > flatLimit {
		m.Warnings = append(m.Warnings, fmt.Sprintf("%d MCP tools exceed mcp.flat_limit (%d); scoped disclosure is not built yet, so none are loaded", len(listed), flatLimit))
		return m, nil
	}
	for _, cand := range listed {
		name := cand.server + "." + cand.info.Name
		if slices.Contains(builtin, name) {
			continue
		}
		m.tools = append(m.tools, tools.NewExternal(name, cand.info.Description, cand.info.InputSchema, m.callFunc(cand, callTimeout)))
	}
	return m, nil
}

// readServers reads the servers of mcp.json. A missing file has no servers.
func readServers(path string) (map[string]Spec, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Spec{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Servers == nil {
		return map[string]Spec{}, nil
	}
	return f.Servers, nil
}

// Tools returns the flat MCP tools, ready to join the built-in registry.
func (m *Manager) Tools() []tools.Tool {
	return m.tools
}

// Close stops every server.
func (m *Manager) Close() {
	for _, c := range m.clients {
		c.Close()
	}
}

// allowed applies the curation of a server: allow is a whitelist when set, and
// deny always removes.
func allowed(spec Spec, tool string) bool {
	if len(spec.Allow) > 0 && !slices.Contains(spec.Allow, tool) {
		return false
	}
	return !slices.Contains(spec.Deny, tool)
}

// reachable reports whether a tool may be offered. Offline, a tool with
// openWorldHint is off, like web_fetch (SPEC §6.4).
func reachable(info ToolInfo, offline bool) bool {
	return !offline || info.Annotations.OpenWorldHint == nil || !*info.Annotations.OpenWorldHint
}

// denial says why a run without a prompt may not call the tool, or returns "" when
// it may. Only a tool in auto_approve, or a read-only tool of a trusted server, runs
// unattended. Every other call would ask, and a run has no one to ask (SPEC §6.4,
// §5.11). Annotations from an untrusted server never remove a prompt.
func denial(spec Spec, info ToolInfo) string {
	switch {
	case slices.Contains(spec.AutoApprove, info.Name):
		return ""
	case !spec.Trusted:
		return "comes from an untrusted MCP server"
	case info.Annotations.ReadOnlyHint == nil || !*info.Annotations.ReadOnlyHint:
		return "is not marked read-only by a trusted server"
	}
	return ""
}

// callFunc runs one MCP tool. A call that needs a permission asks the client, and is
// denied when the client refuses or no client is there to ask (SPEC §6.4, §5.11). A
// server that times out twice leaves the run.
func (m *Manager) callFunc(c candidate, timeout time.Duration) func(ctx context.Context, args json.RawMessage) (string, error) {
	name := c.server + "." + c.info.Name
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		if reason := denial(c.spec, c.info); reason != "" && !m.permit(ctx, name, reason) {
			return "", fmt.Errorf("needs permission: %s %s; the call was not allowed", name, reason)
		}
		if c.state.timeouts >= maxTimeouts {
			return "", fmt.Errorf("mcp server %s left the run after %d timeouts", c.server, c.state.timeouts)
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		out, err := c.client.CallTool(cctx, c.info.Name, args)
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			c.state.timeouts++
			return "", fmt.Errorf("%s timed out after %s", name, timeout)
		}
		return out, err
	}
}

// permit asks the client whether a call to the tool may run. A tool that the client
// allowed for the rest of the session runs without another ask.
func (m *Manager) permit(ctx context.Context, name, reason string) bool {
	if m.always[name] {
		return true
	}
	if m.ask == nil {
		return false
	}
	switch m.ask(ctx, tools.Permission{Tool: name, Reason: name + " " + reason, Family: name}) {
	case tools.AllowOnce:
		return true
	case tools.AllowAlways:
		if m.always == nil {
			m.always = map[string]bool{}
		}
		m.always[name] = true
		return true
	}
	return false
}
