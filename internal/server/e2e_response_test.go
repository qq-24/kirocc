package server

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/d-kuro/kirocc/internal/auth"
	"github.com/d-kuro/kirocc/internal/kiroclient"
	"github.com/d-kuro/kirocc/internal/kiroproto"
)

// errorClient always returns an error from GenerateAssistantResponse.
type errorClient struct {
	err error
}

func (c *errorClient) GenerateAssistantResponse(ctx context.Context, token string, payload *kiroproto.Payload, region string) (*kiroclient.Response, error) {
	return nil, c.err
}

type headerMultiResponseClient struct {
	responses    [][]any
	headers      []http.Header
	promptTokens int
	callCount    int
}

func (c *headerMultiResponseClient) GenerateAssistantResponse(ctx context.Context, token string, payload *kiroproto.Payload, region string) (*kiroclient.Response, error) {
	idx := c.callCount
	if idx >= len(c.responses) {
		idx = len(c.responses) - 1
	}
	c.callCount++
	body := buildEventStream(c.responses[idx]...)
	header := http.Header{}
	if idx < len(c.headers) {
		header = c.headers[idx].Clone()
	}
	return &kiroclient.Response{StatusCode: 200, Body: body, Header: header, PromptTokens: c.promptTokens}, nil
}

func TestE2E_InvalidStateEvent_PreStream(t *testing.T) {
	p1 := mustJSON(map[string]string{
		"reason":  "CONTENT_LENGTH_EXCEEDS_THRESHOLD",
		"message": "Too long",
	})
	client := &capturingClient{events: []any{"invalidStateEvent", p1}}

	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 400 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body = %s", resp.StatusCode, body)
	}
}

func TestE2E_InvalidStateEvent_MidStream(t *testing.T) {
	p1 := mustJSON(map[string]string{"content": "partial"})
	p2 := mustJSON(map[string]string{"message": "limit exceeded"})
	client := &capturingClient{events: []any{"assistantResponseEvent", p1, "invalidStateEvent", p2}}

	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"invalid_state"`) {
		t.Fatalf("missing error event in SSE: %s", body)
	}
}

func TestE2E_InvalidStateEvent_MidStream_ErrorEvent(t *testing.T) {
	p1 := mustJSON(map[string]string{"content": "partial"})
	p2 := mustJSON(map[string]string{"message": "throttled"})
	client := &capturingClient{events: []any{"assistantResponseEvent", p1, "invalidStateEvent", p2}}

	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	sseBody := string(body)
	if !strings.Contains(sseBody, "event: error") {
		t.Fatalf("missing error event: %s", sseBody)
	}
}

func TestE2E_EmptyMessages(t *testing.T) {
	p1 := mustJSON(map[string]string{"content": "ok"})
	client := &capturingClient{events: []any{"assistantResponseEvent", p1}}

	srv := newE2EServer(t, client)
	defer srv.Close()

	reqBody := `{
		"model":"claude-sonnet-4-6",
		"messages":[
			{"role":"user","content":""},
			{"role":"assistant","content":""},
			{"role":"user","content":"hi"}
		],
		"stream":false
	}`
	resp := postMessages(t, srv.URL, reqBody)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	if client.captured == nil {
		t.Fatal("payload not captured")
	}
	// Verify history was built (empty content is allowed — v2 captures show
	// tool-result continuations use content="" in real kiro-cli).
	if len(client.captured.ConversationState.History) == 0 {
		t.Fatal("expected non-empty history")
	}
}

func TestE2E_TokenUsage_CacheFields(t *testing.T) {
	p1 := mustJSON(map[string]string{"content": "ok"})
	meta := mustJSON(map[string]any{
		"tokenUsage": map[string]any{
			"uncachedInputTokens":   50,
			"outputTokens":          20,
			"totalTokens":           120,
			"cacheReadInputTokens":  40,
			"cacheWriteInputTokens": 10,
		},
	})
	client := &capturingClient{events: []any{"assistantResponseEvent", p1, "metadataEvent", meta}}

	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }()

	var result map[string]any
	_ = json.UnmarshalRead(resp.Body, &result)
	usage := result["usage"].(map[string]any)
	if usage["cache_read_input_tokens"] == nil {
		t.Fatal("missing cache_read_input_tokens")
	}
	if usage["cache_creation_input_tokens"] == nil {
		t.Fatal("missing cache_creation_input_tokens")
	}
	if int(usage["cache_read_input_tokens"].(float64)) != 40 {
		t.Fatalf("cache_read = %v", usage["cache_read_input_tokens"])
	}
}

func TestE2E_ToolDeduplication(t *testing.T) {
	tool1 := mustJSON(map[string]any{
		"name":      "read_file",
		"toolUseId": "tool_1",
		"input":     map[string]string{"path": "/tmp/a"},
		"stop":      true,
	})
	tool2 := mustJSON(map[string]any{
		"name":      "read_file",
		"toolUseId": "tool_1",
		"input":     map[string]string{"path": "/tmp/a"},
		"stop":      true,
	})
	client := &capturingClient{events: []any{"toolUseEvent", tool1, "toolUseEvent", tool2}}

	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }()

	var result map[string]any
	_ = json.UnmarshalRead(resp.Body, &result)
	content := result["content"].([]any)
	toolUseCount := 0
	for _, c := range content {
		block := c.(map[string]any)
		if block["type"] == "tool_use" {
			toolUseCount++
		}
	}
	if toolUseCount != 1 {
		t.Fatalf("tool_use count = %d, want 1 (dedup)", toolUseCount)
	}
}

func TestE2E_ToolInputMixed(t *testing.T) {
	// toolUseEvent with string input chunks (accumulated) then stop
	chunk1 := mustJSON(map[string]any{
		"name":      "write_file",
		"toolUseId": "tool_x",
		"input":     `{"path":`,
	})
	chunk2 := mustJSON(map[string]any{
		"input": `"/tmp/a"}`,
		"stop":  true,
	})
	client := &capturingClient{events: []any{"toolUseEvent", chunk1, "toolUseEvent", chunk2}}

	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }()

	var result map[string]any
	_ = json.UnmarshalRead(resp.Body, &result)
	content := result["content"].([]any)
	found := false
	for _, c := range content {
		block := c.(map[string]any)
		if block["type"] == "tool_use" {
			found = true
			if block["name"] != "write_file" {
				t.Fatalf("tool name = %v", block["name"])
			}
		}
	}
	if !found {
		t.Fatal("no tool_use block in response")
	}
}

func TestE2E_Truncation_Content(t *testing.T) {
	// Stream with text but no metadataEvent — response should still succeed.
	p1 := mustJSON(map[string]string{"content": "partial response"})
	client := &capturingClient{events: []any{"assistantResponseEvent", p1}}

	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var result map[string]any
	_ = json.UnmarshalRead(resp.Body, &result)
	// Response should still contain the partial text
	content := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("empty content")
	}
	block := content[0].(map[string]any)
	if block["text"] != "partial response" {
		t.Fatalf("text = %v", block["text"])
	}
}

func TestE2E_PreCountedTokens_NonStreaming(t *testing.T) {
	p1 := mustJSON(map[string]string{"content": "hello"})
	client := &capturingClient{
		events:       []any{"assistantResponseEvent", p1},
		promptTokens: 500, // backend value ignored; tiktoken PreCounted wins
	}

	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }()

	requireStatus(t, resp, 200)

	var result map[string]any
	_ = json.UnmarshalRead(resp.Body, &result)
	usage := result["usage"].(map[string]any)
	// PreCounted (tiktoken on raw body) wins over backend promptTokens.
	// Exact value depends on tiktoken encoding of the JSON body; must NOT be 500.
	inputTokens := int(usage["input_tokens"].(float64))
	if inputTokens == 500 {
		t.Fatal("input_tokens should be tiktoken PreCounted (not backend promptTokens)")
	}
	if inputTokens < 0 {
		t.Fatalf("input_tokens = %d, want >= 0", inputTokens)
	}
}

func TestE2E_PreCountedTokens_Streaming(t *testing.T) {
	p1 := mustJSON(map[string]string{"content": "hello"})
	client := &capturingClient{
		events:       []any{"assistantResponseEvent", p1},
		promptTokens: 750, // backend value ignored; tiktoken PreCounted wins
	}

	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	defer func() { _ = resp.Body.Close() }()

	requireStatus(t, resp, 200)

	body, _ := io.ReadAll(resp.Body)
	sseBody := string(body)
	// Must NOT contain 750 (backend's value); should use tiktoken PreCounted.
	if strings.Contains(sseBody, `"input_tokens":750`) {
		t.Fatal("should use tiktoken PreCounted, not backend promptTokens")
	}
}

func TestE2E_PreCountedTokens_ZeroFallback(t *testing.T) {
	p1 := mustJSON(map[string]string{"content": "hello"})
	client := &capturingClient{
		events:       []any{"assistantResponseEvent", p1},
		promptTokens: 0,
	}

	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }()

	requireStatus(t, resp, 200)

	var result map[string]any
	_ = json.UnmarshalRead(resp.Body, &result)
	usage := result["usage"].(map[string]any)
	// tiktoken always succeeds for text-only requests, so PreCounted > 0.
	// The test just verifies we don't crash when promptTokens=0.
	inputTokens := int(usage["input_tokens"].(float64))
	if inputTokens < 0 {
		t.Fatalf("input_tokens = %d, want >= 0", inputTokens)
	}
}

func TestE2E_PreCountedTokensWinOverMetadata(t *testing.T) {
	p1 := mustJSON(map[string]string{"content": "hello"})
	meta := mustJSON(map[string]any{
		"tokenUsage": map[string]any{
			"uncachedInputTokens": 100,
			"outputTokens":        50,
			"totalTokens":         150,
		},
	})
	client := &capturingClient{
		events:       []any{"assistantResponseEvent", p1, "metadataEvent", meta},
		promptTokens: 999,
	}

	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }()

	requireStatus(t, resp, 200)

	var result map[string]any
	_ = json.UnmarshalRead(resp.Body, &result)
	usage := result["usage"].(map[string]any)
	// PreCounted (tiktoken) wins over metadata's 100.
	inputTokens := int(usage["input_tokens"].(float64))
	if inputTokens == 100 {
		t.Fatal("PreCounted should win over metadata, but got metadata's value")
	}
}

func TestE2E_ClientError_Returns502(t *testing.T) {
	// When the kiro client returns an error, server should return 502
	errClient := &errorClient{err: io.EOF}

	mgr := &mockAuthManager{
		creds: &auth.Credentials{
			AccessToken: "test-token",
			ProfileARN:  "arn:test",
			Region:      "us-east-1",
		},
	}
	s := New(mgr, "", errClient)
	srv := newTCP4TestServer(t, s.Handler())
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 502 {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

func TestE2E_EmptyVisibleEndTurn_NonStreaming_RetrySucceeds(t *testing.T) {
	// First call: thinking-only via tags (empty visible end_turn).
	// Second call (retry): normal text response.
	thinkingOnly := []any{
		"assistantResponseEvent", mustJSON(map[string]string{"content": "<thinking>Let me think</thinking>"}),
		"metadataEvent", mustJSON(map[string]any{
			"tokenUsage": map[string]any{"uncachedInputTokens": 10, "outputTokens": 5, "totalTokens": 15},
		}),
	}
	normalResponse := []any{
		"assistantResponseEvent", mustJSON(map[string]string{"content": "Here is the answer"}),
		"metadataEvent", mustJSON(map[string]any{
			"tokenUsage": map[string]any{"uncachedInputTokens": 10, "outputTokens": 10, "totalTokens": 20},
		}),
	}
	client := &multiResponseClient{responses: [][]any{thinkingOnly, normalResponse}}
	srv := newE2EServerWithClient(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }()

	requireStatus(t, resp, 200)

	var result map[string]any
	_ = json.UnmarshalRead(resp.Body, &result)
	content := result["content"].([]any)
	// Should contain the retry's text, not the thinking-only response.
	found := false
	for _, c := range content {
		block := c.(map[string]any)
		if block["type"] == "text" && block["text"] == "Here is the answer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected retry text in response, got content: %v", content)
	}
	// Should have been called twice.
	if client.callCount != 2 {
		t.Fatalf("callCount = %d, want 2", client.callCount)
	}
}

func TestE2E_EmptyVisibleEndTurn_NonStreaming_RetryAlsoFails(t *testing.T) {
	// Both calls return thinking-only → should return 502.
	thinkingOnly := []any{
		"assistantResponseEvent", mustJSON(map[string]string{"content": "<thinking>Let me think</thinking>"}),
		"metadataEvent", mustJSON(map[string]any{
			"tokenUsage": map[string]any{"uncachedInputTokens": 10, "outputTokens": 5, "totalTokens": 15},
		}),
	}
	client := &multiResponseClient{responses: [][]any{thinkingOnly, thinkingOnly}}
	srv := newE2EServerWithClient(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }()

	requireStatus(t, resp, 502)
	if client.callCount != 2 {
		t.Fatalf("callCount = %d, want 2", client.callCount)
	}
}

func TestE2E_EmptyVisibleEndTurn_RetryClearsIDs(t *testing.T) {
	// Verify that retry clears ConversationID.
	thinkingOnly := []any{
		"assistantResponseEvent", mustJSON(map[string]string{"content": "<thinking>Let me think</thinking>"}),
		"metadataEvent", mustJSON(map[string]any{
			"tokenUsage": map[string]any{"uncachedInputTokens": 10, "outputTokens": 5, "totalTokens": 15},
		}),
	}
	normalResponse := []any{
		"assistantResponseEvent", mustJSON(map[string]string{"content": "Here is the answer"}),
		"metadataEvent", mustJSON(map[string]any{
			"tokenUsage": map[string]any{"uncachedInputTokens": 10, "outputTokens": 10, "totalTokens": 20},
		}),
	}
	client := &multiResponseClient{responses: [][]any{thinkingOnly, normalResponse}}
	srv := newE2EServerWithClient(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }()

	requireStatus(t, resp, 200)
	if client.callCount != 2 {
		t.Fatalf("callCount = %d, want 2", client.callCount)
	}
	// Both payloads point to the same object (mutated in-place before retry),
	// so we verify the final state has cleared IDs.
	if client.payloads[1].ConversationState.ConversationID != "" {
		t.Fatalf("attempt-2 ConversationID should be empty, got %q", client.payloads[1].ConversationState.ConversationID)
	}
}

// NOTE: TestE2E_EmptyVisibleEndTurn_Streaming tests were removed because
// GateWriter now promotes on first thinking delta, making streaming retry
// impossible once thinking has been sent. Non-streaming retry still works
// (tested by TestE2E_EmptyVisibleEndTurn_NonStreaming_RetrySucceeds above).

func TestE2E_Success_DoesNotSaveFailureCapture(t *testing.T) {
	logBuf := setupCaptureTest(t)

	p1 := mustJSON(map[string]string{"content": "ok"})
	client := &capturingClient{events: []any{"assistantResponseEvent", p1}}
	srv := newE2EServer(t, client)
	defer srv.Close()

	resp := postMessages(t, srv.URL, `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	defer func() { _ = resp.Body.Close() }()

	requireStatus(t, resp, 200)
	_, _ = io.ReadAll(resp.Body)

	if strings.Contains(logBuf.String(), "upstream failure capture") {
		t.Fatal("capture log should not appear on success")
	}
}
