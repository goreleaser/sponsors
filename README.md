# sponsors

Fetches sponsors from [GitHub Sponsors](https://github.com/sponsors) and [OpenCollective](https://opencollective.com) and renders them into your files via Go templates.

## Install

```sh
go install github.com/goreleaser/sponsors@latest
```

## Usage

```sh
# Fetch sponsors and write sponsors.json (requires GITHUB_TOKEN)
sponsors generate --config sponsors.yml sponsors.json

# Render a template between <!-- sponsors:begin --> / <!-- sponsors:end --> markers
sponsors apply sponsors.json readme.tpl.md README.md
```

`--config` defaults to `sponsors.yml` in the current directory.

## Config

```yaml
github_user: caarlos0
opencollective_slug: goreleaser

tiers:
  - id: gold
    name: Gold
    monthly_rate: 100
  - id: backer
    name: Backer
    monthly_rate: 0

# Redirect a sponsor's login to another account (e.g. individual → org).
# The target's real name, avatar and website are fetched from GitHub.
aliases:
  johndoe: acme-corp

# Manually managed entries; those past end_date are silently ignored.
external_sponsors:
  - name: Acme Corp
    id: acme-corp
    source: github
    website: https://acme.com
    image: https://github.com/acme-corp.png
    tier: gold
    end_date: "2027-03-23"
```

## Templates

Templates receive `.Sponsors`, `.Tiers`, and `.ByTier` (sponsors keyed by tier ID).
A `dict` helper and `Sponsor.LogoWithSize(size int)` are available:

```
{{- define "tier" -}}
{{- if $s := index . "sponsors" }}
{{- range $s }}<a href="{{ .Website }}"><img src="{{ .LogoWithSize (index $ "size") }}" /></a>{{ end }}
{{- end -}}
{{- end -}}
{{- template "tier" (dict "size" 96 "sponsors" (index .ByTier "gold")) -}}
```
