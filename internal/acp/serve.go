package acp

import (
	"context"
	"io"

	sdk "github.com/coder/acp-go-sdk"
)

// Serve connects the agent to a client over stdio. It returns when the client
// closes the connection, or when ctx is cancelled (SPEC §8.7).
func Serve(ctx context.Context, a *Agent, in io.Reader, out io.Writer) error {
	a.conn = sdk.NewAgentSideConnection(a, out, in)
	close(a.ready)
	defer a.closeSessions()
	select {
	case <-a.conn.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// closeSessions releases the runner of every session, when the connection ends.
func (a *Agent) closeSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.sessions {
		s.runner.Close()
	}
}
