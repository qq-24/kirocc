package websearch

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

type Result struct {
	Title   string `json:"title"`
	URL     string `json:"link"`
	Snippet string `json:"snippet"`
}

type serperResponse struct {
	Organic []Result `json:"organic"`
}

// Search calls the Serper.dev Google Search API.
// Returns up to maxResults organic results.
func Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	apiKey := os.Getenv("SERPER_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("SERPER_API_KEY not set")
	}
	if maxResults <= 0 {
		maxResults = 5
	}

	body, _ := json.Marshal(map[string]any{"q": query, "num": maxResults})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://google.serper.dev/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-KEY", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("serper request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("serper returned %d: %s", resp.StatusCode, b)
	}

	var sr serperResponse
	if err := json.UnmarshalRead(resp.Body, &sr); err != nil {
		return nil, fmt.Errorf("serper response parse error: %w", err)
	}
	if len(sr.Organic) > maxResults {
		sr.Organic = sr.Organic[:maxResults]
	}
	return sr.Organic, nil
}
