package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// closeWait bounds the DELETE that ends a session when the connection closes.
const closeWait = 5 * time.Second

// streamable is a streamable HTTP connection (SPEC §6.4). Each message is a POST
// to the server's URL. The reply to a request is one JSON message, or an event
// stream that ends with the reply. A server may issue a session at initialize;
// every later request carries it, with the negotiated protocol version.
type streamable struct {
	url     string
	headers map[string]string
	client  *http.Client
	session string
	version string
}

func newStreamable(spec Spec) *streamable {
	return &streamable{url: spec.URL, headers: spec.Headers, client: &http.Client{}}
}

// roundTrip posts the request and reads its reply. A request that the server
// makes on the stream goes to answer.
func (h *streamable) roundTrip(ctx context.Context, req rpc, answer func(context.Context, rpc)) (rpc, error) {
	resp, err := h.post(ctx, req)
	if err != nil {
		return rpc{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return readEvents(ctx, resp.Body, req.ID, answer)
	}
	var m rpc
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return rpc{}, fmt.Errorf("the reply is not a JSON-RPC message: %w", err)
	}
	return m, nil
}

// send posts a message that has no reply. The server answers 202 Accepted.
func (h *streamable) send(ctx context.Context, m rpc) error {
	resp, err := h.post(ctx, m)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// negotiated records the protocol version. Later requests carry it in the
// MCP-Protocol-Version header.
func (h *streamable) negotiated(version string) {
	h.version = version
}

// close ends the session with a DELETE, when the server issued one. A failure
// changes nothing: the connection is gone either way.
func (h *streamable) close() {
	if h.session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeWait)
	defer cancel()
	req, err := h.request(ctx, http.MethodDelete, nil)
	if err != nil {
		return
	}
	if resp, err := h.client.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

// post sends one message. A status that is not a success is an error, with the
// start of the body.
func (h *streamable) post(ctx context.Context, m rpc) (*http.Response, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	req, err := h.request(ctx, http.MethodPost, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		h.session = sid
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

// request builds a request with the configured headers, the session and the
// protocol version.
func (h *streamable) request(ctx context.Context, method string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, h.url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range h.headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}
	if h.session != "" {
		req.Header.Set("Mcp-Session-Id", h.session)
	}
	if h.version != "" {
		req.Header.Set("MCP-Protocol-Version", h.version)
	}
	return req, nil
}

// readEvents reads a server-sent event stream until the reply to the request with
// id arrives. Requests that the server makes on the way go to answer.
func readEvents(ctx context.Context, body io.Reader, id json.RawMessage, answer func(context.Context, rpc)) (rpc, error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var data []string
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			if v, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimPrefix(v, " "))
			}
			continue
		}
		if m, found := dispatch(ctx, data, id, answer); found {
			return m, nil
		}
		data = data[:0]
	}
	// A stream may end without the blank line that closes its last event.
	if m, found := dispatch(ctx, data, id, answer); found {
		return m, nil
	}
	if err := sc.Err(); err != nil {
		return rpc{}, err
	}
	return rpc{}, errors.New("the event stream ended before the reply")
}

// dispatch reads one event. A request from the server goes to answer. The reply
// to id is returned with true.
func dispatch(ctx context.Context, data []string, id json.RawMessage, answer func(context.Context, rpc)) (rpc, bool) {
	var m rpc
	if len(data) == 0 || json.Unmarshal([]byte(strings.Join(data, "\n")), &m) != nil {
		return rpc{}, false
	}
	switch {
	case m.Method != "" && len(m.ID) > 0:
		answer(ctx, m)
	case m.Method == "" && sameID(m.ID, id):
		return m, true
	}
	return rpc{}, false
}
