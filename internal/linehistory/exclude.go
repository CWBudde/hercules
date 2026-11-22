package linehistory

import (
	"fmt"
	"path"
)

// ValidateExcludePathPatterns rejects malformed --lines-exclude-paths globs up front.
// IsExcludedPath has to discard path.Match's error per file, so a bad pattern would otherwise
// silently exclude nothing. It is exported so that LegacyBurndownAnalysis, which tracks lines
// itself instead of consuming LineHistory, applies the same exclusion with the same validation.
func ValidateExcludePathPatterns(patterns []string) error {
	for _, pattern := range patterns {
		_, err := path.Match(pattern, "")
		if err != nil {
			return fmt.Errorf("invalid --lines-exclude-paths pattern %q: %w", pattern, err)
		}
	}

	return nil
}

// IsExcludedPath reports whether a change from fromName to toName touches a path matched by one
// of the patterns. Either side matching excludes the whole change, so a rename into or out of an
// excluded path is skipped as well. Empty names (the missing side of an insertion or deletion)
// never match.
func IsExcludedPath(patterns []string, fromName, toName string) bool {
	for _, pattern := range patterns {
		if fromName != "" {
			if ok, _ := path.Match(pattern, fromName); ok {
				return true
			}
		}

		if toName != "" {
			if ok, _ := path.Match(pattern, toName); ok {
				return true
			}
		}
	}

	return false
}
