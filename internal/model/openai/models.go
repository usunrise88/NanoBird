package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ListModels returns the ids of the models the endpoint serves (GET /v1/models).
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("model: list models: %w", err)
	}
	if c.apiKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.hc.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("model: list models: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model: list models: %s: %s", resp.Status, errorMessage(resp))
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorBody)).Decode(&body); err != nil {
		return nil, fmt.Errorf("model: list models: %w", err)
	}
	ids := make([]string, 0, len(body.Data))
	for _, d := range body.Data {
		ids = append(ids, d.ID)
	}
	return ids, nil
}
