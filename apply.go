package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/caarlos0/log"
	"github.com/spf13/cobra"
)

const (
	defaultBeginMarker = "<!-- sponsors:begin -->"
	defaultEndMarker   = "<!-- sponsors:end -->"
)

func newApplyCmd() *cobra.Command {
	var beginMarker, endMarker string

	cmd := &cobra.Command{
		Use:   "apply sponsors.json template.tpl output.md",
		Short: "Render a template and update a file between markers",
		Long: `Render a Go template using the sponsors data from the given JSON file,
then replace the content between the begin and end markers in the output file.

The template receives a TemplateData value with the following fields:
  .Sponsors  []Sponsor            — all sponsors, sorted by tier rank then name
  .Tiers     []Tier               — tiers sorted by monthly_rate descending
  .ByTier    map[string][]Sponsor — sponsors grouped by tier ID

Template functions:
  imageURL url size  — append a size hint to a GitHub or OpenCollective avatar URL`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return apply(args[0], args[1], args[2], beginMarker, endMarker)
		},
	}
	cmd.Flags().StringVar(&beginMarker, "begin-marker", defaultBeginMarker, "begin marker in the target file")
	cmd.Flags().StringVar(&endMarker, "end-marker", defaultEndMarker, "end marker in the target file")
	return cmd
}

func apply(sponsorsPath, templatePath, outputPath, beginMarker, endMarker string) error {
	sfData, err := os.ReadFile(sponsorsPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", sponsorsPath, err)
	}
	var sf SponsorFile
	if err := json.Unmarshal(sfData, &sf); err != nil {
		return fmt.Errorf("parse %s: %w", sponsorsPath, err)
	}

	byTier := make(map[string][]Sponsor)
	for _, s := range sf.Sponsors {
		byTier[s.Tier] = append(byTier[s.Tier], s)
	}

	data := TemplateData{
		Sponsors: sf.Sponsors,
		Tiers:    sf.Tiers,
		ByTier:   byTier,
	}

	tmplSrc, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("read template %s: %w", templatePath, err)
	}
	funcMap := template.FuncMap{
		"imageURL": sizedImageURL,
	}
	tmpl, err := template.New("sponsors").Funcs(funcMap).Parse(string(tmplSrc))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", templatePath, err)
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	existing, err := os.ReadFile(outputPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", outputPath, err)
	}
	updated, err := replaceMarkers(string(existing), beginMarker, endMarker, rendered.String())
	if err != nil {
		return fmt.Errorf("update %s: %w", outputPath, err)
	}
	if err := os.WriteFile(outputPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	log.WithField("output", outputPath).Info("updated file")
	return nil
}

// replaceMarkers replaces the content between begin and end markers.
func replaceMarkers(content, begin, end, replacement string) (string, error) {
	startIdx := strings.Index(content, begin)
	if startIdx == -1 {
		return "", fmt.Errorf("begin marker %q not found", begin)
	}
	endIdx := strings.Index(content[startIdx:], end)
	if endIdx == -1 {
		return "", fmt.Errorf("end marker %q not found", end)
	}
	endIdx += startIdx
	return content[:startIdx+len(begin)] + "\n" + replacement + "\n" + content[endIdx:], nil
}

// sizedImageURL appends a size hint understood by GitHub and OpenCollective CDNs.
// Use it in templates: {{ imageURL .Image 128 }}
func sizedImageURL(url string, size int) string {
	if url == "" {
		return url
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	switch {
	case strings.Contains(url, "avatars.githubusercontent.com"):
		return fmt.Sprintf("%s%ss=%d", url, sep, size)
	case strings.Contains(url, "images.opencollective.com"):
		return fmt.Sprintf("%s%sheight=%d", url, sep, size)
	default:
		return url
	}
}
