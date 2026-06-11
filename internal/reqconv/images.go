package reqconv

import (
	"log/slog"
	"strings"

	"github.com/d-kuro/kirocc/internal/anthropic"
	"github.com/d-kuro/kirocc/internal/kiroproto"
)

func convertImageBlock(b anthropic.ContentBlock) (kiroproto.Image, bool) {
	if b.Source == nil || b.Source.Type != "base64" {
		if b.Source != nil {
			slog.Warn("skipping non-base64 image source type", "type", b.Source.Type)
		}
		return kiroproto.Image{}, false
	}
	format := b.Source.MediaType
	if idx := strings.LastIndex(format, "/"); idx >= 0 {
		format = format[idx+1:]
	}
	return kiroproto.Image{
		Format: format,
		Source: kiroproto.ImageSource{Bytes: b.Source.Data},
	}, true
}

// ExtractImages extracts image blocks from message content and converts to Kiro format.
// Also extracts images nested inside tool_result content blocks.
func ExtractImages(content anthropic.MessageContent) []kiroproto.Image {
	if content.IsString() {
		return nil
	}
	var images []kiroproto.Image
	for _, b := range content.Blocks {
		switch {
		case b.Type == anthropic.BlockTypeImage:
			if img, ok := convertImageBlock(b); ok {
				images = append(images, img)
			}
		case b.IsToolResult() && !b.Content.IsString():
			for _, cb := range b.Content.Blocks {
				if cb.Type == anthropic.BlockTypeImage {
					if img, ok := convertImageBlock(cb); ok {
						images = append(images, img)
					}
				}
			}
		}
	}
	return images
}
