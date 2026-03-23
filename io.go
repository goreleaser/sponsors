package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// readFileOrURL reads data from a local file path, an HTTP/HTTPS URL, or a
// GitHub shorthand of the form gh://owner/repo/path/to/file (resolved to the
// default branch via the raw.githubusercontent.com CDN).
func readFileOrURL(path string) ([]byte, error) {
	if rest, ok := strings.CutPrefix(path, "gh://"); ok {
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 3 {
			return nil, fmt.Errorf("gh: shorthand must be gh:owner/repo/path, got %q", path)
		}
		path = "https://raw.githubusercontent.com/" + parts[0] + "/" + parts[1] + "/HEAD/" + parts[2]
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(path)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch %s: unexpected status %d", path, resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return data, nil
	}
	return os.ReadFile(path)
}
