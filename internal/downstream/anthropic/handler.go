package anthropic

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/lhpqaq/all2api/internal/config"
	"github.com/lhpqaq/all2api/internal/core"
	"github.com/lhpqaq/all2api/internal/diag"
	"github.com/lhpqaq/all2api/internal/orchestrator"
)

type Handler struct {
	cfg  config.Config
	orch *orchestrator.Orchestrator
}

func Register(mux *http.ServeMux, cfg config.Config, orch *orchestrator.Orchestrator) {
	h := &Handler{cfg: cfg, orch: orch}
	mux.HandleFunc("/v1/messages", h.handle)
	mux.HandleFunc("/messages", h.handle)
}

type messagesReq struct {
	Model      string         `json:"model"`
	Messages   []message      `json:"messages"`
	MaxTokens  int            `json:"max_tokens"`
	Stream     bool           `json:"stream"`
	System     any            `json:"system"`
	Tools      []toolDef      `json:"tools"`
	ToolChoice map[string]any `json:"tool_choice"`
}

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type messagesResp struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	StopSeq    any            `json:"stop_sequence"`
	Usage      map[string]any `json:"usage"`
}

type contentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
}

func (h *Handler) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx := withDiagContext(r, h.cfg)
	w.Header().Set("X-Request-Id", diag.RequestID(ctx))

	var body messagesReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if diag.Debug(ctx) {
		toolNames := make([]string, 0, len(body.Tools))
		for _, t := range body.Tools {
			name := strings.TrimSpace(t.Name)
			if name == "" {
				continue
			}
			toolNames = append(toolNames, name)
			if len(toolNames) >= 12 {
				break
			}
		}
		log.Printf("[all2api] req_id=%s phase=downstream.anthropic model=%s stream=%t messages=%d tools=%d tool_names=%q tool_choice=%v system_type=%T system_len=%d",
			diag.RequestID(ctx), body.Model, body.Stream, len(body.Messages), len(body.Tools), toolNames, body.ToolChoice, body.System, len(extractSystem(body.System)),
		)
	}

	upstreamName := "auto"
	if v := r.Header.Get(h.cfg.Routing.UpstreamHeader); v != "" {
		upstreamName = v
	}

	req := core.CoreRequest{
		Endpoint:   core.EndpointAnthropicMessages,
		Upstream:   upstreamName,
		Model:      body.Model,
		Stream:     body.Stream,
		System:     extractSystem(body.System),
		Messages:   extractMessages(body.Messages),
		Tools:      extractTools(body.Tools),
		ToolChoice: extractToolChoice(body.ToolChoice),
		MaxTokens:  body.MaxTokens,
	}

	result, err := h.orch.Execute(ctx, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if body.Stream {
		writeStream(w, req.Model, result)
		return
	}

	blocks := make([]contentBlock, 0)
	if result.Text != "" {
		blocks = append(blocks, contentBlock{Type: "text", Text: result.Text})
	}
	for _, tc := range result.ToolCalls {
		blocks = append(blocks, contentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Args})
	}
	stopReason := "end_turn"
	if len(result.ToolCalls) > 0 {
		stopReason = "tool_use"
	}
	resp := messagesResp{
		ID:         "msg_" + "",
		Type:       "message",
		Role:       "assistant",
		Content:    blocks,
		Model:      req.Model,
		StopReason: stopReason,
		StopSeq:    nil,
		Usage:      map[string]any{"input_tokens": 0, "output_tokens": 0},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func withDiagContext(r *http.Request, cfg config.Config) context.Context {
	ctx := r.Context()
	debug := cfg.Logging.Debug || cfg.Tooling.Emulate.Debug || diag.Debug(ctx)
	if v := r.Header.Get("X-All2API-Debug"); v != "" {
		vv := strings.TrimSpace(strings.ToLower(v))
		if vv == "1" || vv == "true" || vv == "yes" {
			debug = true
		}
		if vv == "0" || vv == "false" || vv == "no" {
			debug = false
		}
	}
	ctx = diag.WithDebug(ctx, debug)

	id := strings.TrimSpace(r.Header.Get("X-Request-Id"))
	if id == "" {
		id = strings.TrimSpace(r.Header.Get("X-Correlation-Id"))
	}
	if id == "" {
		id = strings.TrimSpace(diag.RequestID(ctx))
	}
	if id == "" {
		id = diag.NewRequestID()
	}
	ctx = diag.WithRequestID(ctx, id)
	return ctx
}

func extractSystem(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func extractMessages(msgs []message) []core.Message {
	out := make([]core.Message, 0, len(msgs))
	for _, m := range msgs {
		coreMsg, callID, calls := extractContentWithTools(m.Content)
		out = append(out, core.Message{
			Role:       m.Role,
			Content:    coreMsg,
			ToolCallID: callID,
			ToolCalls:  calls,
		})
	}
	return out
}

func extractContentWithTools(v any) (string, string, []core.ToolCall) {
	if v == nil {
		return "", "", nil
	}
	if s, ok := v.(string); ok {
		return s, "", nil
	}
	if arr, ok := v.([]any); ok {
		sb := ""
		var callID string
		var calls []core.ToolCall
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "text" {
					if tx, ok := m["text"].(string); ok {
						sb += tx + "\n"
					}
				} else if t, _ := m["type"].(string); t == "tool_result" {
					// Extract tool_use_id from tool_result
					if id, ok := m["tool_use_id"].(string); ok {
						callID = id
					}
					if c, ok := m["content"].(string); ok {
						sb += c + "\n"
					} else if cArr, ok := m["content"].([]any); ok {
						// in Anthropic, tool_result content can be an array of text blocks
						b, _ := json.Marshal(cArr)
						sb += string(b) + "\n"
					}
				} else if t, _ := m["type"].(string); t == "tool_use" {
					// We are passing an assistant tool_use back
					id, _ := m["id"].(string)
					name, _ := m["name"].(string)
					args, _ := m["input"].(map[string]any)
					calls = append(calls, core.ToolCall{
						ID:   id,
						Name: name,
						Args: args,
					})
				}
			}
		}
		return sb, callID, calls
	}
	b, _ := json.Marshal(v)
	return string(b), "", nil
}

func extractTools(tools []toolDef) []core.ToolDef {
	out := make([]core.ToolDef, 0, len(tools))
	for _, t := range tools {
		out = append(out, core.ToolDef{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return out
}

func extractToolChoice(v map[string]any) core.ToolChoice {
	if v == nil {
		return core.ToolChoice{Mode: "auto"}
	}
	if t, ok := v["type"].(string); ok {
		if t == "any" {
			return core.ToolChoice{Mode: "any"}
		}
		if t == "tool" {
			if n, ok := v["name"].(string); ok {
				return core.ToolChoice{Mode: "tool", Name: n}
			}
			return core.ToolChoice{Mode: "tool"}
		}
	}
	return core.ToolChoice{Mode: "auto"}
}

func writeStream(w http.ResponseWriter, model string, result core.CoreResult) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	fl, _ := w.(http.Flusher)

	writeEvent := func(event string, data any) {
		b, _ := json.Marshal(data)
		_, _ = w.Write([]byte("event: " + event + "\n"))
		_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}

	writeEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            "msg_" + "",
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})

	idx := 0
	if result.Text != "" {
		writeEvent("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         idx,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		writeEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": idx,
			"delta": map[string]any{"type": "text_delta", "text": result.Text},
		})
		writeEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
		idx++
	}
	for _, tc := range result.ToolCalls {
		writeEvent("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         idx,
			"content_block": map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": map[string]any{}},
		})
		inputJSON, _ := json.Marshal(tc.Args)
		const chunkSize = 128
		for i := 0; i < len(inputJSON); i += chunkSize {
			end := i + chunkSize
			if end > len(inputJSON) {
				end = len(inputJSON)
			}
			writeEvent("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": idx,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": string(inputJSON[i:end])},
			})
		}
		writeEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
		idx++
	}

	stopReason := "end_turn"
	if len(result.ToolCalls) > 0 {
		stopReason = "tool_use"
	}
	writeEvent("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 0},
	})
	writeEvent("message_stop", map[string]any{"type": "message_stop"})
}
