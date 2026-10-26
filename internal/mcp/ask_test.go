package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/usunrise88/nanobird/internal/tools"
)

// untrustedEcho loads the fake server with every tool untrusted, so that each call asks.
func untrustedEcho(t *testing.T) *Manager {
	t.Helper()
	m, err := Load(context.Background(), writeConfig(t, map[string]Spec{"fake": fakeSpec(false)}), 12, 5*time.Second, nil, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(m.Close)
	return m
}

func TestAnAskedCallRunsWhenTheClientAllowsIt(t *testing.T) {
	m := untrustedEcho(t)
	var asked []tools.Permission
	m.SetAsk(func(_ context.Context, p tools.Permission) tools.Answer {
		asked = append(asked, p)
		return tools.AllowAlways
	})
	args := json.RawMessage(`{"text":"hi"}`)
	if _, err := m.Tools()[0].Run(context.Background(), nil, args); err != nil {
		t.Fatalf("an allowed call did not run: %v", err)
	}
	if _, err := m.Tools()[0].Run(context.Background(), nil, args); err != nil {
		t.Fatalf("the second call did not run: %v", err)
	}
	if len(asked) != 1 || asked[0].Tool != "fake.echo" || asked[0].Family != "fake.echo" {
		t.Fatalf("asked %+v, want one ask for fake.echo, which allow always covers", asked)
	}
}

func TestAnAskedCallIsDeniedWhenTheClientRefuses(t *testing.T) {
	m := untrustedEcho(t)
	m.SetAsk(func(context.Context, tools.Permission) tools.Answer { return tools.Deny })
	_, err := m.Tools()[0].Run(context.Background(), nil, json.RawMessage(`{"text":"hi"}`))
	if err == nil || !strings.Contains(err.Error(), "needs permission") {
		t.Fatalf("a refused call ran: %v", err)
	}
}
