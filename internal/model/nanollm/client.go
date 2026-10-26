// Package nanollm is the native NanoLLM backend over the Harness API (SPEC §7.2).
package nanollm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/usunrise88/nanobird/internal/model"
)

// Config is the endpoint of one model.
type Config struct {
	BaseURL    string // the engine's address, such as http://127.0.0.1:8081
	Model      string // the model id, as the engine's catalog names it
	APIKey     string // sent as a bearer token when not empty
	ToolFormat string // loop.tool_format: "strict" or "rescue"
	HTTP       *http.Client
}

// Client talks to one model of a NanoLLM engine over its native API (/api/v1).
type Client struct {
	cfg  Config
	http *http.Client

	mu   sync.Mutex
	caps *capabilities // the probe, kept after the first request (SPEC §7.2)
}

// New checks the configuration. No request is sent until one is needed.
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("nanollm: endpoint %q is not a URL", cfg.BaseURL)
	}
	if cfg.Model == "" {
		return nil, errors.New("nanollm: the model name is empty")
	}
	if cfg.ToolFormat != "strict" && cfg.ToolFormat != "rescue" {
		return nil, fmt.Errorf("nanollm: tool format %q is neither strict nor rescue", cfg.ToolFormat)
	}
	h := cfg.HTTP
	if h == nil {
		h = &http.Client{}
	}
	return &Client{cfg: cfg, http: h}, nil
}

// capabilities is what GET /api/v1/models/{id}/capabilities reports (RFC R1).
type capabilities struct {
	Thinking        bool     `json:"thinking"`
	ToolFormats     []string `json:"tool_formats"`
	ReasoningBudget bool     `json:"reasoning_budget"`
	ParseReport     bool     `json:"parse_report"`
	AssistantPrefix bool     `json:"assistant_prefix"`
	PrefixCache     bool     `json:"prefix_cache"`
	Tokenize        bool     `json:"tokenize"`
}

// features probes the model once and keeps the answer.
func (c *Client) features(ctx context.Context) (capabilities, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.caps != nil {
		return *c.caps, nil
	}
	var got capabilities
	if err := c.getJSON(ctx, "/api/v1/models/"+url.PathEscape(c.cfg.Model)+"/capabilities", &got); err != nil {
		return capabilities{}, err
	}
	c.caps = &got
	return got, nil
}

// Capabilities reports the optional features of the model (SPEC §7.1). Prefix is false:
// prefix handles are not built, so the static prefill relies on the prompt cache.
func (c *Client) Capabilities(ctx context.Context) (model.Caps, error) {
	f, err := c.features(ctx)
	if err != nil {
		return model.Caps{}, err
	}
	return model.Caps{Tools: len(f.ToolFormats) > 0, Tokenize: f.Tokenize}, nil
}

// Complete sends one turn and streams its reply (SPEC §7.1). A tool format that the
// engine lacks falls back to rescue, and a reasoning budget that the engine lacks is
// not sent (SPEC §7.2).
func (c *Client) Complete(ctx context.Context, req model.Request) (model.Stream, error) {
	f, err := c.features(ctx)
	if err != nil {
		return nil, err
	}
	body := chatRequest{
		Model:       c.cfg.Model,
		Messages:    toMessages(req.Messages),
		Tools:       toTools(req.Tools),
		Sampling:    sampling{Temperature: req.Temperature},
		MaxTokens:   req.MaxTokens,
		ToolFormat:  c.toolFormat(f),
		CachePrompt: true,
		Stream:      true,
	}
	if f.Thinking {
		body.EnableThinking = req.Thinking
	}
	if f.ReasoningBudget && req.ReasoningBudget > 0 {
		body.MaxReasoningTokens = req.ReasoningBudget
	}
	answer, err := c.do(ctx, http.MethodPost, "/api/v1/chat", body)
	if err != nil {
		return nil, err
	}
	return &stream{body: answer, r: bufio.NewReader(answer)}, nil
}

// toolFormat is the format the engine serves: the configured one when the engine has
// it, and rescue otherwise (SPEC §7.2).
func (c *Client) toolFormat(f capabilities) string {
	if slices.Contains(f.ToolFormats, c.cfg.ToolFormat) {
		return c.cfg.ToolFormat
	}
	return "rescue"
}

// Tokenize counts the tokens of a text with the engine's tokenizer (RFC R5).
func (c *Client) Tokenize(ctx context.Context, text string) (int, error) {
	var got struct {
		Count int `json:"count"`
	}
	body := map[string]string{"model": c.cfg.Model, "text": text}
	if err := c.postJSON(ctx, "/api/v1/tokenize", body, &got); err != nil {
		return 0, err
	}
	return got.Count, nil
}

// Prefix returns no handle: prefix handles are not built (SPEC §7.2). The engine's
// prompt cache keeps the static prefill, and usage.cached_tokens shows the reuse.
func (c *Client) Prefix(context.Context, []model.Message, []model.Tool) (model.PrefixHandle, error) {
	return "", nil
}

// Release has nothing to release, because Prefix hands out no handles.
func (c *Client) Release(context.Context, model.PrefixHandle) error {
	return nil
}

// ListModels returns the ids of the models the engine serves (GET /api/v1/models).
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	var got struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := c.getJSON(ctx, "/api/v1/models", &got); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(got.Models))
	for _, m := range got.Models {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// do sends a JSON request and returns the body of a 2xx answer, which the caller
// closes. Any other answer becomes an error that carries the problem's title and detail.
func (c *Client) do(ctx context.Context, method, path string, body any) (io.ReadCloser, error) {
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.BaseURL, "/")+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, problemError(resp.StatusCode, resp.Body)
	}
	return resp.Body, nil
}

func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	answer, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer answer.Close()
	return json.NewDecoder(answer).Decode(v)
}

func (c *Client) postJSON(ctx context.Context, path string, body, v any) error {
	answer, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer answer.Close()
	return json.NewDecoder(answer).Decode(v)
}

// problemError turns an RFC 9457 problem into an error.
func problemError(status int, r io.Reader) error {
	var p struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	_ = json.NewDecoder(io.LimitReader(r, 64<<10)).Decode(&p)
	if p.Detail == "" {
		p.Detail = http.StatusText(status)
	}
	return fmt.Errorf("nanollm: %s: %s (HTTP %d)", p.Title, p.Detail, status)
}

type sampling struct {
	Temperature float64 `json:"temperature"`
}

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type chatRequest struct {
	Model              string    `json:"model"`
	Messages           []message `json:"messages"`
	Tools              []tool    `json:"tools,omitempty"`
	Sampling           sampling  `json:"sampling"`
	MaxTokens          int       `json:"max_tokens,omitempty"`
	EnableThinking     *bool     `json:"enable_thinking,omitempty"`
	MaxReasoningTokens int       `json:"max_reasoning_tokens,omitempty"`
	ToolFormat         string    `json:"tool_format"`
	CachePrompt        bool      `json:"cache_prompt"`
	Stream             bool      `json:"stream"`
}

func toMessages(msgs []model.Message) []message {
	out := make([]message, 0, len(msgs))
	for _, m := range msgs {
		msg := message{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, c := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, toolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
		}
		out = append(out, msg)
	}
	return out
}

func toTools(tools []model.Tool) []tool {
	out := make([]tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	return out
}

// stream reads one reply from the native events (SPEC §7.1): "delta" carries text and
// reasoning, "done" carries the whole result, and "error" carries a problem.
type stream struct {
	body  io.ReadCloser
	r     *bufio.Reader
	queue []model.Event
	done  bool
}

func (s *stream) Next() (model.Event, error) {
	for len(s.queue) == 0 {
		if s.done {
			return model.Event{}, io.EOF
		}
		name, data, err := readEvent(s.r)
		if err != nil {
			return model.Event{}, err
		}
		switch name {
		case "delta":
			var d struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			}
			if err := json.Unmarshal(data, &d); err != nil {
				return model.Event{}, fmt.Errorf("nanollm: delta: %w", err)
			}
			if d.Content != "" {
				s.queue = append(s.queue, model.Event{Text: d.Content})
			}
			if d.ReasoningContent != "" {
				s.queue = append(s.queue, model.Event{Reasoning: d.ReasoningContent})
			}
		case "done":
			var res chatResult
			if err := json.Unmarshal(data, &res); err != nil {
				return model.Event{}, fmt.Errorf("nanollm: done: %w", err)
			}
			s.done = true
			s.queue = append(s.queue, model.Event{Final: res.final()})
		case "error":
			var p struct {
				Title  string `json:"title"`
				Detail string `json:"detail"`
			}
			_ = json.Unmarshal(data, &p)
			return model.Event{}, fmt.Errorf("nanollm: %s: %s", p.Title, p.Detail)
		}
	}
	ev := s.queue[0]
	s.queue = s.queue[1:]
	return ev, nil
}

// Close releases the connection.
func (s *stream) Close() error {
	return s.body.Close()
}

// readEvent reads one server-sent event: its name, and its data lines joined by newlines.
func readEvent(r *bufio.Reader) (string, []byte, error) {
	var name string
	var data []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if name == "" && len(data) == 0 {
				continue
			}
			return name, []byte(strings.Join(data, "\n")), nil
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
}

// chatResult is the "done" event: the whole reply.
type chatResult struct {
	Message struct {
		Content          string     `json:"content"`
		ReasoningContent string     `json:"reasoning_content"`
		ToolCalls        []toolCall `json:"tool_calls"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
	Usage        struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		ReasoningTokens  int `json:"reasoning_tokens"`
	} `json:"usage"`
	Parse *struct {
		Status string `json:"status"`
		Raw    string `json:"raw"`
		Error  string `json:"error"`
	} `json:"parse"`
}

func (r chatResult) final() *model.Final {
	f := &model.Final{
		FinishReason: r.FinishReason,
		Usage: model.Usage{
			PromptTokens:     r.Usage.PromptTokens,
			CompletionTokens: r.Usage.CompletionTokens,
			ReasoningTokens:  r.Usage.ReasoningTokens,
		},
	}
	for _, c := range r.Message.ToolCalls {
		f.ToolCalls = append(f.ToolCalls, model.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
	}
	if r.Parse != nil {
		f.Parse = &model.ParseReport{Status: r.Parse.Status, Raw: r.Parse.Raw, Error: r.Parse.Error}
	}
	return f
}
