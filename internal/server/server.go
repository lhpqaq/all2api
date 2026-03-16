package server

import (
	"net/http"
	"strings"

	"github.com/lhpqaq/all2api/internal/config"
	"github.com/lhpqaq/all2api/internal/downstream/anthropic"
	"github.com/lhpqaq/all2api/internal/downstream/openai"
	"github.com/lhpqaq/all2api/internal/orchestrator"
	"github.com/lhpqaq/all2api/internal/upstream"
	"github.com/lhpqaq/all2api/internal/upstream/cursor"
	"github.com/lhpqaq/all2api/internal/upstream/zed"
)

type Server struct {
	cfg  config.Config
	mux  *http.ServeMux
	orch *orchestrator.Orchestrator
}

func New(cfg config.Config) (*Server, error) {
	reg, err := upstream.NewRegistry(cfg)
	if err != nil {
		return nil, err
	}
	reg.RegisterFactory("cursor", cursor.New)
	reg.RegisterFactory("zed", zed.New)

	orch, err := orchestrator.New(cfg, reg)
	if err != nil {
		return nil, err
	}

	s := &Server{cfg: cfg, mux: http.NewServeMux(), orch: orch}
	s.routes()
	return s, nil
}

func (s *Server) Router() http.Handler {
	if len(s.cfg.Server.APIKeys) == 0 {
		return s.mux
	}

	validKeys := make(map[string]bool)
	for _, k := range s.cfg.Server.APIKeys {
		validKeys[k] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			s.mux.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		token := strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)

		if token == "" {
			token = strings.TrimSpace(r.Header.Get("X-API-Key"))
		}

		if !validKeys[token] {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": {"message": "Unauthorized: Invalid or missing API Key" }}`))
			return
		}

		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	anthropic.Register(s.mux, s.cfg, s.orch)
	openai.Register(s.mux, s.cfg, s.orch)
}
