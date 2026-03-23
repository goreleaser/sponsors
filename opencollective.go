package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const ocGraphQLURL = "https://api.opencollective.com/graphql/v2"

// fetchOCSponsors returns active sponsors for the given OpenCollective collective.
// Recurring (monthly/yearly) and recent one-time contributions are included.
// One-time and yearly amounts are normalised to a monthly figure.
func fetchOCSponsors(slug string) ([]rawSponsor, error) {
	const query = `
	query($slug: String!) {
	  collective(slug: $slug) {
	    members(role: BACKER) {
	      nodes {
	        account {
	          name
	          slug
	          website
	          imageUrl
	        }
	        tier {
	          amount { value }
	          frequency
	        }
	        totalDonations { value }
	        since
	        isActive
	      }
	    }
	  }
	}`

	payload, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": map[string]any{"slug": slug},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, ocGraphQLURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "goreleaser-sponsors")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencollective request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencollective: unexpected status %d", resp.StatusCode)
	}

	type member struct {
		Account struct {
			Name     string `json:"name"`
			Slug     string `json:"slug"`
			Website  string `json:"website"`
			ImageURL string `json:"imageUrl"`
		} `json:"account"`
		Tier *struct {
			Amount    struct{ Value float64 `json:"value"` } `json:"amount"`
			Frequency string                                 `json:"frequency"`
		} `json:"tier"`
		TotalDonations struct{ Value float64 `json:"value"` } `json:"totalDonations"`
		Since          string `json:"since"`
		IsActive       bool   `json:"isActive"`
	}
	var result struct {
		Data struct {
			Collective struct {
				Members struct {
					Nodes []member `json:"nodes"`
				} `json:"members"`
			} `json:"collective"`
		} `json:"data"`
		Errors []struct{ Message string `json:"message"` } `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("opencollective: decode response: %w", err)
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("opencollective graphql error: %s", result.Errors[0].Message)
	}

	oneYearAgo := time.Now().AddDate(-1, 0, 0)
	seen := map[string]float64{} // slug -> highest monthly amount seen so far
	var sponsors []rawSponsor

	for _, m := range result.Data.Collective.Members.Nodes {
		if !m.IsActive || m.TotalDonations.Value <= 0 || m.Account.Name == "" || m.Tier == nil {
			continue
		}
		// Skip anonymous/guest accounts — OC uses "Guest" and "Incognito" as
		// placeholder names for contributors who have no public profile.
		switch m.Account.Name {
		case "Guest", "Incognito":
			continue
		}
		if m.Account.Slug == "" || m.Account.Website == "" || m.Account.ImageURL == "" {
			continue
		}

		monthly := m.Tier.Amount.Value
		switch m.Tier.Frequency {
		case "MONTHLY":
			// use as-is
		case "YEARLY":
			monthly /= 12
		case "ONETIME":
			since, err := parseTime(m.Since)
			if err != nil || since.Before(oneYearAgo) {
				continue
			}
			monthly /= 12
		default:
			continue
		}

		if monthly <= 0 {
			continue
		}

		slug := m.Account.Slug
		// Deduplicate within OpenCollective: keep the highest amount.
		if prev, ok := seen[slug]; ok && prev >= monthly {
			continue
		}
		seen[slug] = monthly
		for i, s := range sponsors {
			if s.id == slug {
				sponsors = append(sponsors[:i], sponsors[i+1:]...)
				break
			}
		}

		sponsors = append(sponsors, rawSponsor{
			id:         slug,
			name:       m.Account.Name,
			source:     "opencollective",
			website:    m.Account.Website,
			image:      m.Account.ImageURL,
			monthlyUSD: monthly,
		})
	}

	return sponsors, nil
}

// parseTime attempts to parse an ISO 8601 timestamp as returned by the
// OpenCollective API, trying RFC3339Nano before RFC3339 as a fallback.
func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}
