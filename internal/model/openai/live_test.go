package openai

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/usunrise88/nanobird/internal/model"
)

// TestLiveEndpoint runs one streamed turn, with a tool offered, against a real
// OpenAI-compatible engine. It is skipped unless NANOBIRD_TEST_ENDPOINT and
// NANOBIRD_TEST_MODEL are set. An API key, if the engine asks for one, comes
// from NANOBIRD_MODEL_KEY.
func TestLiveEndpoint(t *testing.T) {
	endpoint, id := os.Getenv("NANOBIRD_TEST_ENDPOINT"), os.Getenv("NANOBIRD_TEST_MODEL")
	if endpoint == "" || id == "" {
		t.Skip("set NANOBIRD_TEST_ENDPOINT and NANOBIRD_TEST_MODEL to run against a live engine")
	}
	c, err := New(Config{BaseURL: endpoint, Model: id, APIKey: os.Getenv("NANOBIRD_MODEL_KEY"), StallTimeout: 5 * time.Minute})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s, err := c.Complete(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "Reply with one word."}},
		Tools: []model.Tool{{
			Name:        "get_time",
			Description: "Return the current time.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		}},
		MaxTokens: 64,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got := events(t, s)
	if len(got) == 0 || got[len(got)-1].Final == nil || got[len(got)-1].Final.FinishReason == "" {
		t.Fatalf("the reply does not end with a Final event that has a finish reason: %+v", got)
	}
	final := got[len(got)-1].Final
	t.Logf("finish=%s usage=%+v tool_calls=%d events=%d", final.FinishReason, final.Usage, len(final.ToolCalls), len(got))
}
