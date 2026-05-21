// Package config loads /etc/minti/runtime.yaml into a typed struct.
// Layout is the source of truth for what users see; see configs/runtime.yaml.example.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/minti/runtime-adapter/internal/backend"
)

// Config is the parsed contents of runtime.yaml.
type Config struct {
	Listen   ListenConfig   `yaml:"listen"`
	Backend  BackendConfig  `yaml:"backend"`
	Telemetry TelemetryConfig `yaml:"telemetry"`
}

// ListenConfig — where the runtime exposes its HTTP API. Bound to
// localhost by default; cland is the only legitimate consumer.
type ListenConfig struct {
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

// BackendConfig — which concrete runtime we delegate to.
type BackendConfig struct {
	Kind     backend.Kind `yaml:"kind"`     // ollama | llamacpp-server | localai | remote-api
	BaseURL  string       `yaml:"base_url"` // optional, defaults per-backend
	Model    string       `yaml:"model"`    // for remote-api: which remote model
	Vendor   string       `yaml:"vendor"`   // for remote-api
	APIKeyID string       `yaml:"api_key_id"` // reference into key store, not the secret
}

// TelemetryConfig — optional local-only logging knobs.
type TelemetryConfig struct {
	LogLevel string `yaml:"log_level"` // debug | info | warn | error
}

// Default returns a config suitable for a fresh install:
// listen on localhost:7780, backend = ollama on its default port.
func Default() Config {
	return Config{
		Listen:   ListenConfig{Address: "127.0.0.1", Port: 7780},
		Backend:  BackendConfig{Kind: backend.KindOllama},
		Telemetry: TelemetryConfig{LogLevel: "info"},
	}
}

// Load reads YAML from path and merges into a Default(). Missing path is
// not an error — we return the defaults so the daemon can start at all.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// NewBackend constructs the concrete Backend matching cfg.
func NewBackend(cfg BackendConfig) (backend.Backend, error) {
	switch cfg.Kind {
	case backend.KindOllama, "":
		return backend.NewOllama(cfg.BaseURL), nil
	case backend.KindLlamaCpp:
		return &backend.LlamaCpp{BaseURL: cfg.BaseURL}, nil
	case backend.KindLocalAI:
		return &backend.LocalAI{BaseURL: cfg.BaseURL}, nil
	case backend.KindRemoteAPI:
		return &backend.RemoteAPI{Vendor: cfg.Vendor, BaseURL: cfg.BaseURL, Model: cfg.Model, APIKeyID: cfg.APIKeyID}, nil
	default:
		return nil, fmt.Errorf("unknown backend kind: %q", cfg.Kind)
	}
}
