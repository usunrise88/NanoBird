package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// protocolVersion is the MCP revision the client asks for. A server answers with
// the revision it speaks, and the client uses that one from then on.
const protocolVersion = "2024-11-05"

// errorUnsupported is the JSON-RPC code for a method the client does not offer.
const errorUnsupported = -32601

// Spec is one server of mcp.json (SPEC §6.4). A server runs as a command over
// stdio, or is reached at a URL over streamable HTTP. Headers go with every request
// to the URL; $NAME and ${NAME} in a header value expand from the environment, so a
// token need not sit in the file. Trusted and AutoApprove decide whether its tools
// may run without a prompt; Allow and Deny curate its tools.
type Spec struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
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
	Annotations Annotations     `json:"annotations"`
}

// Annotations are the hints a server gives about a tool. A hint the server leaves
// out is nil. The client acts on them only for a trusted server (SPEC §6.4).
type Annotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint"`
	DestructiveHint *bool `json:"destructiveHint"`
	OpenWorldHint   *bool `json:"openWorldHint"`
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

// Client is one connection to an MCP server. Requests run one at a time.
type Client struct {
	name   string
	t      transport
	nextID int
}

// transport carries JSON-RPC messages to one server, over stdio or streamable HTTP.
type transport interface {
	// roundTrip sends a request and returns its reply. A request that the server
	// makes meanwhile goes to answer.
	roundTrip(ctx context.Context, req rpc, answer func(context.Context, rpc)) (rpc, error)
	// send sends a message that has no reply: a notification or an answer.
	send(ctx context.Context, m rpc) error
	// negotiated records the protocol version the server agreed to.
	negotiated(version string)
	// close ends the connection.
	close()
}

// Start connects to a server and performs the initialize handshake. A server has
// a command or a URL, and exactly one of them.
func Start(ctx context.Context, name string, spec Spec) (*Client, error) {
	if (spec.Command == "") == (spec.URL == "") {
		return nil, errors.New("set exactly one of command (stdio) or url (streamable HTTP)")
	}
	var t transport
	if spec.URL != "" {
		t = newStreamable(spec)
	} else {
		s, err := startStdio(name, spec)
		if err != nil {
			return nil, err
		}
		t = s
	}
	c := &Client{name: name, t: t}
	raw, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "nanobird", "version": "dev"},
	})
	if err != nil {
		c.Close()
		return nil, err
	}
	var agreed struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &agreed); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp server %s: initialize: %w", name, err)
	}
	t.negotiated(agreed.ProtocolVersion)
	if err := c.notify(ctx, "notifications/initialized"); err != nil {
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

// Close ends the connection, and with it the server.
func (c *Client) Close() {
	c.t.close()
}

// call sends one request and waits for its reply.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.nextID++
	req := rpc{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprint(c.nextID)), Method: method, Params: marshal(params)}
	m, err := c.t.roundTrip(ctx, req, c.answer)
	if err != nil {
		return nil, fmt.Errorf("mcp server %s: %s: %w", c.name, method, err)
	}
	if m.Error != nil {
		return nil, fmt.Errorf("mcp server %s: %s: %s (code %d)", c.name, method, m.Error.Message, m.Error.Code)
	}
	return m.Result, nil
}

// notify sends a notification, which has no reply.
func (c *Client) notify(ctx context.Context, method string) error {
	return c.t.send(ctx, rpc{JSONRPC: "2.0", Method: method})
}

// answer replies to a request the server made. A ping gets an empty result, and
// anything else an unsupported error.
func (c *Client) answer(ctx context.Context, m rpc) {
	reply := rpc{JSONRPC: "2.0", ID: m.ID}
	if m.Method == "ping" {
		reply.Result = json.RawMessage("{}")
	} else {
		reply.Error = &rpcError{Code: errorUnsupported, Message: "method not supported by nanobird: " + m.Method}
	}
	_ = c.t.send(ctx, reply)
}

// sameID reports whether two JSON-RPC ids are the same. A reply carries the id of
// its request.
func sameID(a, b json.RawMessage) bool {
	return len(a) > 0 && bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
}

// marshal encodes request parameters. Parameters that cannot be encoded send null.
func marshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}
