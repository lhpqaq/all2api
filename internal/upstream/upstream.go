package upstream

import (
	"context"

	"github.com/lhpqaq/all2api/internal/core"
)

type Capabilities struct {
	NativeToolCalls bool
	SupportThinking bool
}

type Upstream interface {
	Do(ctx context.Context, req core.CoreRequest) (core.CoreResult, error)
}
