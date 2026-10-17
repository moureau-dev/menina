package manifest

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Image returns the full image reference for a build target.
func (t Target) Image() string {
	if t.Tag == "" {
		return t.Repository
	}
	return t.Repository + ":" + t.Tag
}

// FilterTargets selects targets by comma-separated names or globs. An empty
// filter selects every target. Unknown names are ignored so CI can pass a
// computed list that may include apps this manifest does not build.
func FilterTargets(targets []Target, filter string) ([]Target, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return targets, nil
	}
	var patterns []string
	for _, part := range strings.Split(filter, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, err := filepath.Match(part, "x"); err != nil {
			return nil, fmt.Errorf("invalid filter %q: %w", part, err)
		}
		patterns = append(patterns, part)
	}
	if len(patterns) == 0 {
		return targets, nil
	}
	seen := map[string]bool{}
	var out []Target
	for _, t := range targets {
		if seen[t.Image()] {
			continue
		}
		for _, pattern := range patterns {
			match, _ := filepath.Match(pattern, t.Name)
			if match || pattern == t.Name {
				seen[t.Image()] = true
				out = append(out, t)
				break
			}
		}
	}
	return out, nil
}
