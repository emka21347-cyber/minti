// Package config loads cland's YAML configuration. Pattern mirrors
// runtime-adapter's config loader — explicit Default(), permissive Load()
// that returns defaults on file-missing.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen    ListenConfig    `yaml:"listen"`
	State     StateConfig     `yaml:"state"`
	Discovery DiscoveryConfig `yaml:"discovery"`
	Advertise AdvertiseConfig `yaml:"advertise"`
	Election  ElectionConfig  `yaml:"election"`
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
	RubricPath  string `yaml:"rubric_path"`
}

// AdvertiseConfig controls the §4.2 capability advertisement loop (Phase D).
type AdvertiseConfig struct {
	Interval     time.Duration `yaml:"interval"`       // 30s default
	BumpRate     time.Duration `yaml:"bump_rate"`      // 1s default
	InitialDelay time.Duration `yaml:"initial_delay"`  // 5s default
}

// ElectionConfig controls the §5.2 leader-lease election cadence (Phase E).
// Defaults match the spec verbatim; overrides exist so tests can dial the
// cadence down to ~50 ms for fast iteration.
type ElectionConfig struct {
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"` // 2s — Orchestrator emit cadence
	LeaseDuration     time.Duration `yaml:"lease_duration"`     // 8s — receiver lease window
	FailoverGrace     time.Duration `yaml:"failover_grace"`     // 6s — silence tolerated before election
	ElectionTimeout   time.Duration `yaml:"election_timeout"`   // 1s — accept-count window
	HistorySize       int           `yaml:"history_size"`       // 32 — ring buffer for /clan/election/history
	RuntimeProbeMaxAge time.Duration `yaml:"runtime_probe_max_age"` // 60s (2× ad interval) — R1 zombie-leader gate
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
		Discovery: DiscoveryConfig{MDNSEnabled: true, RubricPath: "/etc/minti/reasoning-scores.yaml"},
		Advertise: AdvertiseConfig{
			Interval:     30 * time.Second,
			BumpRate:     1 * time.Second,
			InitialDelay: 5 * time.Second,
		},
		Election: ElectionConfig{
			HeartbeatInterval:  2 * time.Second,
			LeaseDuration:      8 * time.Second,
			FailoverGrace:      6 * time.Second,
			ElectionTimeout:    1 * time.Second,
			HistorySize:        32,
			RuntimeProbeMaxAge: 60 * time.Second,
		},
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
