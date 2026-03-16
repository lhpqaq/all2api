package orchestrator

import (
	"context"
	"math/rand"
	"strings"
	"time"

	"github.com/lhpqaq/all2api/internal/upstream"
)

func splitUpstreamModel(model string) (string, string, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", "", false
	}
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	a := strings.TrimSpace(parts[0])
	b := strings.TrimSpace(parts[1])
	if a == "" || b == "" {
		return "", "", false
	}
	return a, b, true
}

func (o *Orchestrator) findUpstreamForModel(ctx context.Context, model string) (string, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", false
	}

	names := make([]string, 0, len(o.cfg.Upstreams))
	for name := range o.cfg.Upstreams {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", false
	}

	src := rand.New(rand.NewSource(time.Now().UnixNano()))
	start := src.Intn(len(names))
	for i := 0; i < len(names); i++ {
		idx := (start + i) % len(names)
		name := names[idx]
		up, _, err := o.reg.Get(name)
		if err != nil {
			continue
		}
		has, err := upstream.HasModel(ctx, up, model)
		if err != nil {
			continue
		}
		if has {
			return name, true
		}
	}
	return "", false
}
