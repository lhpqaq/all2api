package upstream

import "context"

type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

type ModelChecker interface {
	HasModel(ctx context.Context, model string) (bool, error)
}

func HasModel(ctx context.Context, up Upstream, model string) (bool, error) {
	if up == nil {
		return false, nil
	}
	if c, ok := up.(ModelChecker); ok {
		return c.HasModel(ctx, model)
	}
	if l, ok := up.(ModelLister); ok {
		ms, err := l.ListModels(ctx)
		if err != nil {
			return false, err
		}
		for _, m := range ms {
			if m == model {
				return true, nil
			}
		}
	}
	return false, nil
}
