package orchestrator

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/lhpqaq/all2api/internal/config"
	"github.com/lhpqaq/all2api/internal/core"
	"github.com/lhpqaq/all2api/internal/diag"
	"github.com/lhpqaq/all2api/internal/tooling"
	"github.com/lhpqaq/all2api/internal/tooling/emulate"
	"github.com/lhpqaq/all2api/internal/upstream"
)

type Orchestrator struct {
	cfg config.Config
	reg *upstream.Registry
}

func New(cfg config.Config, reg *upstream.Registry) (*Orchestrator, error) {
	return &Orchestrator{cfg: cfg, reg: reg}, nil
}

func (o *Orchestrator) Execute(ctx context.Context, req core.CoreRequest) (core.CoreResult, error) {
	if raw := strings.TrimSpace(req.Model); raw != "" {
		if a, b, ok := splitUpstreamModel(raw); ok {
			if _, exists := o.cfg.Upstreams[a]; exists {
				req.Upstream = a
				req.Model = b
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(req.Upstream), "auto") {
		if resolved, ok := o.findUpstreamForModel(ctx, strings.TrimSpace(req.Model)); ok {
			req.Upstream = resolved
		}
	}

	modelIn := req.Model
	if mapped, ok := o.cfg.Routing.ModelMap[req.Model]; ok {
		m := strings.TrimSpace(mapped)
		if m != "" {
			req.Model = m
		}
	}
	modelOut := req.Model

	debug := o.cfg.Logging.Debug || o.cfg.Tooling.Emulate.Debug || diag.Debug(ctx)
	if debug {
		log.Printf("[all2api] req_id=%s phase=orchestrator.start endpoint=%s upstream=%s model_in=%s model_out=%s stream=%t tools=%d tool_choice=%s/%s system_len=%d messages=%d",
			diag.RequestID(ctx), req.Endpoint, req.Upstream, modelIn, modelOut, req.Stream, len(req.Tools), req.ToolChoice.Mode, req.ToolChoice.Name, len(req.System), len(req.Messages),
		)
	}

	up, cap, err := o.reg.Get(req.Upstream)
	if err != nil {
		return core.CoreResult{}, err
	}
	var binder upstream.ToolingEmulationBinder
	if p, ok := up.(upstream.ToolingEmulationBinderProvider); ok {
		binder = p.ToolingEmulationBinder()
	}
	if debug {
		log.Printf("[all2api] req_id=%s phase=orchestrator.capabilities native_tool_calls=%t", diag.RequestID(ctx), cap.NativeToolCalls)
	}

	var strat tooling.Strategy
	if cap.NativeToolCalls {
		strat = tooling.NewNativeStrategy()
	} else {
		if !o.cfg.Tooling.Emulate.Enabled && len(req.Tools) > 0 {
			return core.CoreResult{}, fmt.Errorf("upstream %q does not support tool calls and tooling.emulate.enabled=false", req.Upstream)
		}
		ecfg := emulate.Config{
			MaxScanBytes:  o.cfg.Tooling.Emulate.MaxScanBytes,
			SmartQuotes:   o.cfg.Tooling.Emulate.SmartQuotes,
			FuzzyKeyMatch: o.cfg.Tooling.Emulate.FuzzyKeyMatch,
		}
		strat = tooling.NewEmulatedStrategy(ecfg, binder)
		if debug {
			log.Printf("[all2api] req_id=%s phase=orchestrator.strategy strategy=emulated emulate_max_scan_bytes=%d smart_quotes=%t fuzzy_key_match=%t",
				diag.RequestID(ctx), ecfg.MaxScanBytes, ecfg.SmartQuotes, ecfg.FuzzyKeyMatch,
			)
		}
	}

	prepared, err := strat.Prepare(ctx, req, cap)
	if err != nil {
		return core.CoreResult{}, fmt.Errorf("tooling prepare: %w", err)
	}
	if debug && !cap.NativeToolCalls {
		systemBefore := len(req.System)
		systemAfter := len(prepared.System)
		log.Printf("[all2api] req_id=%s phase=tooling.prepare tools=%d tool_choice=%s/%s system_len_before=%d system_len_after=%d messages_before=%d messages_after=%d",
			diag.RequestID(ctx), len(req.Tools), req.ToolChoice.Mode, req.ToolChoice.Name, systemBefore, systemAfter, len(req.Messages), len(prepared.Messages),
		)
		if len(req.Tools) > 0 {
			names := make([]string, 0, len(req.Tools))
			empty := 0
			for _, t := range req.Tools {
				n := strings.TrimSpace(t.Name)
				if n == "" {
					empty++
					continue
				}
				names = append(names, n)
				if len(names) >= 12 {
					break
				}
			}
			log.Printf("[all2api] req_id=%s phase=tooling.prepare tools_empty_names=%d tool_names=%q", diag.RequestID(ctx), empty, names)
		}
	}
	if debug {
		log.Printf("[all2api] req_id=%s phase=orchestrator.prepared system_len=%d", diag.RequestID(ctx), len(prepared.System))
	}

	upstreamResult, err := up.Do(ctx, prepared)
	if err != nil {
		if debug {
			log.Printf("[all2api] req_id=%s phase=orchestrator.upstream_error err=%q partial_text_len=%d",
				diag.RequestID(ctx), err.Error(), len(upstreamResult.Text),
			)
		}
		return core.CoreResult{}, err
	}
	if debug {
		prefix := strings.TrimSpace(upstreamResult.Text)
		if len(prefix) > 200 {
			prefix = prefix[:200] + "…"
		}
		log.Printf("[all2api] req_id=%s phase=orchestrator.upstream_ok text_len=%d text_prefix=%q",
			diag.RequestID(ctx), len(upstreamResult.Text), prefix,
		)
	}

	result, err := strat.PostProcessResult(ctx, prepared, upstreamResult, cap)
	if err != nil {
		return core.CoreResult{}, fmt.Errorf("tooling postprocess: %w", err)
	}
	if debug && !cap.NativeToolCalls {
		log.Printf("[all2api] req_id=%s phase=tooling.postprocess tool_calls=%d", diag.RequestID(ctx), len(result.ToolCalls))
	}
	if !cap.NativeToolCalls && len(req.Tools) > 0 && len(result.ToolCalls) == 0 {
		maxRetries := o.cfg.Tooling.Emulate.MaxRetries
		if maxRetries <= 0 {
			maxRetries = 2
		}
		shouldRetry := false
		reason := ""
		if req.ToolChoice.Mode == "tool" && strings.TrimSpace(req.ToolChoice.Name) != "" {
			shouldRetry = true
			reason = "tool_choice_tool"
		}
		if req.ToolChoice.Mode == "any" {
			shouldRetry = true
			reason = "tool_choice_any"
		}
		if o.cfg.Tooling.Emulate.RetryOnRefusal && binder != nil && binder.LooksLikeRefusal(upstreamResult.Text) {
			shouldRetry = true
			if reason == "" {
				reason = "refusal"
			}
		}
		if shouldRetry && binder != nil {
			for attempt := 1; attempt <= maxRetries; attempt++ {
				if debug {
					log.Printf("[all2api] req_id=%s phase=tooling.retry attempt=%d/%d reason=%s", diag.RequestID(ctx), attempt, maxRetries, reason)
				}
				retryReq := prepared
				msgs := make([]core.Message, 0, len(prepared.Messages)+2)
				msgs = append(msgs, prepared.Messages...)
				assistantText := upstreamResult.Text
				if binder != nil && binder.LooksLikeRefusal(assistantText) {
					ex := binder.ActionBlockExample(req.Tools)
					if ex != "" {
						assistantText = ex
					}
				}
				force := ""
				if binder != nil {
					force = binder.ForceToolingPrompt(req.ToolChoice)
				}
				msgs = append(msgs,
					core.Message{Role: "assistant", Content: assistantText},
					core.Message{Role: "user", Content: force},
				)
				retryReq.Messages = msgs

				retryUpstream, retryErr := up.Do(ctx, retryReq)
				if retryErr != nil {
					if debug {
						log.Printf("[all2api] req_id=%s phase=tooling.retry_error attempt=%d err=%q", diag.RequestID(ctx), attempt, retryErr.Error())
					}
					break
				}
				retryResult, retryPostErr := strat.PostProcessResult(ctx, retryReq, retryUpstream, cap)
				if retryPostErr != nil {
					break
				}
				upstreamResult = retryUpstream
				result = retryResult
				if debug {
					log.Printf("[all2api] req_id=%s phase=tooling.retry_result attempt=%d tool_calls=%d text_len=%d", diag.RequestID(ctx), attempt, len(result.ToolCalls), len(result.Text))
				}
				if len(result.ToolCalls) > 0 {
					break
				}
			}
		}
	}
	if debug {
		log.Printf("[all2api] req_id=%s phase=orchestrator.done tool_calls=%d final_text_len=%d",
			diag.RequestID(ctx), len(result.ToolCalls), len(result.Text),
		)
	}
	return result, nil
}

type UpstreamModelInfo struct {
	Models          []string
	SupportThinking bool
}

func (o *Orchestrator) GetUpstreamModels(ctx context.Context) map[string]UpstreamModelInfo {
	res := make(map[string]UpstreamModelInfo)
	for upName, upCfg := range o.cfg.Upstreams {
		upName = strings.TrimSpace(upName)
		if upName == "" {
			continue
		}

		info := UpstreamModelInfo{
			Models:          upCfg.Models,
			SupportThinking: true, // default or fallback
		}

		if up, caps, err := o.reg.Get(upName); err == nil {
			info.SupportThinking = caps.SupportThinking
			if lister, ok := up.(upstream.ModelLister); ok {
				if ms, err := lister.ListModels(ctx); err == nil {
					info.Models = ms
					res[upName] = info
					continue
				}
			}
		}
		res[upName] = info
	}
	return res
}
