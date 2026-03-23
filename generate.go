package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/caarlos0/log"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
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
		Args:    cobra.MaximumNArgs(1),
		Example: "# Output to file:\nsponsors generate out.json\n# Output to STDOUT:\nsponsors generate -\n",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				args = []string{"-"}
			}
			return generate(configPath, args[0])
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "sponsors.yml", "path to config YAML")
	return cmd
}

func generate(configPath, output string) error {
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
	if len(cfg.Aliases) == 0 {
		log.Warn("no aliases configured")
	} else {
		log.Infof("configured %d alias(es)", len(cfg.Aliases))
	}
	resolvedTargets := map[string]resolvedInfo{}
	for _, target := range cfg.Aliases {
		if _, ok := resolvedTargets[target]; ok {
			continue
		}
		log.WithField("target", target).Info("resolving alias target")
		info, err := gh.FetchUserInfo(target)
		if err != nil {
			log.WithError(err).Warn("using login as fallback")
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
	var mu sync.Mutex
	var wg errgroup.Group

	wg.Go(func() error {
		log := log.
			WithField("source", "opencollective").
			WithField("slug", cfg.OpenCollectiveSlug)
		log.Info("fetching sponsors")
		ocSponsors, err := fetchOCSponsors(cfg.OpenCollectiveSlug)
		if err != nil {
			return fmt.Errorf("opencollective: %w", err)
		}
		log.Infof("found %d sponsor(s)", len(ocSponsors))
		if len(ocSponsors) == 0 {
			return fmt.Errorf("no sponsors from OpenCollective — API may not be responding correctly")
		}
		mu.Lock()
		all = append(all, ocSponsors...)
		mu.Unlock()
		return nil
	})

	wg.Go(func() error {
		log := log.
			WithField("source", "github").
			WithField("login", cfg.GitHubUser)
		log.Info("fetching")
		ghSponsors, err := gh.fetchSponsors(cfg.GitHubUser)
		if err != nil {
			return fmt.Errorf("github: %w", err)
		}
		log.Infof("found %d sponsor(s)", len(ghSponsors))
		if len(ghSponsors) == 0 {
			return fmt.Errorf("no sponsors from GitHub — API may not be responding correctly or token lacks permissions")
		}
		mu.Lock()
		all = append(all, ghSponsors...)
		mu.Unlock()
		return nil
	})

	if err := wg.Wait(); err != nil {
		return err
	}

	// Apply aliases.
	applied := map[string]bool{}
	for i, s := range all {
		if info, ok := aliasLookup[strings.ToLower(s.id)]; ok {
			log.WithField("from", s.id).WithField("to", info.id).Info("applying alias")
			all[i].name = info.name
			all[i].id = info.id
			all[i].website = info.website
			all[i].image = info.image
			applied[strings.ToLower(s.id)] = true
		}
	}
	for source := range aliasLookup {
		if !applied[source] {
			log.WithField("source", source).Warn("alias source not found among fetched sponsors")
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
			Name:       s.name,
			ID:         s.id,
			Source:     s.source,
			Website:    s.website,
			Image:      s.image,
			Tier:       tier,
			MonthlyUSD: s.monthlyUSD,
		})
	}

	// Merge external sponsors: valid entries override any fetched sponsor with
	// the same ID, or are appended if no match exists.
	today := time.Now().Truncate(24 * time.Hour)
	for _, ext := range cfg.ExternalSponsors {
		if ext.EndDate != "" {
			expiry, err := time.Parse("2006-01-02", ext.EndDate)
			if err != nil {
				log.
					WithField("end_date", ext.EndDate).
					WithField("id", ext.ID).
					Warn("invalid end_date format, skipping external sponsor")
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

	// Sort: highest tier first, then highest monthly contribution, then alphabetically.
	sort.Slice(sponsors, func(i, j int) bool {
		ri, rj := tierRank[sponsors[i].Tier], tierRank[sponsors[j].Tier]
		if ri != rj {
			return ri < rj
		}
		if sponsors[i].MonthlyUSD != sponsors[j].MonthlyUSD {
			return sponsors[i].MonthlyUSD > sponsors[j].MonthlyUSD
		}
		return strings.ToLower(sponsors[i].Name) < strings.ToLower(sponsors[j].Name)
	})

	sf := SponsorFile{Tiers: cfg.Tiers, Sponsors: sponsors}
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sponsors: %w", err)
	}
	if output == "-" {
		if _, err := os.Stdout.Write(data); err != nil {
			return fmt.Errorf("write to STDOUT: %w", err)
		}
		log.Infof("wrote %d sponsor(s) to STDOUT", len(sponsors))
		return nil
	}
	if err := os.WriteFile(output, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", output, err)
	}
	log.Infof("wrote %d sponsor(s) to %s", len(sponsors), output)
	return nil
}
