package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	githubGraphQLURL = "https://api.github.com/graphql"
	githubRESTURL    = "https://api.github.com"
)

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
	          ... on User         { login name url avatarUrl }
	          ... on Organization { login name url avatarUrl }
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
		SponsorEntity struct {
			Login     string `json:"login"`
			Name      string `json:"name"`
			URL       string `json:"url"`
			AvatarURL string `json:"avatarUrl"`
		} `json:"sponsorEntity"`
		Tier struct {
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

			name := n.SponsorEntity.Name
			if name == "" {
				name = login
			}
			sponsors = append(sponsors, rawSponsor{
				id:         login,
				name:       name,
				source:     "github",
				website:    n.SponsorEntity.URL,
				image:      n.SponsorEntity.AvatarURL,
				monthlyUSD: monthly,
			})
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

// FetchUserInfo fetches public profile data for the given login via the REST API.
func (c *githubClient) FetchUserInfo(login string) (UserInfo, error) {
	req, err := http.NewRequest(http.MethodGet, githubRESTURL+"/users/"+login, nil)
	if err != nil {
		return UserInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "goreleaser-sponsors")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.client.Do(req)
	if err != nil {
		return UserInfo{}, fmt.Errorf("fetch user info %q: %w", login, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UserInfo{}, fmt.Errorf("fetch user info %q: status %d", login, resp.StatusCode)
	}

	var u struct {
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
		Blog      string `json:"blog"`
		HTMLURL   string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return UserInfo{}, fmt.Errorf("decode user info %q: %w", login, err)
	}

	name := u.Name
	if name == "" {
		name = u.Login
	}
	website := u.Blog
	if website == "" {
		website = u.HTMLURL
	}
	return UserInfo{Name: name, Login: u.Login, Website: website, Image: u.AvatarURL}, nil
}
