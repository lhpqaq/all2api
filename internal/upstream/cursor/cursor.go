package cursor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/lhpqaq/all2api/internal/config"
	"github.com/lhpqaq/all2api/internal/core"
	"github.com/lhpqaq/all2api/internal/diag"
	emulatePkg "github.com/lhpqaq/all2api/internal/tooling/emulate"
	"github.com/lhpqaq/all2api/internal/upstream"
)

type cursorUpstream struct {
	baseURL string
	client  *http.Client
	headers map[string]string
	timeout time.Duration
	models  []string
}

func (c *cursorUpstream) ListModels(ctx context.Context) ([]string, error) {
	_ = ctx
	if len(c.models) == 0 {
		return []string{"cursor"}, nil
	}
	out := make([]string, 0, len(c.models)+1)
	for _, m := range c.models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		out = append(out, m)
	}
	out = append(out, "cursor")
	return out, nil
}

func (c *cursorUpstream) HasModel(ctx context.Context, model string) (bool, error) {
	_ = ctx
	model = strings.TrimSpace(model)
	if model == "" {
		return false, nil
	}
	if model == "cursor" {
		return true, nil
	}
	for _, m := range c.models {
		if strings.TrimSpace(m) == model {
			return true, nil
		}
	}
	return false, nil
}

type cursorChatRequest struct {
	Model     string          `json:"model"`
	ID        string          `json:"id"`
	Messages  []cursorMessage `json:"messages"`
	Trigger   string          `json:"trigger"`
	MaxTokens int             `json:"max_tokens"`
}

type cursorMessage struct {
	Role  string       `json:"role"`
	ID    string       `json:"id"`
	Parts []cursorPart `json:"parts"`
}

type cursorPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type cursorSSE struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

func New(name string, ucfg config.UpstreamConf) (upstream.Upstream, upstream.Capabilities, error) {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if ucfg.Proxy != "" {
		pu, err := url.Parse(ucfg.Proxy)
		if err != nil {
			return nil, upstream.Capabilities{}, fmt.Errorf("invalid proxy url: %w", err)
		}
		tr.Proxy = http.ProxyURL(pu)
	}
	client := &http.Client{Transport: tr, Timeout: 0}

	headers := map[string]string{}
	for k, v := range ucfg.Headers {
		headers[k] = v
	}

	headers["Content-Type"] = "application/json"
	if _, ok := headers["origin"]; !ok {
		headers["origin"] = "https://cursor.com"
	}
	if _, ok := headers["referer"]; !ok {
		headers["referer"] = "https://cursor.com/"
	}
	if _, ok := headers["user-agent"]; !ok {
		headers["user-agent"] = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	}
	if _, ok := headers["accept-language"]; !ok {
		headers["accept-language"] = "zh-CN,zh;q=0.9,en;q=0.8"
	}
	if _, ok := headers["sec-ch-ua-platform"]; !ok {
		headers["sec-ch-ua-platform"] = "\"Windows\""
	}
	if _, ok := headers["sec-ch-ua"]; !ok {
		headers["sec-ch-ua"] = "\"Chromium\";v=\"140\", \"Not=A?Brand\";v=\"24\", \"Google Chrome\";v=\"140\""
	}
	if _, ok := headers["sec-ch-ua-bitness"]; !ok {
		headers["sec-ch-ua-bitness"] = "\"64\""
	}
	if _, ok := headers["sec-ch-ua-mobile"]; !ok {
		headers["sec-ch-ua-mobile"] = "?0"
	}
	if _, ok := headers["sec-ch-ua-arch"]; !ok {
		headers["sec-ch-ua-arch"] = "\"x86\""
	}
	if _, ok := headers["sec-ch-ua-platform-version"]; !ok {
		headers["sec-ch-ua-platform-version"] = "\"19.0.0\""
	}
	if _, ok := headers["sec-fetch-site"]; !ok {
		headers["sec-fetch-site"] = "same-origin"
	}
	if _, ok := headers["sec-fetch-mode"]; !ok {
		headers["sec-fetch-mode"] = "cors"
	}
	if _, ok := headers["sec-fetch-dest"]; !ok {
		headers["sec-fetch-dest"] = "empty"
	}
	if _, ok := headers["priority"]; !ok {
		headers["priority"] = "u=1, i"
	}
	if _, ok := headers["anthropic-beta"]; !ok {
		headers["anthropic-beta"] = "max-tokens-3-5-sonnet-2024-07-15"
	}
	if _, ok := headers["x-is-human"]; !ok {
		headers["x-is-human"] = ""
	}
	if _, ok := headers["x-path"]; !ok {
		headers["x-path"] = "/api/chat"
	}
	if _, ok := headers["x-method"]; !ok {
		headers["x-method"] = "POST"
	}
	if err := applyAuth(headers, ucfg.Auth); err != nil {
		return nil, upstream.Capabilities{}, err
	}

	base := strings.TrimRight(ucfg.BaseURL, "/")
	if base == "" {
		base = "https://cursor.com"
	}

	cap := upstream.Capabilities{NativeToolCalls: false, SupportThinking: true}
	if ucfg.Capabilities.NativeToolCalls != nil {
		cap.NativeToolCalls = *ucfg.Capabilities.NativeToolCalls
	}

	return &cursorUpstream{
		baseURL: base,
		client:  client,
		headers: headers,
		timeout: ucfg.Timeout.Duration,
		models:  append([]string{}, ucfg.Models...),
	}, cap, nil
}

func (c *cursorUpstream) Do(ctx context.Context, req core.CoreRequest) (core.CoreResult, error) {
	debug := diag.Debug(ctx)

	// Inject Thinking Hint if requested and not in tool mode
	if req.Thinking && len(req.Tools) == 0 {
		if req.System == "" {
			req.System = emulatePkg.ThinkingHint
		} else {
			req.System += "\n\n" + emulatePkg.ThinkingHint
		}
	}

	cursorReq := cursorChatRequest{
		Model:     req.Model,
		ID:        "req_" + randID(16),
		Trigger:   "submit-message",
		MaxTokens: req.MaxTokens,
		Messages:  nil,
	}
	if cursorReq.MaxTokens <= 0 {
		cursorReq.MaxTokens = 8192
	}
	if cursorReq.MaxTokens > 200000 {
		cursorReq.MaxTokens = 200000
	}

	msgs, injected := buildCursorMessages(req)

	fsm := &streamExtractFSM{}
	var completeOut strings.Builder
	maxContinuations := 5
	var lastStreamErr error

	for attempt := 0; attempt <= maxContinuations; attempt++ {
		cursorReq.ID = "req_" + randID(16)
		cursorReq.Messages = msgs

		if debug {
			log.Printf("[all2api] req_id=%s phase=upstream.cursor.build attempt=%d cursor_req_id=%s model=%s max_tokens=%d messages=%d system_present=%t system_injected=%t",
				diag.RequestID(ctx), attempt, cursorReq.ID, cursorReq.Model, cursorReq.MaxTokens, len(cursorReq.Messages), strings.TrimSpace(req.System) != "", injected,
			)
		}

		body, err := json.Marshal(cursorReq)
		if err != nil {
			return core.CoreResult{}, err
		}

		chatURL := c.baseURL + "/api/chat"
		ctxReq, cancelReq := context.WithCancel(ctx)
		defer cancelReq() // Ensure it's cancelled eventually
		httpReq, err := http.NewRequestWithContext(ctxReq, http.MethodPost, chatURL, bytes.NewReader(body))
		if err != nil {
			return core.CoreResult{}, err
		}
		for k, v := range c.headers {
			httpReq.Header.Set(k, v)
		}

		resp, err := c.client.Do(httpReq)
		if err != nil {
			return core.CoreResult{}, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			cancelReq()
			return core.CoreResult{}, fmt.Errorf("cursor upstream http %d: %s", resp.StatusCode, string(b))
		}

		idle := c.timeout
		if idle <= 0 {
			idle = 120 * time.Second
		}

		var iterOut strings.Builder
		reader := bufio.NewReader(resp.Body)
		var streamErr error

		errCh := make(chan error, 1)
		lineCh := make(chan string)
		go func() {
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					errCh <- err
					return
				}
				lineCh <- line
			}
		}()

		timer := time.NewTimer(idle)
	readLoop:
		for {
			select {
			case <-timer.C:
				cancelReq()
				streamErr = fmt.Errorf("cursor upstream idle timeout after %s", idle)
				break readLoop
			case err := <-errCh:
				if err != io.EOF && !errors.Is(err, context.Canceled) {
					streamErr = err
				}
				break readLoop
			case line := <-lineCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idle)

				if strings.HasPrefix(line, "data: ") {
					payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
					if payload == "" {
						continue
					}
					var evt cursorSSE
					if err := json.Unmarshal([]byte(payload), &evt); err != nil {
						continue
					}
					if evt.Type == "text-delta" {
						iterOut.WriteString(evt.Delta)
						completeOut.WriteString(evt.Delta)
						textOut, thinkingOut := fsm.Process(evt.Delta)
						if req.StreamChannel != nil {
							if textOut != "" || thinkingOut != "" {
								req.StreamChannel <- core.StreamEvent{TextDelta: textOut, ThinkingDelta: thinkingOut}
							}
						}
					}
				}
			}
		}
		resp.Body.Close()
		cancelReq()

		if streamErr != nil {
			lastStreamErr = streamErr
			// Do not break immediately; evaluate continuation first.
		}

		suffix := iterOut.String()
		if len(suffix) > 80 {
			suffix = suffix[len(suffix)-80:]
		}

		isTruncated := fsm.inThinking || lastStreamErr != nil || iterOut.Len() >= 4000

		// Check if we need to auto-continue (e.g. truncated during thought process)
		if attempt < maxContinuations && isTruncated && iterOut.Len() > 0 {
			if debug {
				log.Printf("[all2api] req_id=%s attempt=%d cutoff detected, injecting assistant prefill and continuing", diag.RequestID(ctx), attempt)
			}
			// Append the partial thinking to the prompt as assistant to force continue
			if len(msgs) > 0 && msgs[len(msgs)-1].Role == "assistant" {
				msgs[len(msgs)-1].Parts[0].Text += iterOut.String()
			} else {
				msgs = append(msgs, cursorMessage{
					Role:  "assistant",
					ID:    "m_" + randID(12),
					Parts: []cursorPart{{Type: "text", Text: iterOut.String()}},
				})
			}

			// Create continuation prompt based on whether we were inside a thinking block
			var continuationPrompt string
			if fsm.inThinking {
				// Close the thinking tag so the upstream model doesn't complain about invalid XML
				if len(msgs) > 0 && msgs[len(msgs)-1].Role == "assistant" {
					msgs[len(msgs)-1].Parts[0].Text += "\n</thinking>"
				}
				continuationPrompt = "System Notice: The connection dropped due to network limits. Your last output inside the <thinking> block ended with: `" + suffix + "`\n\nPlease continue your internal reasoning EXACTLY from the next character. IMPORTANT: You MUST start your response DIRECTLY with a new `<thinking>` tag, and then seamlessly continue your thought. Do NOT output any introductory text, no \"Sure\", and no apologies. The very first characters of your response MUST be `<thinking>`."
				// Reset FSM so it expects the new <thinking> tag and consumes it gracefully, hiding it from the downstream
				fsm.inThinking = false
			} else {
				continuationPrompt = "System Notice: The connection dropped due to network limits. The last output received was: `" + suffix + "`\n\nPlease continue EXACTLY from the next character. Do NOT repeat the ending text. Do NOT apologize."
			}

			// Cursor's backend API ignores generation if the last message isn't from the user.
			// So we inject a user message explicitly requesting continuation.
			msgs = append(msgs, cursorMessage{
				Role:  "user",
				ID:    "m_" + randID(12),
				Parts: []cursorPart{{Type: "text", Text: continuationPrompt}},
			})

			time.Sleep(500 * time.Millisecond) // Slight backoff
			lastStreamErr = nil                // Clear error for the next continuation if successful
			continue
		}

		break
	}

	if lastStreamErr != nil {
		if req.StreamChannel != nil {
			req.StreamChannel <- core.StreamEvent{Error: lastStreamErr}
			close(req.StreamChannel)
		}
		return core.CoreResult{Text: completeOut.String()}, lastStreamErr
	}

	fullText := completeOut.String()
	thinking, cleanText := emulatePkg.ExtractThinking(fullText)

	if req.StreamChannel != nil {
		textOut, thinkingOut := fsm.Flush()
		if textOut != "" || thinkingOut != "" {
			req.StreamChannel <- core.StreamEvent{TextDelta: textOut, ThinkingDelta: thinkingOut}
		}
		req.StreamChannel <- core.StreamEvent{Done: true}
		close(req.StreamChannel)
	}

	return core.CoreResult{Text: cleanText, Thinking: thinking}, nil
}

func buildCursorMessages(req core.CoreRequest) ([]cursorMessage, bool) {
	msgs := make([]cursorMessage, 0, len(req.Messages)+1)

	injected := false
	for _, m := range req.Messages {
		txt := m.Content
		if m.Role == "user" && !injected {
			if req.System != "" {
				txt = req.System + "\n\n---\n\n" + txt
			}
			injected = true
		}
		if strings.TrimSpace(txt) == "" {
			continue
		}

		role := m.Role
		if role == "tool" {
			txt = "Action output:\n" + txt + "\n\nBased on the output above, continue with the next appropriate action using the structured format."
			role = "user"
		} else if role == "system" {
			role = "user"
		}

		msgs = append(msgs, cursorMessage{
			Role:  role,
			ID:    "m_" + randID(12),
			Parts: []cursorPart{{Type: "text", Text: txt}},
		})
	}
	if !injected && req.System != "" {
		msgs = append([]cursorMessage{{
			Role:  "user",
			ID:    "m_" + randID(12),
			Parts: []cursorPart{{Type: "text", Text: req.System}},
		}}, msgs...)
		injected = true
	}
	return msgs, injected
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func randID(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	seed := time.Now().UnixNano()
	for i := 0; i < n; i++ {
		seed = (seed*1664525 + 1013904223) & 0x7fffffff
		b[i] = alphabet[int(seed)%len(alphabet)]
	}
	return string(b)
}

func applyAuth(headers map[string]string, auth config.AuthConf) error {
	switch auth.Kind {
	case "", "none":
		return nil
	case "bearer":
		if auth.TokenEnv == "" {
			return fmt.Errorf("auth.kind=bearer requires auth.token_env")
		}
		tok := strings.TrimSpace(os.Getenv(auth.TokenEnv))
		if tok == "" {
			return fmt.Errorf("auth token env %q is empty", auth.TokenEnv)
		}
		headers["authorization"] = "Bearer " + tok
		return nil
	case "header":
		if auth.HeaderName == "" || auth.HeaderValueEnv == "" {
			return fmt.Errorf("auth.kind=header requires auth.header_name and auth.header_value_env")
		}
		val := strings.TrimSpace(os.Getenv(auth.HeaderValueEnv))
		if val == "" {
			return fmt.Errorf("auth header env %q is empty", auth.HeaderValueEnv)
		}
		headers[auth.HeaderName] = val
		return nil
	default:
		return fmt.Errorf("unsupported auth.kind: %s", auth.Kind)
	}
}
