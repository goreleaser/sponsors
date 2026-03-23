package main

import (
	_ "embed"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

//go:embed sponsors.yml
var defaultConfigData []byte

// Config holds all settings for the sponsors tool.
type Config struct {
	GitHubUser         string            `yaml:"github_user"`
	OpenCollectiveSlug string            `yaml:"opencollective_slug"`
	Tiers              []Tier            `yaml:"tiers"`
	Aliases            map[string]string `yaml:"aliases"`          // source login -> target login
	ExternalSponsors   []ExternalSponsor `yaml:"external_sponsors"` // manually managed entries
}

// loadConfig reads a Config from path.
// If path is empty the embedded default sponsors.yml is used.
func loadConfig(path string) (*Config, error) {
	data := defaultConfigData
	if path != "" {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Highest monthly_rate first so assignTier matches greedily from the top.
	sort.Slice(cfg.Tiers, func(i, j int) bool {
		return cfg.Tiers[i].MonthlyRate > cfg.Tiers[j].MonthlyRate
	})
	return &cfg, nil
}

// assignTier returns the ID of the highest tier whose monthly_rate the sponsor
// meets. Returns an empty string when no tier matches.
func assignTier(tiers []Tier, monthlyUSD float64) string {
	for _, t := range tiers {
		if monthlyUSD >= t.MonthlyRate {
			return t.ID
		}
	}
	return ""
}
