package mcp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAClientServerWithAConfiguredNameIsSkipped(t *testing.T) {
	path := writeConfig(t, map[string]Spec{"fake": {Command: "/nonexistent/configured"}})
	m, err := LoadWith(context.Background(), path, map[string]Spec{"fake": {Command: "/nonexistent/client"}}, 12, time.Second, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for _, w := range m.Warnings {
		if strings.Contains(w, "from the client is already in mcp.json") {
			return
		}
	}
	t.Fatalf("warnings %v do not say that the client's entry was skipped", m.Warnings)
}
