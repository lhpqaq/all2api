package zed

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/lhpqaq/all2api/internal/core"
)

func (z *zedUpstream) handleNonStream(body io.ReadCloser) (core.CoreResult, error) {
	return z.processStreamResponse(context.Background(), nil, body)
}

func (z *zedUpstream) handleStream(ctx context.Context, req core.CoreRequest, body io.ReadCloser) (core.CoreResult, error) {
	return z.processStreamResponse(ctx, req.StreamChannel, body)
}

func (z *zedUpstream) processStreamResponse(ctx context.Context, streamCh chan core.StreamEvent, body io.ReadCloser) (core.CoreResult, error) {
	var fullText strings.Builder
	var fullThinking strings.Builder

	// Tool parsing state
	var currentToolCall *core.ToolCall
	var currentToolInput strings.Builder
	var toolCalls []core.ToolCall

	scanner := bufio.NewScanner(body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			line = strings.TrimPrefix(line, "data: ")
			if line == "[DONE]" {
				continue
			}
		}

		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}

		if event, ok := obj["event"].(map[string]any); ok {
			obj = event
		}

		textDelta := ""
		thinkingDelta := ""

		// 1. Anthropic format
		if typeVal, ok := obj["type"].(string); ok {
			if typeVal == "content_block_start" {
				if cb, ok := obj["content_block"].(map[string]any); ok {
					if cb["type"] == "tool_use" {
						tc := core.ToolCall{}
						if id, ok := cb["id"].(string); ok {
							tc.ID = id
						}
						if name, ok := cb["name"].(string); ok {
							tc.Name = name
						}
						currentToolCall = &tc
						currentToolInput.Reset()
					}
				}
			} else if typeVal == "content_block_delta" {
				if delta, ok := obj["delta"].(map[string]any); ok {
					if delta["type"] == "text_delta" {
						if dText, ok := delta["text"].(string); ok {
							textDelta = dText
						}
					} else if delta["type"] == "thinking_delta" {
						if dThink, ok := delta["thinking"].(string); ok {
							thinkingDelta = dThink
						}
					} else if delta["type"] == "input_json_delta" {
						if pj, ok := delta["partial_json"].(string); ok {
							if currentToolCall != nil {
								currentToolInput.WriteString(pj)
							}
						}
					}
				}
			} else if typeVal == "content_block_stop" {
				if currentToolCall != nil {
					args := map[string]any{}
					_ = json.Unmarshal([]byte(currentToolInput.String()), &args)
					currentToolCall.Args = args
					toolCalls = append(toolCalls, *currentToolCall)
					currentToolCall = nil
				}
			} else if typeVal == "message_stop" {
				// done
			}
		}

		// 2. OpenAI format
		if choicesAny, ok := obj["choices"].([]any); ok && len(choicesAny) > 0 {
			if choice, ok := choicesAny[0].(map[string]any); ok {
				if delta, ok := choice["delta"].(map[string]any); ok {
					if content, ok := delta["content"].(string); ok {
						textDelta = content
					}
					if reasoning, ok := delta["reasoning_content"].(string); ok {
						thinkingDelta = reasoning
					}

					if tcs, ok := delta["tool_calls"].([]any); ok && len(tcs) > 0 {
						for _, tcAny := range tcs {
							if tcObj, ok := tcAny.(map[string]any); ok {
								// To be thorough, we would reconstruct tool_calls by index
								// But for Zed, it focuses on mapping tool calls via Anthropic natively inside `zed2api`
								// However, if raw OpenAI is passed down...
								_ = tcObj
							}
						}
					}
				}
				if message, ok := choice["message"].(map[string]any); ok {
					if content, ok := message["content"].(string); ok {
						textDelta = content
					}
					if reasoning, ok := message["reasoning_content"].(string); ok {
						thinkingDelta = reasoning
					}
					if tcs, ok := message["tool_calls"].([]any); ok && len(tcs) > 0 {
						for _, tcAny := range tcs {
							if tcObj, ok := tcAny.(map[string]any); ok {
								tc := core.ToolCall{}
								if id, ok := tcObj["id"].(string); ok {
									tc.ID = id
								}
								if funcObj, ok := tcObj["function"].(map[string]any); ok {
									if name, ok := funcObj["name"].(string); ok {
										tc.Name = name
									}
									if arguments, ok := funcObj["arguments"].(string); ok {
										args := map[string]any{}
										_ = json.Unmarshal([]byte(arguments), &args)
										tc.Args = args
									}
								}
								toolCalls = append(toolCalls, tc)
							}
						}
					}
				}
			}
		}

		// 3. Gemini / Google format
		if candidatesAny, ok := obj["candidates"].([]any); ok && len(candidatesAny) > 0 {
			if cand, ok := candidatesAny[0].(map[string]any); ok {
				if content, ok := cand["content"].(map[string]any); ok {
					if partsAny, ok := content["parts"].([]any); ok {
						for _, part := range partsAny {
							if pMap, ok := part.(map[string]any); ok {
								if t, ok := pMap["text"].(string); ok {
									textDelta += t
								}
							}
						}
					}
				}
			}
		}

		if textDelta != "" {
			fullText.WriteString(textDelta)
		}
		if thinkingDelta != "" {
			fullThinking.WriteString(thinkingDelta)
		}

		if streamCh != nil {
			if textDelta != "" || thinkingDelta != "" {
				streamCh <- core.StreamEvent{TextDelta: textDelta, ThinkingDelta: thinkingDelta}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if streamCh != nil {
			streamCh <- core.StreamEvent{Error: err}
			close(streamCh)
		}
		return core.CoreResult{}, err
	}

	if streamCh != nil {
		streamCh <- core.StreamEvent{Done: true}
		close(streamCh)
	}

	return core.CoreResult{
		Text:      fullText.String(),
		Thinking:  fullThinking.String(),
		ToolCalls: toolCalls,
	}, nil
}
