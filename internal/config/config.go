package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a scalar")
	}
	if node.Value == "" {
		d.Duration = 0
		return nil
	}
	if dur, err := time.ParseDuration(node.Value); err == nil {
		d.Duration = dur
		return nil
	}
	n, err := strconv.ParseInt(node.Value, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid duration: %q", node.Value)
	}
	d.Duration = time.Duration(n) * time.Second
	return nil
}

type Config struct {
	Server    ServerConfig            `yaml:"server"`
	Routing   RoutingConfig           `yaml:"routing"`
	Tooling   ToolingConfig           `yaml:"tooling"`
	Logging   LoggingConfig           `yaml:"logging"`
	Upstreams map[string]UpstreamConf `yaml:"upstreams"`
}

type LoggingConfig struct {
	Debug bool `yaml:"debug"`
}

type ServerConfig struct {
	Addr         string   `yaml:"addr"`
	ReadTimeout  Duration `yaml:"read_timeout"`
	WriteTimeout Duration `yaml:"write_timeout"`
	IdleTimeout  Duration `yaml:"idle_timeout"`
	APIKeys      []string `yaml:"api_keys"`
}

type RoutingConfig struct {
	DefaultUpstream string            `yaml:"default_upstream"`
	UpstreamHeader  string            `yaml:"upstream_header"`
	ModelMap        map[string]string `yaml:"model_map"`
}

type ToolingConfig struct {
	Emulate EmulateToolingConfig `yaml:"emulate"`
}

type EmulateToolingConfig struct {
	Enabled        bool `yaml:"enabled"`
	MaxScanBytes   int  `yaml:"max_scan_bytes"`
	SmartQuotes    bool `yaml:"smart_quotes"`
	FuzzyKeyMatch  bool `yaml:"fuzzy_key_match"`
	Debug          bool `yaml:"debug"`
	RetryOnRefusal bool `yaml:"retry_on_refusal"`
	MaxRetries     int  `yaml:"max_retries"`
}

type UpstreamConf struct {
	Type    string            `yaml:"type"`
	BaseURL string            `yaml:"base_url"`
	Timeout Duration          `yaml:"timeout"`
	Proxy   string            `yaml:"proxy"`
	Headers map[string]string `yaml:"headers"`
	Models  []string          `yaml:"models"`

	Capabilities UpstreamCapsConf `yaml:"capabilities"`
	Auth         AuthConf         `yaml:"auth"`
}

type UpstreamCapsConf struct {
	NativeToolCalls *bool `yaml:"native_tool_calls"`
}

type AuthConf struct {
	Kind           string `yaml:"kind"`
	Token          string `yaml:"token"`
	TokenEnv       string `yaml:"token_env"`
	HeaderName     string `yaml:"header_name"`
	HeaderValueEnv string `yaml:"header_value_env"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Addr:         "0.0.0.0:8848",
			ReadTimeout:  Duration{Duration: 30 * time.Second},
			WriteTimeout: Duration{Duration: 0},
			IdleTimeout:  Duration{Duration: 120 * time.Second},
		},
		Routing: RoutingConfig{
			DefaultUpstream: "cursor",
			UpstreamHeader:  "X-All2API-Upstream",
			ModelMap:        map[string]string{"cursor": "anthropic/claude-sonnet-4.6"},
		},
		Tooling: ToolingConfig{
			Emulate: EmulateToolingConfig{
				Enabled:        true,
				MaxScanBytes:   256 * 1024,
				SmartQuotes:    true,
				RetryOnRefusal: true,
				MaxRetries:     2,
			},
		},
		Logging: LoggingConfig{Debug: false},
		Upstreams: map[string]UpstreamConf{
			"cursor": {
				Type:    "cursor",
				BaseURL: "https://cursor.com",
				Timeout: Duration{Duration: 120 * time.Second},
				Headers: map[string]string{},
				Auth:    AuthConf{Kind: "none"},
			},
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	raw, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse yaml: %w", err)
		}
	}

	applyEnvOverrides(&cfg)

	if cfg.Server.Addr == "" {
		return Config{}, errors.New("server.addr is required")
	}
	if cfg.Routing.UpstreamHeader == "" {
		cfg.Routing.UpstreamHeader = "X-All2API-Upstream"
	}
	if cfg.Routing.DefaultUpstream == "" {
		return Config{}, errors.New("routing.default_upstream is required")
	}
	if cfg.Routing.DefaultUpstream != "auto" {
		if _, ok := cfg.Upstreams[cfg.Routing.DefaultUpstream]; !ok {
			return Config{}, fmt.Errorf("default upstream %q not found in upstreams", cfg.Routing.DefaultUpstream)
		}
	}

	for name, u := range cfg.Upstreams {
		if u.Type == "" {
			return Config{}, fmt.Errorf("upstreams.%s.type is required", name)
		}
		if u.BaseURL == "" && u.Type != "zed" {
			return Config{}, fmt.Errorf("upstreams.%s.base_url is required", name)
		}
		if u.Timeout.Duration <= 0 {
			u.Timeout = Duration{Duration: 120 * time.Second}
			cfg.Upstreams[name] = u
		}
		if u.Auth.Kind == "" {
			u.Auth.Kind = "none"
			cfg.Upstreams[name] = u
		}
	}

	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("ALL2API_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("ALL2API_API_KEYS"); v != "" {
		keys := strings.Split(v, ",")
		for i := range keys {
			keys[i] = strings.TrimSpace(keys[i])
		}
		cfg.Server.APIKeys = keys
	}
	if v := os.Getenv("ALL2API_DEFAULT_UPSTREAM"); v != "" {
		cfg.Routing.DefaultUpstream = v
	}
	if v := os.Getenv("ALL2API_DEBUG"); v != "" {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			cfg.Logging.Debug = b
		}
	}
	if v := os.Getenv("ALL2API_TOOLING_EMULATE_DEBUG"); v != "" {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			cfg.Tooling.Emulate.Debug = b
		}
	}
	if v := os.Getenv("ALL2API_TOOLING_EMULATE_RETRY_ON_REFUSAL"); v != "" {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			cfg.Tooling.Emulate.RetryOnRefusal = b
		}
	}
	if v := os.Getenv("ALL2API_TOOLING_EMULATE_MAX_RETRIES"); v != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			cfg.Tooling.Emulate.MaxRetries = n
		}
	}
}
