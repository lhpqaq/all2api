package upstream

import (
	"fmt"
	"sync"

	"github.com/lhpqaq/all2api/internal/config"
)

type Factory func(name string, cfg config.UpstreamConf) (Upstream, Capabilities, error)

type Registry struct {
	cfg       config.Config
	factories map[string]Factory
	cache     map[string]entry
	mu        sync.RWMutex
}

type entry struct {
	up  Upstream
	cap Capabilities
}

func NewRegistry(cfg config.Config) (*Registry, error) {
	return &Registry{cfg: cfg, factories: map[string]Factory{}, cache: map[string]entry{}}, nil
}

func (r *Registry) RegisterFactory(typ string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[typ] = f
}

func (r *Registry) Get(name string) (Upstream, Capabilities, error) {
	r.mu.RLock()
	if e, ok := r.cache[name]; ok {
		r.mu.RUnlock()
		return e.up, e.cap, nil
	}
	r.mu.RUnlock()

	ucfg, ok := r.cfg.Upstreams[name]
	if !ok {
		return nil, Capabilities{}, fmt.Errorf("unknown upstream: %s", name)
	}
	r.mu.RLock()
	f, ok := r.factories[ucfg.Type]
	r.mu.RUnlock()
	if !ok {
		return nil, Capabilities{}, fmt.Errorf("no factory registered for upstream type: %s", ucfg.Type)
	}
	up, cap, err := f(name, ucfg)
	if err != nil {
		return nil, Capabilities{}, err
	}
	r.mu.Lock()
	if e, ok := r.cache[name]; ok {
		r.mu.Unlock()
		return e.up, e.cap, nil
	}
	r.cache[name] = entry{up: up, cap: cap}
	r.mu.Unlock()
	return up, cap, nil
}
