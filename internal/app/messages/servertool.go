package messages

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/d-kuro/kirocc/internal/anthropic"
	"github.com/d-kuro/kirocc/internal/auth"
	"github.com/d-kuro/kirocc/internal/httpx"
	"github.com/d-kuro/kirocc/internal/kiroproto"
	"github.com/d-kuro/kirocc/internal/logging"
	"github.com/d-kuro/kirocc/internal/reqconv"
	"github.com/d-kuro/kirocc/internal/respconv"
	"github.com/d-kuro/kirocc/internal/servertool"
	"github.com/d-kuro/kirocc/internal/webfetch"
	"github.com/d-kuro/kirocc/internal/websearch"
	"github.com/google/uuid"
)

const maxServerToolRounds = 5

type serverToolOrchestrator struct {
	service           *Service
	stCtx             *servertool.Context
	req               *anthropic.Request
	creds             *auth.Credentials
	buildOpts         reqconv.BuildOptions
	contextWindowSize int
	responseModel     string
}

func (o *serverToolOrchestrator) run(ctx context.Context, w http.ResponseWriter) string {
	if o.req.Stream {
		return o.handleStreaming(ctx, w)
	}
	return o.handleNonStreaming(ctx, w)
}

func (o *serverToolOrchestrator) handleStreaming(ctx context.Context, w http.ResponseWriter) string {
	_, short := logging.TraceIDs(ctx)

	gw := NewGateWriter(w)
	sw := respconv.NewSSEWriter(ctx, gw, o.responseModel, o.contextWindowSize, o.req.StopSequences, o.req.MaxTokens, 0)
	sw.OnVisibleOutput = func() { gw.Promote() }

	msgs := slices.Clone(o.req.Messages)
	var cumulativeInputTokens, cumulativeOutputTokens int

	for round := range maxServerToolRounds {
		payload, nameMap, err := o.buildPayload(msgs)
		if err != nil {
			slog.WarnContext(ctx, "server tool payload build error", "trace_id", short, "err", err)
			writeStreamingOrJSONError(gw, sw, w, http.StatusBadRequest, errTypeInvalidRequest, err.Error())
			return ""
		}
		sw.SetToolNameMap(nameMap.ReverseMap())

		apiResp, err := o.service.client.GenerateAssistantResponse(ctx, o.creds.AccessToken, payload, o.creds.Region)
		if err != nil {
			logUpstreamError(ctx, short, err, "round", round+1)
			writeStreamingOrJSONError(gw, sw, w, http.StatusBadGateway, errTypeAPI, "upstream API error")
			return ""
		}

		if round > 0 {
			in, out := sw.Usage()
			cumulativeInputTokens += in
			cumulativeOutputTokens += out
			sw.ResetAccumulator(o.contextWindowSize, o.req.StopSequences, o.req.MaxTokens, 0)
		}

		var foundServerTool bool
		var serverToolName, serverToolInput, originalToolUseID string
		var streamErr, localStop bool

		err = kiroproto.ParseStream(ctx, apiResp.Body, func(e kiroproto.Event) bool {
			if streamErr || localStop || foundServerTool {
				return true
			}
			if sw.WriteErr() {
				streamErr = true
				return true
			}
			if e.Type == kiroproto.EventToolUse && e.ToolStop {
				toolName := e.ToolName
				if mapped, ok := nameMap.ReverseMap()[toolName]; ok {
					toolName = mapped
				}
				if o.stCtx.IsServerTool(toolName) {
					foundServerTool = true
					serverToolName = toolName
					serverToolInput = e.ToolInput
					originalToolUseID = e.ToolUseID
					return true
				}
			}
			shouldStop := sw.HandleEvent(e)
			if sw.WriteErr() {
				streamErr = true
				return true
			}
			if !shouldStop {
				return false
			}
			if sw.LocalStop() {
				localStop = true
				return true
			}
			streamErr = true
			return true
		})
		_ = apiResp.Body.Close()

		if err != nil && !foundServerTool {
			slog.ErrorContext(ctx, "stream error", "trace_id", short, "round", round+1, "err", err)
			writeStreamingOrJSONError(gw, sw, w, http.StatusBadGateway, errTypeAPI, "upstream stream error")
			return ""
		}

		if !foundServerTool {
			if !streamErr && !localStop {
				sw.Finish()
			}
			if !streamErr && !localStop && sw.IsEmptyVisibleEndTurn() && !gw.IsPromoted() {
				gw.Discard()
				slog.WarnContext(ctx, "empty visible end_turn detected in server tool", "trace_id", short)
				return retryReasonEmptyVisibleEndTurn
			}
			if streamErr && !gw.IsPromoted() {
				gw.Discard()
				httpx.WriteError(w, http.StatusBadGateway, errTypeAPI, "upstream stream error")
			}
			if !streamErr {
				inputTokens, outputTokens := sw.Usage()
				logResponseStats(ctx, short, inputTokens+cumulativeInputTokens, outputTokens+cumulativeOutputTokens, sw.HasContextUsage(), sw.ContextUsagePercentage(), o.contextWindowSize)
			}
			return ""
		}

		// Server tool detected — execute silently. Do NOT emit server_tool_use
		// to the client; Claude Code treats WebSearch/WebFetch as regular tools
		// and would try to execute them itself. Instead we append the result
		// to the conversation and let Kiro generate the final answer.
		inputJSON, resultJSON, execErr := o.executeServerTool(ctx, short, round, serverToolName, serverToolInput)
		if execErr != nil {
			slog.WarnContext(ctx, "server tool execution failed", "trace_id", short, "tool", serverToolName, "err", execErr)
		}

		msgs = o.appendServerToolMessages(msgs, originalToolUseID, serverToolName, inputJSON, resultJSON, execErr, nameMap)
	}

	slog.WarnContext(ctx, "server tool max rounds reached", "trace_id", short, "max_rounds", maxServerToolRounds)
	sw.Finish()
	inputTokens, outputTokens := sw.Usage()
	logResponseStats(ctx, short, inputTokens+cumulativeInputTokens, outputTokens+cumulativeOutputTokens, sw.HasContextUsage(), sw.ContextUsagePercentage(), o.contextWindowSize)
	return ""
}

func (o *serverToolOrchestrator) handleNonStreaming(ctx context.Context, w http.ResponseWriter) string {
	_, short := logging.TraceIDs(ctx)

	msgs := slices.Clone(o.req.Messages)
	var orderedBlocks []any
	var totalInputTokens, totalOutputTokens int
	var lastStopReason string
	var lastStopSequence any
	var normalExit bool

	for round := range maxServerToolRounds {
		payload, nameMap, err := o.buildPayload(msgs)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, errTypeInvalidRequest, err.Error())
			return ""
		}

		apiResp, err := o.service.client.GenerateAssistantResponse(ctx, o.creds.AccessToken, payload, o.creds.Region)
		if err != nil {
			logUpstreamError(ctx, short, err, "round", round+1)
			httpx.WriteError(w, http.StatusBadGateway, errTypeAPI, "upstream API error")
			return ""
		}

		acc := respconv.NewNonStreamingAccumulator(o.contextWindowSize, o.req.StopSequences, o.req.MaxTokens, 0)
		acc.SetToolNameMap(nameMap.ReverseMap())

		var hasError, foundServerTool bool
		var nsServerToolName, nsServerToolInput, nsOriginalToolUseID string

		err = kiroproto.ParseStream(ctx, apiResp.Body, func(e kiroproto.Event) bool {
			d := acc.ProcessEvent(e)
			if d.IsError {
				hasError = true
				return true
			}
			if d.ToolStop {
				toolName := d.ToolName
				if o.stCtx.IsServerTool(toolName) {
					foundServerTool = true
					nsServerToolName = toolName
					nsServerToolInput = d.ToolInput
					nsOriginalToolUseID = d.ToolUseID
				}
			}
			return false
		})
		_ = apiResp.Body.Close()

		if (err != nil || hasError) && !foundServerTool {
			httpx.WriteError(w, http.StatusBadGateway, errTypeAPI, "upstream error")
			return ""
		}

		resp, stats := acc.BuildResponse(o.responseModel)
		totalInputTokens += stats.InputTokens
		totalOutputTokens += stats.OutputTokens
		lastStopReason, _ = resp["stop_reason"].(string)
		lastStopSequence = resp["stop_sequence"]

		content, _ := resp["content"].([]any)
		orderedBlocks = append(orderedBlocks, content...)

		if !foundServerTool {
			if acc.IsEmptyVisibleEndTurn() {
				slog.WarnContext(ctx, "empty visible end_turn detected in server tool", "trace_id", short)
				return retryReasonEmptyVisibleEndTurn
			}
			normalExit = true
			break
		}

		inputJSON, resultJSON, execErr := o.executeServerTool(ctx, short, round, nsServerToolName, nsServerToolInput)
		if execErr != nil {
			slog.WarnContext(ctx, "server tool execution failed", "trace_id", short, "tool", nsServerToolName, "err", execErr)
		}

		msgs = o.appendServerToolMessages(msgs, nsOriginalToolUseID, nsServerToolName, inputJSON, resultJSON, execErr, nameMap)
	}

	if !normalExit {
		slog.WarnContext(ctx, "server tool max rounds reached", "trace_id", short, "max_rounds", maxServerToolRounds)
	}

	finalResp := map[string]any{
		"id":            "msg_" + uuid.New().String()[:24],
		"type":          "message",
		"role":          "assistant",
		"content":       orderedBlocks,
		"model":         o.responseModel,
		"stop_reason":   lastStopReason,
		"stop_sequence": lastStopSequence,
		"usage": map[string]any{
			"input_tokens":                totalInputTokens,
			"output_tokens":               totalOutputTokens,
			"cache_read_input_tokens":     0,
			"cache_creation_input_tokens": 0,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.MarshalWrite(w, finalResp); err != nil {
		slog.ErrorContext(ctx, "write non-streaming response failed", "err", err)
	}
	_, _ = w.Write([]byte("\n"))

	logResponseStats(ctx, short, totalInputTokens, totalOutputTokens, false, 0, o.contextWindowSize)
	return ""
}

func (o *serverToolOrchestrator) executeServerTool(ctx context.Context, short string, round int, toolName, toolInput string) (inputJSON string, resultJSON string, err error) {
	slog.InfoContext(ctx, "server tool intercepted",
		"trace_id", short, "round", round+1, "tool", toolName,
	)

	tool := o.stCtx.ServerTools[toolName]

	if servertool.IsWebSearch(tool) {
		return o.executeWebSearch(ctx, toolInput)
	}
	if servertool.IsWebFetch(tool) {
		return o.executeWebFetch(ctx, toolInput)
	}
	return toolInput, "", fmt.Errorf("unsupported server tool: %s", toolName)
}

func (o *serverToolOrchestrator) executeWebSearch(ctx context.Context, toolInput string) (string, string, error) {
	var input struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(toolInput), &input); err != nil {
		return toolInput, "", fmt.Errorf("invalid web_search input: %w", err)
	}

	inputJSON, _ := json.Marshal(map[string]any{"query": input.Query})

	results, err := websearch.Search(ctx, input.Query, 5)
	if err != nil {
		return string(inputJSON), "", err
	}

	// Build search results in Anthropic web_search_tool_result format.
	var searchResults []map[string]any
	for _, r := range results {
		searchResults = append(searchResults, map[string]any{
			"type":        "web_search_result",
			"url":         r.URL,
			"title":       r.Title,
			"description": r.Snippet,
		})
	}
	resultJSON, _ := json.Marshal(searchResults)
	return string(inputJSON), string(resultJSON), nil
}

func (o *serverToolOrchestrator) executeWebFetch(ctx context.Context, toolInput string) (string, string, error) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(toolInput), &input); err != nil {
		return toolInput, "", fmt.Errorf("invalid web_fetch input: %w", err)
	}

	inputJSON, _ := json.Marshal(map[string]any{"url": input.URL})

	content, err := webfetch.Fetch(ctx, input.URL)
	if err != nil {
		return string(inputJSON), "", err
	}

	resultJSON, _ := json.Marshal([]map[string]any{
		{"type": "text", "text": content},
	})
	return string(inputJSON), string(resultJSON), nil
}

func (o *serverToolOrchestrator) appendServerToolMessages(msgs []anthropic.Message, toolUseID, toolName, inputJSON, resultJSON string, execErr error, nameMap *reqconv.ToolNameMap) []anthropic.Message {
	var inputMap map[string]any
	_ = json.Unmarshal([]byte(inputJSON), &inputMap)

	var resultContent anthropic.MessageContent
	var isError bool
	if execErr != nil {
		isError = true
		resultContent = anthropic.MessageContent{Text: "tool error: " + execErr.Error()}
	} else {
		resultContent = anthropic.MessageContent{Text: resultJSON}
	}

	// Use the original tool name Kiro saw (shortened if applicable), so the
	// assistant tool_use entry in history matches the schema Kiro has for tools.
	kiroName := toolName
	if nameMap != nil {
		kiroName = nameMap.Shorten(toolName)
	}

	return append(msgs,
		anthropic.Message{
			Role: "assistant",
			Content: anthropic.MessageContent{Blocks: []anthropic.ContentBlock{
				{Type: anthropic.BlockTypeToolUse, ID: toolUseID, Name: kiroName, Input: inputMap},
			}},
		},
		anthropic.Message{
			Role: "user",
			Content: anthropic.MessageContent{Blocks: []anthropic.ContentBlock{
				{Type: anthropic.BlockTypeToolResult, ToolUseID: toolUseID, Content: resultContent, IsError: isError},
			}},
		},
	)
}

func (o *serverToolOrchestrator) buildPayload(msgs []anthropic.Message) (*kiroproto.Payload, *reqconv.ToolNameMap, error) {
	tmpReq := *o.req
	tmpReq.Messages = msgs
	return reqconv.BuildPayload(&tmpReq, o.buildOpts)
}
