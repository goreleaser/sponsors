package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newGenerateCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "generate output.json",
		Short: "Fetch sponsors from GitHub and OpenCollective",
		Long: `Fetch all active sponsors from GitHub Sponsors and OpenCollective,
apply any configured aliases and tier overrides, then write the result
to a JSON file suitable for use with the apply command.

Requires the GITHUB_TOKEN environment variable to be set.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return generate(configPath, args[0])
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "path to config YAML (default: built-in sponsors.yml)")
	return cmd
}

func generate(configPath, outputPath string) error {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN environment variable is required")
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	gh := newGithubClient(token)

	// Resolve aliases: fetch real account info for each unique target login.
	type resolvedInfo struct {
		name    string
		id      string
		website string
		image   string
	}
	resolvedTargets := map[string]resolvedInfo{}
	for _, target := range cfg.Aliases {
		if _, ok := resolvedTargets[target]; ok {
			continue
		}
		fmt.Printf("Resolving alias target %q...\n", target)
		info, err := gh.FetchUserInfo(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %v — using login as fallback\n", err)
			resolvedTargets[target] = resolvedInfo{
				name:    target,
				id:      target,
				website: "https://github.com/" + target,
				image:   "https://github.com/" + target + ".png",
			}
		} else {
			resolvedTargets[target] = resolvedInfo{
				name:    info.Name,
				id:      info.Login,
				website: info.Website,
				image:   info.Image,
			}
		}
	}
	aliasLookup := map[string]resolvedInfo{}
	for source, target := range cfg.Aliases {
		aliasLookup[strings.ToLower(source)] = resolvedTargets[target]
	}

	var all []rawSponsor

	fmt.Printf("Fetching OpenCollective sponsors (%s)...\n", cfg.OpenCollectiveSlug)
	ocSponsors, err := fetchOCSponsors(cfg.OpenCollectiveSlug)
	if err != nil {
		return fmt.Errorf("opencollective: %w", err)
	}
	fmt.Printf("  Found %d sponsor(s)\n", len(ocSponsors))
	if len(ocSponsors) == 0 {
		return fmt.Errorf("no sponsors from OpenCollective — API may not be responding correctly")
	}
	all = append(all, ocSponsors...)

	fmt.Printf("Fetching GitHub sponsors (%s)...\n", cfg.GitHubUser)
	ghSponsors, err := gh.fetchSponsors(cfg.GitHubUser)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	fmt.Printf("  Found %d sponsor(s)\n", len(ghSponsors))
	if len(ghSponsors) == 0 {
		return fmt.Errorf("no sponsors from GitHub — API may not be responding correctly or token lacks permissions")
	}
	all = append(all, ghSponsors...)

	// Apply aliases.
	for i, s := range all {
		if info, ok := aliasLookup[strings.ToLower(s.id)]; ok {
			all[i].name = info.name
			all[i].id = info.id
			all[i].website = info.website
			all[i].image = info.image
		}
	}

	// Deduplicate by ID across sources, keeping the highest monthly amount.
	seenIdx := map[string]int{}
	var deduped []rawSponsor
	for _, s := range all {
		key := strings.ToLower(s.id)
		if idx, ok := seenIdx[key]; ok {
			if s.monthlyUSD > deduped[idx].monthlyUSD {
				deduped[idx] = s
			}
			continue
		}
		seenIdx[key] = len(deduped)
		deduped = append(deduped, s)
	}

	// Assign tiers to fetched sponsors.
	tierRank := map[string]int{}
	for i, t := range cfg.Tiers {
		tierRank[strings.ToLower(t.ID)] = i
	}
	var sponsors []Sponsor
	for _, s := range deduped {
		tier := assignTier(cfg.Tiers, s.monthlyUSD)
		if tier == "" {
			continue
		}
		sponsors = append(sponsors, Sponsor{
			Name:    s.name,
			ID:      s.id,
			Source:  s.source,
			Website: s.website,
			Image:   s.image,
			Tier:    tier,
		})
	}

	// Merge external sponsors: valid entries override any fetched sponsor with
	// the same ID, or are appended if no match exists.
	today := time.Now().Truncate(24 * time.Hour)
	for _, ext := range cfg.ExternalSponsors {
		if ext.EndDate != "" {
			expiry, err := time.Parse("2006-01-02", ext.EndDate)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: invalid end_date %q for external sponsor %q, skipping\n", ext.EndDate, ext.ID)
				continue
			}
			if today.After(expiry) {
				continue // expired
			}
		}
		replaced := false
		for i, s := range sponsors {
			if strings.EqualFold(s.ID, ext.ID) {
				sponsors[i] = ext.Sponsor
				replaced = true
				break
			}
		}
		if !replaced {
			sponsors = append(sponsors, ext.Sponsor)
		}
	}


	// Sort: highest tier first, then alphabetically by name.
	sort.Slice(sponsors, func(i, j int) bool {
		ri, rj := tierRank[sponsors[i].Tier], tierRank[sponsors[j].Tier]
		if ri != rj {
			return ri < rj
		}
		return sponsors[i].Name < sponsors[j].Name
	})

	fmt.Println("\nTier summary:")
	counts := map[string]int{}
	for _, s := range sponsors {
		counts[s.Tier]++
	}
	for _, t := range cfg.Tiers {
		if n := counts[strings.ToLower(t.ID)]; n > 0 {
			fmt.Printf("  %s: %d\n", t.Name, n)
		}
	}

	sf := SponsorFile{Tiers: cfg.Tiers, Sponsors: sponsors}
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sponsors: %w", err)
	}
	if err := os.WriteFile(outputPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	fmt.Printf("\n✓ Wrote %d sponsor(s) to %s\n", len(sponsors), outputPath)
	return nil
}
