package webfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const maxBodySize = 512 * 1024 // 512 KB

var httpClient = &http.Client{Timeout: 30 * time.Second}

// Fetch performs an HTTP GET and returns the response body as text.
func Fetch(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; KiroccBot/1.0)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch returned %d for %s", resp.StatusCode, url)
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return "", fmt.Errorf("fetch read error: %w", err)
	}
	return string(b), nil
}
