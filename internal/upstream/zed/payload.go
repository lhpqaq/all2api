package zed

import (
	"github.com/google/uuid"
	"github.com/lhpqaq/all2api/internal/core"
)

func (z *zedUpstream) buildPayload(req core.CoreRequest) (*zedPayload, error) {
	provider := getProvider(req.Model)

	provReq := zedProviderRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		System:      req.System,
		Stream:      req.Stream,
	}

	if provReq.MaxTokens == 0 {
		provReq.MaxTokens = 8192
	}

	if req.Thinking {
		provReq.Thinking = map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": 8192,
		}
	}

	if len(req.Tools) > 0 {
		for _, t := range req.Tools {
			provReq.Tools = append(provReq.Tools, map[string]interface{}{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.InputSchema,
			})
		}
	}
	if req.ToolChoice.Mode != "" {
		if req.ToolChoice.Mode == "auto" {
			provReq.ToolChoice = map[string]string{"type": "auto"}
		} else if req.ToolChoice.Mode == "any" || req.ToolChoice.Mode == "required" {
			provReq.ToolChoice = map[string]string{"type": "any"}
		} else if req.ToolChoice.Mode == "tool" {
			provReq.ToolChoice = map[string]interface{}{
				"type": "tool",
				"name": req.ToolChoice.Name,
			}
		}
	}

	for _, m := range req.Messages {
		if m.Role == "system" {
			if provReq.System != "" {
				provReq.System += "\n\n" + m.Content
			} else {
				provReq.System = m.Content
			}
			continue
		}

		var content any
		role := m.Role

		if m.ToolCallID != "" || role == "tool" {
			role = "user"
			content = []map[string]interface{}{
				{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":     m.Content,
					"is_error":    false,
				},
			}
		} else if len(m.ToolCalls) > 0 {
			var block []map[string]interface{}
			if m.Content != "" {
				block = append(block, map[string]interface{}{
					"type": "text",
					"text": m.Content,
				})
			}
			for _, tc := range m.ToolCalls {
				block = append(block, map[string]interface{}{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": tc.Args,
				})
			}
			content = block
		} else {
			content = []map[string]interface{}{
				{
					"type": "text",
					"text": m.Content,
				},
			}
		}

		msgObj := zedMessage{
			Role:    role,
			Content: content,
		}

		// Merge consecutive messages of the same role
		if len(provReq.Messages) > 0 && provReq.Messages[len(provReq.Messages)-1].Role == role {
			lastIdx := len(provReq.Messages) - 1
			lastContent := provReq.Messages[lastIdx].Content

			var merged []map[string]interface{}
			if lcArr, ok := lastContent.([]map[string]interface{}); ok {
				merged = append(merged, lcArr...)
			}
			if cArr, ok := content.([]map[string]interface{}); ok {
				merged = append(merged, cArr...)
			}
			provReq.Messages[lastIdx].Content = merged
		} else {
			provReq.Messages = append(provReq.Messages, msgObj)
		}
	}

	p := &zedPayload{
		ThreadID:        uuid.New().String(),
		PromptID:        uuid.New().String(),
		Intent:          "user_prompt",
		Provider:        provider,
		Model:           req.Model,
		ProviderRequest: provReq,
	}

	return p, nil
}
