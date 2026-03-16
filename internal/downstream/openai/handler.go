package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
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
	mux.HandleFunc("/v1/chat/completions", h.handleChat)
	mux.HandleFunc("/chat/completions", h.handleChat)
	mux.HandleFunc("/v1/responses", h.handleResponses)
	mux.HandleFunc("/responses", h.handleResponses)
	mux.HandleFunc("/v1/models", h.handleModels)
}

type chatReq struct {
	Model        string     `json:"model"`
	Messages     []chatMsg  `json:"messages"`
	Stream       bool       `json:"stream"`
	MaxTokens    int        `json:"max_tokens"`
	Temperature  *float64   `json:"temperature"`
	Tools        []toolWrap `json:"tools"`
	ToolChoice   any        `json:"tool_choice"`
	FunctionCall any        `json:"function_call"`
}

type chatMsg struct {
	Role       string            `json:"role"`
	Content    any               `json:"content"`
	ToolCallID string            `json:"tool_call_id"`
	ToolCalls  []inboundToolCall `json:"tool_calls"`
}

type inboundToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type toolWrap struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

func (h *Handler) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx := withDiagContext(r, h.cfg)
	w.Header().Set("X-Request-Id", diag.RequestID(ctx))

	var body chatReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if diag.Debug(ctx) {
		if _, ok := body.ToolChoice.(string); ok {
			if s, ok2 := body.ToolChoice.(string); ok2 {
				log.Printf("[all2api] req_id=%s phase=downstream.openai tool_choice_str=%q", diag.RequestID(ctx), s)
			}
		}
		if _, ok := body.FunctionCall.(string); ok {
			if s, ok2 := body.FunctionCall.(string); ok2 {
				log.Printf("[all2api] req_id=%s phase=downstream.openai function_call_str=%q", diag.RequestID(ctx), s)
			}
		}
		if body.FunctionCall != nil {
			log.Printf("[all2api] req_id=%s phase=downstream.openai function_call_type=%T", diag.RequestID(ctx), body.FunctionCall)
		}
		ip := requestIP(r)
		toolNames := make([]string, 0, len(body.Tools))
		emptyToolNames := 0
		for _, t := range body.Tools {
			name := strings.TrimSpace(t.Function.Name)
			if name == "" {
				emptyToolNames++
				continue
			}
			toolNames = append(toolNames, name)
			if len(toolNames) >= 12 {
				break
			}
		}
		log.Printf("[all2api] req_id=%s phase=downstream.openai request_ip=%s model=%s stream=%t messages=%d tools=%d empty_tool_names=%d tool_names=%q tool_choice=%T",
			diag.RequestID(ctx), ip, body.Model, body.Stream, len(body.Messages), len(body.Tools), emptyToolNames, toolNames, body.ToolChoice,
		)
	}

	upstreamName := "auto"
	if v := r.Header.Get(h.cfg.Routing.UpstreamHeader); v != "" {
		upstreamName = v
	}

	modelName := body.Model
	thinkingRequested := false
	if strings.HasSuffix(modelName, "-thinking") {
		thinkingRequested = true
		modelName = strings.TrimSuffix(modelName, "-thinking")
	}

	req := core.CoreRequest{
		Endpoint:    core.EndpointOpenAIChat,
		Upstream:    upstreamName,
		Model:       modelName,
		Thinking:    thinkingRequested,
		Stream:      body.Stream,
		System:      "",
		Messages:    extractMessages(body.Messages),
		Tools:       extractTools(body.Tools),
		ToolChoice:  mergeToolChoice(body.ToolChoice, body.FunctionCall),
		MaxTokens:   body.MaxTokens,
		Temperature: body.Temperature,
	}

	if body.Stream && len(req.Tools) == 0 {
		req.StreamChannel = make(chan core.StreamEvent, 100)
		go func() {
			_, err := h.orch.Execute(ctx, req)
			if err != nil {
				req.StreamChannel <- core.StreamEvent{Error: err}
			}
		}()
		writeChatRealStream(w, req.Model, req.StreamChannel)
		return
	}

	result, err := h.orch.Execute(ctx, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if body.Stream {
		writeChatStream(w, req.Model, result)
		return
	}
	writeChatNonStream(w, req.Model, result)
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

func requestIP(r *http.Request) string {
	ff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if ff != "" {
		parts := strings.Split(ff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (h *Handler) handleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx := withDiagContext(r, h.cfg)
	w.Header().Set("X-Request-Id", diag.RequestID(ctx))

	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if diag.Debug(ctx) {
		typeName := func(v any) string {
			if v == nil {
				return "<nil>"
			}
			return fmt.Sprintf("%T", v)
		}
		toolsVal, hasTools := raw["tools"]
		insVal, hasIns := raw["instructions"]
		log.Printf("[all2api] req_id=%s phase=downstream.openai.responses raw_tools_present=%t raw_tools_type=%s raw_instructions_present=%t raw_instructions_type=%s",
			diag.RequestID(ctx), hasTools, typeName(toolsVal), hasIns, typeName(insVal),
		)
	}
	chat := responsesToChat(raw)
	b, _ := json.Marshal(chat)
	r.Body = io.NopCloser(bytes.NewReader(b))
	r.ContentLength = int64(len(b))
	r = r.WithContext(ctx)
	h.handleChat(w, r)
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	data := make([]any, 0)
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		data = append(data, map[string]any{
			"id":       id,
			"object":   "model",
			"created":  0,
			"owned_by": "all2api",
		})
	}

	for upName, info := range h.orch.GetUpstreamModels(r.Context()) {
		for _, m := range info.Models {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			add(m)
			add(upName + "/" + m)
			if info.SupportThinking {
				add(m + "-thinking")
				add(upName + "/" + m + "-thinking")
			}
		}
	}
	if len(data) == 0 {
		add("cursor")
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func extractMessages(msgs []chatMsg) []core.Message {
	out := make([]core.Message, 0, len(msgs))
	for _, m := range msgs {
		coreMsg := core.Message{Role: m.Role, Content: flatten(m.Content), ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			coreMsg.ToolCalls = append(coreMsg.ToolCalls, core.ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: args,
			})
		}
		out = append(out, coreMsg)
	}
	return out
}

func flatten(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if arr, ok := v.([]any); ok {
		sb := ""
		for _, it := range arr {
			if m, ok := it.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "text" {
					if tx, ok := m["text"].(string); ok {
						sb += tx + "\n"
					}
				}
			}
		}
		return sb
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func extractTools(tools []toolWrap) []core.ToolDef {
	out := make([]core.ToolDef, 0, len(tools))
	for _, t := range tools {
		out = append(out, core.ToolDef{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	return out
}

func extractToolChoice(v any) core.ToolChoice {
	if v == nil {
		return core.ToolChoice{Mode: "auto"}
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" || s == "auto" {
			return core.ToolChoice{Mode: "auto"}
		}
		if s == "required" {
			return core.ToolChoice{Mode: "any"}
		}
		if s == "any" {
			return core.ToolChoice{Mode: "any"}
		}
		if s == "none" {
			return core.ToolChoice{Mode: "auto"}
		}
		return core.ToolChoice{Mode: "tool", Name: s}
	}
	if m, ok := v.(map[string]any); ok {
		if t, ok := m["type"].(string); ok {
			t = strings.TrimSpace(t)
			switch t {
			case "function":
				if fn, ok := m["function"].(map[string]any); ok {
					if n, ok := fn["name"].(string); ok {
						n = strings.TrimSpace(n)
						if n != "" {
							return core.ToolChoice{Mode: "tool", Name: n}
						}
					}
				}
				if n, ok := m["name"].(string); ok {
					n = strings.TrimSpace(n)
					if n != "" {
						return core.ToolChoice{Mode: "tool", Name: n}
					}
				}
			case "tool":
				if n, ok := m["name"].(string); ok {
					n = strings.TrimSpace(n)
					if n != "" {
						return core.ToolChoice{Mode: "tool", Name: n}
					}
				}
				if fn, ok := m["function"].(map[string]any); ok {
					if n, ok := fn["name"].(string); ok {
						n = strings.TrimSpace(n)
						if n != "" {
							return core.ToolChoice{Mode: "tool", Name: n}
						}
					}
				}
			case "any", "required":
				return core.ToolChoice{Mode: "any"}
			case "auto", "none":
				return core.ToolChoice{Mode: "auto"}
			}
		}
	}
	return core.ToolChoice{Mode: "auto"}
}

func mergeToolChoice(toolChoice any, functionCall any) core.ToolChoice {
	primary := extractToolChoice(toolChoice)
	if primary.Mode != "auto" {
		return primary
	}
	secondary := extractFunctionCallChoice(functionCall)
	if secondary.Mode != "auto" {
		return secondary
	}
	return primary
}

func extractFunctionCallChoice(v any) core.ToolChoice {
	if v == nil {
		return core.ToolChoice{Mode: "auto"}
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" || s == "auto" || s == "none" {
			return core.ToolChoice{Mode: "auto"}
		}
		if s == "required" || s == "any" {
			return core.ToolChoice{Mode: "any"}
		}
		return core.ToolChoice{Mode: "tool", Name: s}
	}
	if m, ok := v.(map[string]any); ok {
		if n, ok := m["name"].(string); ok {
			n = strings.TrimSpace(n)
			if n != "" {
				if n == "auto" || n == "none" {
					return core.ToolChoice{Mode: "auto"}
				}
				return core.ToolChoice{Mode: "tool", Name: n}
			}
		}
		if fn, ok := m["function"].(map[string]any); ok {
			if n, ok := fn["name"].(string); ok {
				n = strings.TrimSpace(n)
				if n != "" {
					return core.ToolChoice{Mode: "tool", Name: n}
				}
			}
		}
	}
	return core.ToolChoice{Mode: "auto"}
}

func writeChatNonStream(w http.ResponseWriter, model string, result core.CoreResult) {
	msg := map[string]any{
		"role":    "assistant",
		"content": result.Text,
	}
	if result.Thinking != "" {
		msg["reasoning_content"] = result.Thinking
	}
	if len(result.ToolCalls) > 0 {
		calls := make([]any, 0, len(result.ToolCalls))
		for _, tc := range result.ToolCalls {
			b, _ := json.Marshal(tc.Args)
			calls = append(calls, map[string]any{
				"id":       tc.ID,
				"type":     "function",
				"function": map[string]any{"name": tc.Name, "arguments": string(b)},
			})
		}
		msg["tool_calls"] = calls
	}
	finish := "stop"
	if len(result.ToolCalls) > 0 {
		finish = "tool_calls"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "chatcmpl_",
		"object":  "chat.completion",
		"created": 0,
		"model":   model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	})
}

func writeChatStream(w http.ResponseWriter, model string, result core.CoreResult) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	fl, _ := w.(http.Flusher)
	write := func(data any) {
		b, _ := json.Marshal(data)
		_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}

	write(map[string]any{
		"id":      "chatcmpl_",
		"object":  "chat.completion.chunk",
		"created": 0,
		"model":   model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": nil}},
	})

	if result.Thinking != "" {
		write(map[string]any{
			"id":      "chatcmpl_",
			"object":  "chat.completion.chunk",
			"created": 0,
			"model":   model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": result.Thinking}, "finish_reason": nil}},
		})
	}

	if result.Text != "" {
		write(map[string]any{
			"id":      "chatcmpl_",
			"object":  "chat.completion.chunk",
			"created": 0,
			"model":   model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": result.Text}, "finish_reason": nil}},
		})
	}
	if len(result.ToolCalls) > 0 {
		const chunkSize = 128
		for i, tc := range result.ToolCalls {
			args, _ := json.Marshal(tc.Args)
			write(map[string]any{
				"id":      "chatcmpl_",
				"object":  "chat.completion.chunk",
				"created": 0,
				"model":   model,
				"choices": []any{map[string]any{
					"index": 0,
					"delta": map[string]any{
						"tool_calls": []any{map[string]any{
							"index":    i,
							"id":       tc.ID,
							"type":     "function",
							"function": map[string]any{"name": tc.Name, "arguments": ""},
						}},
					},
					"finish_reason": nil,
				}},
			})
			for j := 0; j < len(args); j += chunkSize {
				end := j + chunkSize
				if end > len(args) {
					end = len(args)
				}
				write(map[string]any{
					"id":      "chatcmpl_",
					"object":  "chat.completion.chunk",
					"created": 0,
					"model":   model,
					"choices": []any{map[string]any{
						"index": 0,
						"delta": map[string]any{"tool_calls": []any{map[string]any{
							"index":    i,
							"function": map[string]any{"arguments": string(args[j:end])},
						}}},
						"finish_reason": nil,
					}},
				})
			}
		}
	}
	finish := "stop"
	if len(result.ToolCalls) > 0 {
		finish = "tool_calls"
	}
	write(map[string]any{
		"id":      "chatcmpl_",
		"object":  "chat.completion.chunk",
		"created": 0,
		"model":   model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
	})
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	if fl != nil {
		fl.Flush()
	}
}

func responsesToChat(body map[string]any) map[string]any {
	out := map[string]any{}
	out["model"], _ = body["model"].(string)
	if s, ok := body["stream"].(bool); ok {
		out["stream"] = s
	}
	if tc, ok := body["tool_choice"]; ok {
		out["tool_choice"] = tc
	}
	if fc, ok := body["function_call"]; ok {
		out["function_call"] = fc
	}
	if ins, ok := body["instructions"].(string); ok && ins != "" {
		out["messages"] = appendMessage(out["messages"], map[string]any{"role": "system", "content": ins})
	}
	input := body["input"]
	switch v := input.(type) {
	case string:
		out["messages"] = appendMessage(out["messages"], map[string]any{"role": "user", "content": v})
	case []any:
		for _, it := range v {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := m["type"].(string); t == "function_call_output" {
				out["messages"] = appendMessage(out["messages"], map[string]any{
					"role":         "tool",
					"content":      m["output"],
					"tool_call_id": m["call_id"],
				})
				continue
			}
			role, _ := m["role"].(string)
			if role == "developer" {
				role = "system"
			}
			if role == "" {
				role = "user"
			}
			out["messages"] = appendMessage(out["messages"], map[string]any{"role": role, "content": m["content"]})
		}
	}
	if tools, ok := body["tools"].([]any); ok {
		out["tools"] = tools
	}
	return out
}

func appendMessage(existing any, msg map[string]any) []any {
	if existing == nil {
		return []any{msg}
	}
	if arr, ok := existing.([]any); ok {
		return append(arr, msg)
	}
	return []any{msg}
}

func writeChatRealStream(w http.ResponseWriter, model string, streamChan chan core.StreamEvent) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	fl, _ := w.(http.Flusher)
	write := func(data any) {
		b, _ := json.Marshal(data)
		_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}

	write(map[string]any{
		"id":      "chatcmpl_",
		"object":  "chat.completion.chunk",
		"created": 0,
		"model":   model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": nil}},
	})

	for evt := range streamChan {
		if evt.Error != nil {
			break
		}
		if evt.Done {
			break
		}

		delta := map[string]any{}
		if evt.TextDelta != "" {
			delta["content"] = evt.TextDelta
		}
		if evt.ThinkingDelta != "" {
			delta["reasoning_content"] = evt.ThinkingDelta
		}

		if len(delta) > 0 {
			write(map[string]any{
				"id":      "chatcmpl_",
				"object":  "chat.completion.chunk",
				"created": 0,
				"model":   model,
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}},
			})
		}
	}

	write(map[string]any{
		"id":      "chatcmpl_",
		"object":  "chat.completion.chunk",
		"created": 0,
		"model":   model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	})

	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	if fl != nil {
		fl.Flush()
	}
}
