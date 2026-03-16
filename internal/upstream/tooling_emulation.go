package upstream

import (
	"context"

	"github.com/lhpqaq/all2api/internal/core"
)

type ToolingEmulationBinder interface {
	PrepareEmulatedTooling(ctx context.Context, req core.CoreRequest) (core.CoreRequest, error)
	LooksLikeRefusal(text string) bool
	ActionBlockExample(tools []core.ToolDef) string
	ForceToolingPrompt(choice core.ToolChoice) string
}

type ToolingEmulationBinderProvider interface {
	ToolingEmulationBinder() ToolingEmulationBinder
}
