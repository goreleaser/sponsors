package main

// Tier defines a sponsorship tier and its minimum monthly contribution threshold.
type Tier struct {
	ID          string  `yaml:"id"           json:"id"`
	Name        string  `yaml:"name"         json:"name"`
	MonthlyRate float64 `yaml:"monthly_rate" json:"monthly_rate"`
}

// Sponsor represents a single sponsor from any platform.
type Sponsor struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Source  string `json:"source"` // "github" or "opencollective"
	Website string `json:"website"`
	Image   string `json:"image"`
	Tier    string `json:"tier"` // tier ID, e.g. "platinum"
}

// SponsorFile is the structure written by generate and read by apply.
type SponsorFile struct {
	Tiers    []Tier    `json:"tiers"`
	Sponsors []Sponsor `json:"sponsors"`
}

// TemplateData is the value passed to templates in the apply command.
type TemplateData struct {
	Sponsors []Sponsor            // all sponsors, sorted by tier rank then name
	Tiers    []Tier               // tiers sorted by monthly_rate descending
	ByTier   map[string][]Sponsor // sponsors grouped by tier ID
}

// ExternalSponsor is a manually configured sponsor entry. It uses the same
// fields as Sponsor plus an optional end_date (YYYY-MM-DD). Entries whose
// end_date is in the past are silently ignored.
type ExternalSponsor struct {
	Sponsor  `yaml:",inline"`
	EndDate string `yaml:"end_date" json:"end_date,omitempty"`
}

// before tier assignment and deduplication.
type rawSponsor struct {
	id         string
	name       string
	source     string // "github" or "opencollective"
	website    string
	image      string
	monthlyUSD float64
}
