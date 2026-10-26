package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/usunrise88/nanobird/internal/model"
)

const (
	// defaultStall is the silence after which a turn counts as stalled (SPEC §7.5).
	defaultStall = 120 * time.Second
	// connectTimeout bounds the TCP connect (SPEC §7.5).
	connectTimeout = 10 * time.Second
	// maxErrorBody bounds how much of a failed reply is read for its message.
	maxErrorBody = 64 << 10
)

var _ model.Client = (*Client)(nil)

// Config describes one OpenAI-compatible endpoint.
type Config struct {
	BaseURL      string        // endpoint root, for example http://127.0.0.1:8080
	Model        string        // model id sent with every request
	APIKey       string        // optional; sent as a bearer token
	StallTimeout time.Duration // time without data that counts as a stall; zero means 120 s
}

// Client talks to an OpenAI-compatible chat endpoint over HTTP.
type Client struct {
	endpoint string
	model    string
	apiKey   string
	stall    time.Duration
	hc       *http.Client
}

// New checks the configuration and returns a client for it.
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("model: endpoint %q is not an http or https URL", cfg.BaseURL)
	}
	if cfg.Model == "" {
		return nil, errors.New("model: no model id configured")
	}
	stall := cfg.StallTimeout
	if stall <= 0 {
		stall = defaultStall
	}
	return &Client{
		endpoint: strings.TrimSuffix(cfg.BaseURL, "/"),
		model:    cfg.Model,
		apiKey:   cfg.APIKey,
		stall:    stall,
		hc: &http.Client{Transport: &http.Transport{
			Proxy:       http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{Timeout: connectTimeout}).DialContext,
		}},
	}, nil
}

// Capabilities reports what the OpenAI dialect offers. It has no probe, so the
// answer is fixed.
func (c *Client) Capabilities(context.Context) (model.Caps, error) {
	return model.Caps{Tools: true}, nil
}

// Complete starts one streamed turn. Close the stream when done, whether it
// ended, failed or was abandoned.
func (c *Client) Complete(ctx context.Context, req model.Request) (model.Stream, error) {
	payload, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return nil, fmt.Errorf("model: encode request: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &stream{cancel: cancel, timeout: c.stall, calls: map[int]*partialCall{}}
	// The stall timer starts before the request, so an engine that never
	// answers is caught as well as one that goes quiet mid-reply.
	s.timer = time.AfterFunc(c.stall, s.onStall)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		s.shutdown()
		return nil, fmt.Errorf("model: build request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.hc.Do(hreq)
	if err != nil {
		s.shutdown()
		return nil, s.failure("request", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := errorMessage(resp)
		s.shutdown()
		return nil, fmt.Errorf("model: %s: %s", resp.Status, msg)
	}
	s.body = resp.Body
	s.r = bufio.NewReader(resp.Body)
	return s, nil
}

// Tokenize is not part of the OpenAI dialect; callers budget in bytes (SPEC §7.2).
func (c *Client) Tokenize(context.Context, string) (int, error) {
	return 0, model.ErrUnsupported
}

// Prefix is not part of the OpenAI dialect; the engine's own prompt cache
// applies instead (SPEC §7.2).
func (c *Client) Prefix(context.Context, []model.Message, []model.Tool) (model.PrefixHandle, error) {
	return "", model.ErrUnsupported
}

// Release has nothing to release, because Prefix is never offered.
func (c *Client) Release(context.Context, model.PrefixHandle) error {
	return model.ErrUnsupported
}

// chatRequest is the body of POST /v1/chat/completions.
type chatRequest struct {
	Model         string        `json:"model"`
	Messages      []chatMessage `json:"messages"`
	Tools         []chatTool    `json:"tools,omitempty"`
	Stream        bool          `json:"stream"`
	StreamOptions streamOptions `json:"stream_options"`
	Temperature   float64       `json:"temperature"`
	MaxTokens     int           `json:"max_tokens,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function callFunction `json:"function"`
}

type callFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func (c *Client) buildRequest(req model.Request) chatRequest {
	out := chatRequest{
		Model:         c.model,
		Stream:        true,
		StreamOptions: streamOptions{IncludeUsage: true},
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
	}
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, toChatMessage(m))
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, chatTool{Type: "function", Function: toolFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}})
	}
	return out
}

// toChatMessage maps a message to the wire shape. Arguments travel as JSON
// text, so an empty string becomes "{}".
func toChatMessage(m model.Message) chatMessage {
	out := chatMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
	for _, tc := range m.ToolCalls {
		args := tc.Arguments
		if args == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, chatToolCall{
			ID:       tc.ID,
			Type:     "function",
			Function: callFunction{Name: tc.Name, Arguments: args},
		})
	}
	return out
}

// errorMessage reads the message of a failed reply: the OpenAI error envelope
// when there is one, the raw body otherwise.
func errorMessage(resp *http.Response) string {
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	var env struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Error != nil && env.Error.Message != "" {
		return env.Error.Message
	}
	return strings.TrimSpace(string(raw))
}
