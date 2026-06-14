package messages

import (
	"fmt"
	"context"

	"github.com/d-kuro/kirocc/internal/anthropic"
)

func formatContextWindow(size int) string {
	if size >= 1_000_000 {
		return fmt.Sprintf("%dM", size/1_000_000)
	}
	return fmt.Sprintf("%dk", size/1000)
}

func resolveEffort(ctx context.Context, kiroModel string, req *anthropic.Request, thinking bool) string {
	return "max"
}

