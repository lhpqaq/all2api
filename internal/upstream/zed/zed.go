package zed

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lhpqaq/all2api/internal/config"
	"github.com/lhpqaq/all2api/internal/core"
	"github.com/lhpqaq/all2api/internal/upstream"
)

const (
	zedTokenURL       = "https://cloud.zed.dev/client/llm_tokens"
	zedCompletionsURL = "https://cloud.zed.dev/completions"
	zedSystemID       = "6b87ab66-af2c-49c7-b986-ef4c27c9e1fb"
	zedVersion        = "0.222.4+stable.147.b385025df963c9e8c3f74cc4dadb1c4b29b3c6f0"
)

type zedUpstream struct {
	client  *http.Client
	timeout time.Duration
	models  []string

	authHeader string // contains "<user_id> <credential_json>"

	mu           sync.RWMutex
	jwtToken     string
	jwtExpiresAt time.Time
}

func getProvider(model string) string {
	if strings.HasPrefix(model, "claude") {
		return "anthropic"
	}
	if strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") {
		return "open_ai"
	}
	if strings.HasPrefix(model, "gemini") {
		return "google"
	}
	if strings.HasPrefix(model, "grok") {
		return "x_ai"
	}
	return "anthropic"
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

	authHeader := ""
	if ucfg.Auth.Kind == "token" && ucfg.Auth.Token != "" {
		authHeader = ucfg.Auth.Token
	} else if ucfg.Auth.TokenEnv != "" {
		authHeader = strings.TrimSpace(os.Getenv(ucfg.Auth.TokenEnv))
	} else if ucfg.Auth.HeaderValueEnv != "" {
		authHeader = strings.TrimSpace(os.Getenv(ucfg.Auth.HeaderValueEnv))
	} else if val, ok := ucfg.Headers["authorization"]; ok {
		authHeader = val
	}

	if authHeader == "" {
		return nil, upstream.Capabilities{}, fmt.Errorf("zed upstream requires auth configuraiton (token_env or authorization header) formatted as '<user_id> <credential_json>'")
	}

	z := &zedUpstream{
		client:     client,
		timeout:    ucfg.Timeout.Duration,
		models:     ucfg.Models,
		authHeader: authHeader,
	}

	caps := upstream.Capabilities{
		NativeToolCalls: true,
		SupportThinking: false,
	}

	return z, caps, nil
}

func (z *zedUpstream) ListModels(ctx context.Context) ([]string, error) {
	_ = ctx
	if len(z.models) == 0 {
		return []string{
			"claude-3-7-sonnet",
			"claude-sonnet-4-5",
			"claude-haiku-4-5",
			"gpt-5.4-latest",
			"gpt-5.3-codex",
			"gpt-5.2",
			"gpt-5.2-codex",
			"gpt-5-mini",
			"gpt-5-nano",
			"gemini-3.1-pro",
			"gemini-3-pro",
		}, nil
	}
	out := make([]string, 0, len(z.models)+2)
	for _, m := range z.models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

func (z *zedUpstream) HasModel(ctx context.Context, model string) (bool, error) {
	if len(z.models) == 0 {
		return true, nil
	}
	for _, m := range z.models {
		if m == model {
			return true, nil
		}
	}
	return false, nil
}

func (z *zedUpstream) getToken(ctx context.Context) (string, error) {
	z.mu.RLock()
	tok := z.jwtToken
	exp := z.jwtExpiresAt
	z.mu.RUnlock()

	if tok != "" && time.Now().Add(60*time.Second).Before(exp) {
		return tok, nil
	}

	z.mu.Lock()
	defer z.mu.Unlock()

	// Double check
	if z.jwtToken != "" && time.Now().Add(60*time.Second).Before(z.jwtExpiresAt) {
		return z.jwtToken, nil
	}

	req, err := http.NewRequestWithContext(ctx, "POST", zedTokenURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", z.authHeader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Zed-System-Id", zedSystemID)

	resp, err := z.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch zed token error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("zed token fetch failed with status %d: %s", resp.StatusCode, string(b))
	}

	var resData struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&resData); err != nil {
		return "", fmt.Errorf("parse zed token response error: %w", err)
	}

	jwtTok := resData.Token
	parts := strings.Split(jwtTok, ".")
	if len(parts) >= 2 {
		payload := parts[1]
		if l := len(payload) % 4; l > 0 {
			payload += strings.Repeat("=", 4-l)
		}
		dec, err := base64.URLEncoding.DecodeString(payload)
		if err == nil {
			var claims struct {
				Exp int64 `json:"exp"`
			}
			if err := json.Unmarshal(dec, &claims); err == nil {
				z.jwtExpiresAt = time.Unix(claims.Exp, 0)
				z.jwtToken = jwtTok
			}
		}
	}

	if z.jwtToken == "" {
		z.jwtToken = jwtTok
		z.jwtExpiresAt = time.Now().Add(1 * time.Hour) // fallback 1h
	}

	return z.jwtToken, nil
}

func (z *zedUpstream) Do(ctx context.Context, req core.CoreRequest) (core.CoreResult, error) {
	if z.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, z.timeout)
		defer cancel()
	}

	jwt, err := z.getToken(ctx)
	if err != nil {
		return core.CoreResult{}, err
	}

	payload, err := z.buildPayload(req)
	if err != nil {
		return core.CoreResult{}, err
	}

	bodyData, err := json.Marshal(payload)
	if err != nil {
		return core.CoreResult{}, fmt.Errorf("marshal payload error: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", zedCompletionsURL, bytes.NewReader(bodyData))
	if err != nil {
		return core.CoreResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+jwt)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Zed-Version", zedVersion)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := z.client.Do(httpReq)
	if err != nil {
		return core.CoreResult{}, fmt.Errorf("do request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return core.CoreResult{}, fmt.Errorf("zed upstream HTTP %d: %s", resp.StatusCode, string(b))
	}

	if !req.Stream {
		defer resp.Body.Close()
		return z.handleNonStream(resp.Body)
	}

	return z.handleStream(ctx, req, resp.Body)
}
