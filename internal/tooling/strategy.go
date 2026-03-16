package tooling

import (
	"context"

	"github.com/lhpqaq/all2api/internal/core"
	"github.com/lhpqaq/all2api/internal/tooling/emulate"
	"github.com/lhpqaq/all2api/internal/upstream"
)

type Strategy interface {
	Prepare(ctx context.Context, req core.CoreRequest, cap upstream.Capabilities) (core.CoreRequest, error)
	PostProcessResult(ctx context.Context, req core.CoreRequest, upstreamResult core.CoreResult, cap upstream.Capabilities) (core.CoreResult, error)
}

type nativeStrategy struct{}

func NewNativeStrategy() Strategy {
	return &nativeStrategy{}
}

func (s *nativeStrategy) Prepare(_ context.Context, req core.CoreRequest, _ upstream.Capabilities) (core.CoreRequest, error) {
	return req, nil
}

func (s *nativeStrategy) PostProcessResult(_ context.Context, _ core.CoreRequest, upstreamResult core.CoreResult, _ upstream.Capabilities) (core.CoreResult, error) {
	return upstreamResult, nil
}

type emulatedStrategy struct {
	cfg    emulate.Config
	binder upstream.ToolingEmulationBinder
}

func NewEmulatedStrategy(cfg emulate.Config, binder upstream.ToolingEmulationBinder) Strategy {
	if cfg.MaxScanBytes <= 0 {
		cfg.MaxScanBytes = 256 * 1024
	}
	return &emulatedStrategy{cfg: cfg, binder: binder}
}

func (s *emulatedStrategy) Prepare(_ context.Context, req core.CoreRequest, _ upstream.Capabilities) (core.CoreRequest, error) {
	if len(req.Tools) == 0 {
		return req, nil
	}
	if s.binder == nil {
		return req, nil
	}
	return s.binder.PrepareEmulatedTooling(context.Background(), req)
}

func (s *emulatedStrategy) PostProcessResult(ctx context.Context, req core.CoreRequest, upstreamResult core.CoreResult, _ upstream.Capabilities) (core.CoreResult, error) {
	text := upstreamResult.Text
	calls, clean, err := emulate.ParseActionBlocks(text, s.cfg)
	if err != nil {
		return core.CoreResult{Text: text}, nil
	}
	return core.CoreResult{Text: clean, ToolCalls: calls}, nil
}
