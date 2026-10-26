package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// protocolVersion is the MCP revision the client speaks.
const protocolVersion = "2024-11-05"

// errorUnsupported is the JSON-RPC code for a method the client does not offer.
const errorUnsupported = -32601

// Spec is one server of mcp.json (SPEC §6.4). Trusted and AutoApprove decide
// whether its tools may run without a prompt; Allow and Deny curate its tools.
type Spec struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	Trusted     bool              `json:"trusted"`
	AutoApprove []string          `json:"auto_approve"`
	Allow       []string          `json:"allow"`
	Deny        []string          `json:"deny"`
}

// ToolInfo is a tool as a server describes it.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// rpc is one JSON-RPC message, in either direction.
type rpc struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Client is one stdio connection to an MCP server. Requests run one at a time.
type Client struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	inbox  chan rpc
	nextID int
}

// Start launches the server and performs the initialize handshake. The server's
// stderr is discarded, so a chatty server cannot fill the run's output.
func Start(ctx context.Context, name string, spec Spec) (*Client, error) {
	// The server lives as long as the client, not as long as the start-up context:
	// Close stops it.
	cmd := exec.CommandContext(context.Background(), spec.Command, spec.Args...)
	cmd.Env = os.Environ()
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp server %s: %w", name, err)
	}
	c := &Client{name: name, cmd: cmd, stdin: stdin, inbox: make(chan rpc, 64)}
	go c.read(stdout)
	_, err = c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "nanobird", "version": "dev"},
	})
	if err != nil {
		c.Close()
		return nil, err
	}
	if err := c.notify("notifications/initialized"); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// ListTools returns every tool the server offers, following its pages.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	var out []ToolInfo
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var r struct {
			Tools      []ToolInfo `json:"tools"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("mcp server %s: tools/list: %w", c.name, err)
		}
		out = append(out, r.Tools...)
		if r.NextCursor == "" {
			break
		}
		cursor = r.NextCursor
	}
	return out, nil
}

// CallTool runs one tool. The text parts of the result come back joined; other
// content is named by its type. A result the server marks as an error is an
// error.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("mcp server %s: tools/call: %w", c.name, err)
	}
	parts := make([]string, 0, len(r.Content))
	for _, p := range r.Content {
		if p.Type == "text" {
			parts = append(parts, p.Text)
		} else {
			parts = append(parts, "["+p.Type+" content]")
		}
	}
	text := strings.Join(parts, "\n")
	if r.IsError {
		return "", errors.New(text)
	}
	return text, nil
}

// Close stops the server.
func (c *Client) Close() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
}

// read pumps the server's messages into the inbox, one per line.
func (c *Client) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var m rpc
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			c.inbox <- m
		}
	}
	close(c.inbox)
}

// call sends one request and waits for its response. Requests from the server
// are answered: a ping gets an empty result, anything else an unsupported error.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	if err := c.send(rpc{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprint(id)), Method: method, Params: marshal(params)}); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("mcp server %s: %s: %w", c.name, method, ctx.Err())
		case m, ok := <-c.inbox:
			if !ok {
				return nil, fmt.Errorf("mcp server %s: the connection closed during %s", c.name, method)
			}
			if m.Method != "" {
				if len(m.ID) > 0 {
					c.answer(m)
				}
				continue
			}
			var got int
			if json.Unmarshal(m.ID, &got) != nil || got != id {
				continue
			}
			if m.Error != nil {
				return nil, fmt.Errorf("mcp server %s: %s: %s (code %d)", c.name, method, m.Error.Message, m.Error.Code)
			}
			return m.Result, nil
		}
	}
}

// notify sends a notification, which has no response.
func (c *Client) notify(method string) error {
	return c.send(rpc{JSONRPC: "2.0", Method: method})
}

// answer replies to a request the server made.
func (c *Client) answer(m rpc) {
	reply := rpc{JSONRPC: "2.0", ID: m.ID}
	if m.Method == "ping" {
		reply.Result = json.RawMessage("{}")
	} else {
		reply.Error = &rpcError{Code: errorUnsupported, Message: "method not supported by nanobird: " + m.Method}
	}
	_ = c.send(reply)
}

func (c *Client) send(m rpc) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("mcp server %s: %w", c.name, err)
	}
	return nil
}

// marshal encodes request parameters. Parameters that cannot be encoded send null.
func marshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}
