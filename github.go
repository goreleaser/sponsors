package main

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

// githubEntity holds the common profile fields for a GitHub User or Organisation.
type githubEntity struct {
	Login      string
	Name       string
	URL        string `graphql:"url"`
	WebsiteURL string `graphql:"websiteUrl"`
	AvatarURL  string `graphql:"avatarUrl"`
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
	client *githubv4.Client
}

func newGithubClient(token string) *githubClient {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(context.Background(), src)
	return &githubClient{client: githubv4.NewClient(httpClient)}
}

// fetchSponsors returns active, public sponsors for the given GitHub user.
// One-time sponsorships from the past year are included with their amount
// divided by 12 to normalise to a monthly figure.
func (c *githubClient) fetchSponsors(user string) ([]rawSponsor, error) {
	var q struct {
		User struct {
			SponsorshipsAsMaintainer struct {
				PageInfo struct {
					HasNextPage bool
					EndCursor   githubv4.String
				}
				Nodes []struct {
					SponsorEntity struct {
						AsUser         githubEntity `graphql:"... on User"`
						AsOrganization githubEntity `graphql:"... on Organization"`
					}
					Tier struct {
						MonthlyPriceInDollars int
						IsOneTime             bool
					}
					PrivacyLevel string
					CreatedAt    githubv4.DateTime
				}
			} `graphql:"sponsorshipsAsMaintainer(first: 100, after: $cursor, activeOnly: true)"`
		} `graphql:"user(login: $user)"`
	}

	variables := map[string]any{
		"user":   githubv4.String(user),
		"cursor": (*githubv4.String)(nil),
	}

	oneYearAgo := time.Now().AddDate(-1, 0, 0)
	seen := map[string]float64{} // login -> highest monthly amount seen so far
	var sponsors []rawSponsor

	for {
		if err := c.client.Query(context.Background(), &q, variables); err != nil {
			return nil, err
		}

		for _, n := range q.User.SponsorshipsAsMaintainer.Nodes {
			if n.PrivacyLevel != "PUBLIC" {
				continue
			}

			entity := n.SponsorEntity.AsUser
			if entity.Login == "" {
				entity = n.SponsorEntity.AsOrganization
			}
			if entity.Login == "" {
				continue
			}

			monthly := float64(n.Tier.MonthlyPriceInDollars)
			if n.Tier.IsOneTime {
				if n.CreatedAt.Before(oneYearAgo) {
					continue
				}
				monthly /= 12
			}
			if monthly <= 0 {
				continue
			}

			login := entity.Login
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

			sponsors = append(sponsors, entity.toRawSponsor("github", monthly))
		}

		if !q.User.SponsorshipsAsMaintainer.PageInfo.HasNextPage {
			break
		}
		variables["cursor"] = new(q.User.SponsorshipsAsMaintainer.PageInfo.EndCursor)
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
// repositoryOwner resolves both users and organisations without errors.
// ProfileOwner is the interface implemented by both User and Organization.
func (c *githubClient) FetchUserInfo(login string) (UserInfo, error) {
	var q struct {
		RepositoryOwner *struct {
			Login     string
			URL       string `graphql:"url"`
			AvatarURL string `graphql:"avatarUrl"`
			Profile   struct {
				Name       string
				WebsiteURL string `graphql:"websiteUrl"`
			} `graphql:"... on ProfileOwner"`
		} `graphql:"repositoryOwner(login: $login)"`
	}

	if err := c.client.Query(context.Background(), &q, map[string]any{
		"login": githubv4.String(login),
	}); err != nil {
		return UserInfo{}, fmt.Errorf("fetch user info %q: %w", login, err)
	}
	if q.RepositoryOwner == nil {
		return UserInfo{}, fmt.Errorf("fetch user info %q: not found", login)
	}

	o := q.RepositoryOwner
	return UserInfo{
		Name:    cmp.Or(o.Profile.Name, o.Login),
		Login:   o.Login,
		Website: cmp.Or(o.Profile.WebsiteURL, o.URL),
		Image:   o.AvatarURL,
	}, nil
}
