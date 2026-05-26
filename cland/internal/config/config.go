// Package config loads cland's YAML configuration. Pattern mirrors
// runtime-adapter's config loader — explicit Default(), permissive Load()
// that returns defaults on file-missing.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen    ListenConfig    `yaml:"listen"`
	State     StateConfig     `yaml:"state"`
	Discovery DiscoveryConfig `yaml:"discovery"`
	Runtime   RuntimeConfig   `yaml:"runtime"`
	Telemetry TelemetryConfig `yaml:"telemetry"`
}

type ListenConfig struct {
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

type StateConfig struct {
	Dir string `yaml:"dir"`
}

type DiscoveryConfig struct {
	MDNSEnabled bool   `yaml:"mdns_enabled"`
	Interface   string `yaml:"interface"`
}

type RuntimeConfig struct {
	BaseURL string `yaml:"base_url"`
}

type TelemetryConfig struct {
	LogLevel string `yaml:"log_level"`
}

// Default returns the production defaults; safe to use when no config file
// is present.
func Default() Config {
	return Config{
		Listen:    ListenConfig{Address: "0.0.0.0", Port: 7777},
		State:     StateConfig{Dir: "/var/lib/minti/cland"},
		Discovery: DiscoveryConfig{MDNSEnabled: true},
		Runtime:   RuntimeConfig{BaseURL: "http://127.0.0.1:7780"},
		Telemetry: TelemetryConfig{LogLevel: "info"},
	}
}

// Load reads path; a missing file returns Default() with no error.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return cfg, nil
}
