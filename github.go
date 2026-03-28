package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	githubGraphQLURL = "https://api.github.com/graphql"

	// githubEntityFields is the GraphQL field list for both User and Organization.
	githubEntityFields = `login name url avatarUrl websiteUrl`
)

// githubEntity holds the common profile fields for a GitHub User or Organisation.
type githubEntity struct {
	Login      string `json:"login"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	WebsiteURL string `json:"websiteUrl"`
	AvatarURL  string `json:"avatarUrl"`
}

func (e githubEntity) toUserInfo() UserInfo {
	return UserInfo{
		Name:    cmp.Or(e.Name, e.Login),
		Login:   e.Login,
		Website: cmp.Or(e.WebsiteURL, e.URL),
		Image:   e.AvatarURL,
	}
}

func (e githubEntity) toRawSponsor(source string, monthly float64) rawSponsor {
	return rawSponsor{
		id:         e.Login,
		name:       cmp.Or(e.Name, e.Login),
		source:     source,
		website:    cmp.Or(e.WebsiteURL, e.URL),
		image:      e.AvatarURL,
		monthlyUSD: monthly,
	}
}

type githubClient struct {
	token  string
	client *http.Client
}

func newGithubClient(token string) *githubClient {
	return &githubClient{
		token:  token,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *githubClient) graphql(query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, githubGraphQLURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "goreleaser-sponsors")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("github graphql request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github graphql: unexpected status %d", resp.StatusCode)
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("github graphql: decode response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		msgs := make([]string, len(envelope.Errors))
		for i, e := range envelope.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("github graphql errors: %s", strings.Join(msgs, "; "))
	}
	return json.Unmarshal(envelope.Data, out)
}

// fetchSponsors returns active, public sponsors for the given GitHub user.
// One-time sponsorships from the past year are included with their amount
// divided by 12 to normalise to a monthly figure.
func (c *githubClient) fetchSponsors(user string) ([]rawSponsor, error) {
	const query = `
	query($user: String!, $cursor: String) {
	  user(login: $user) {
	    sponsorshipsAsMaintainer(first: 100, after: $cursor, activeOnly: true) {
	      pageInfo { hasNextPage endCursor }
	      nodes {
	        sponsorEntity {
	          ... on User         { ` + githubEntityFields + ` }
	          ... on Organization { ` + githubEntityFields + ` }
	        }
	        tier { monthlyPriceInDollars isOneTime }
	        privacyLevel
	        createdAt
	      }
	    }
	  }
	}`

	type pageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	}
	type node struct {
		SponsorEntity githubEntity `json:"sponsorEntity"`
		Tier          struct {
			MonthlyPriceInDollars float64 `json:"monthlyPriceInDollars"`
			IsOneTime             bool    `json:"isOneTime"`
		} `json:"tier"`
		PrivacyLevel string `json:"privacyLevel"`
		CreatedAt    string `json:"createdAt"`
	}
	type result struct {
		User struct {
			SponsorshipsAsMaintainer struct {
				PageInfo pageInfo `json:"pageInfo"`
				Nodes    []node   `json:"nodes"`
			} `json:"sponsorshipsAsMaintainer"`
		} `json:"user"`
	}

	oneYearAgo := time.Now().AddDate(-1, 0, 0)
	seen := map[string]float64{} // login -> highest monthly amount seen so far
	var sponsors []rawSponsor
	var cursor *string

	for {
		vars := map[string]any{"user": user}
		if cursor != nil {
			vars["cursor"] = *cursor
		}
		var r result
		if err := c.graphql(query, vars, &r); err != nil {
			return nil, err
		}

		for _, n := range r.User.SponsorshipsAsMaintainer.Nodes {
			if n.PrivacyLevel != "PUBLIC" || n.SponsorEntity.Login == "" {
				continue
			}

			monthly := n.Tier.MonthlyPriceInDollars
			if n.Tier.IsOneTime {
				createdAt, err := time.Parse(time.RFC3339, n.CreatedAt)
				if err != nil || createdAt.Before(oneYearAgo) {
					continue
				}
				monthly /= 12
			}
			if monthly <= 0 {
				continue
			}

			login := n.SponsorEntity.Login
			// Deduplicate within GitHub: keep the entry with the highest amount.
			if prev, ok := seen[login]; ok && prev >= monthly {
				continue
			}
			seen[login] = monthly
			for i, s := range sponsors {
				if s.id == login {
					sponsors = append(sponsors[:i], sponsors[i+1:]...)
					break
				}
			}

			sponsors = append(sponsors, n.SponsorEntity.toRawSponsor("github", monthly))
		}

		pi := r.User.SponsorshipsAsMaintainer.PageInfo
		if !pi.HasNextPage {
			break
		}
		cursor = &pi.EndCursor
	}

	return sponsors, nil
}

// UserInfo holds public profile data for a GitHub user or organisation.
type UserInfo struct {
	Name    string
	Login   string
	Website string
	Image   string
}

// FetchUserInfo fetches public profile data for the given login via GraphQL.
// It tries User first, then Organisation, to handle both account types.
func (c *githubClient) FetchUserInfo(login string) (UserInfo, error) {
	const query = `
	query($login: String!) {
	  user(login: $login) { ` + githubEntityFields + ` }
	  organization(login: $login) { ` + githubEntityFields + ` }
	}`

	var result struct {
		User         *githubEntity `json:"user"`
		Organization *githubEntity `json:"organization"`
	}

	if err := c.graphql(query, map[string]any{"login": login}, &result); err != nil {
		return UserInfo{}, fmt.Errorf("fetch user info %q: %w", login, err)
	}

	switch {
	case result.User != nil:
		return result.User.toUserInfo(), nil
	case result.Organization != nil:
		return result.Organization.toUserInfo(), nil
	default:
		return UserInfo{}, fmt.Errorf("fetch user info %q: not found", login)
	}
}
