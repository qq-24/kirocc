package messages

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/d-kuro/kirocc/internal/anthropic"
	"github.com/d-kuro/kirocc/internal/httpx"
	"github.com/d-kuro/kirocc/internal/models"
	"github.com/d-kuro/kirocc/internal/reqconv"
	"github.com/d-kuro/kirocc/internal/tokencount"
)

// HandleCountTokens serves POST /v1/messages/count_tokens.
func (s *Service) HandleCountTokens(w http.ResponseWriter, r *http.Request) {
	req, inputTokens, err := parseAndValidateRequest(r.Context(), w, r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, errTypeInvalidRequest, err.Error())
		return
	}
	_ = inputTokens

	profileARN := ""
	if creds, err := s.auth.GetToken(r.Context()); err == nil {
		profileARN = creds.ProfileARN
	} else {
		slog.DebugContext(r.Context(), "count_tokens proceeding without credentials", "err", err)
	}

	kiroModel, thinking, _, _ := models.Resolve(req.Model, anthropic.HasContext1MBeta(r.Header))
	if req.IsThinkingEnabled() {
		thinking = true
	}

	ccSessionID := r.Header.Get(headerCCSessionID)

	// Mirror the live send path so token counts include effort (envState is
	// derived inside BuildPayload from the system prompt).
	effort := resolveEffort(r.Context(), kiroModel, req, thinking)

	payload, _, err := reqconv.BuildPayload(req, reqconv.BuildOptions{ProfileARN: profileARN, ModelID: kiroModel, ConversationID: ccSessionID, Effort: effort})
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, errTypeInvalidRequest, err.Error())
		return
	}

	data, err := json.Marshal(payload)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, errTypeAPI, "failed to serialize payload")
		return
	}

	n, err := tokencount.CountBytes(data)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, errTypeAPI, "token counting unavailable")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.MarshalWrite(w, map[string]int{"input_tokens": n}); err != nil {
		slog.ErrorContext(r.Context(), "write count_tokens response failed", "err", err)
		return
	}
	_, _ = w.Write([]byte("\n"))
}

// parseAndValidateRequest decodes and validates an Anthropic request from the HTTP body.
// Returns the parsed request and the token count of the original request body.
func parseAndValidateRequest(ctx context.Context, w http.ResponseWriter, r *http.Request) (*anthropic.Request, int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid request: %w", err)
	}
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		slog.DebugContext(ctx, "client request body", "request_body", jsontext.Value(raw))
	}
	var req anthropic.Request
	if err := json.UnmarshalDecode(jsontext.NewDecoder(bytes.NewReader(raw)), &req); err != nil {
		return nil, 0, fmt.Errorf("invalid request: %w", err)
	}
	if len(req.Messages) == 0 {
		return nil, 0, fmt.Errorf("messages must not be empty")
	}
	inputTokens, _ := tokencount.CountBytes(raw)
	if requestHasImages(&req) {
		inputTokens = 0
	}
	return &req, inputTokens, nil
}

func requestHasImages(req *anthropic.Request) bool {
	for i := range req.Messages {
		for _, b := range req.Messages[i].Content.Blocks {
			if b.Type == anthropic.BlockTypeImage {
				return true
			}
		}
	}
	return false
}
