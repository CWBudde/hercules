package render

import (
	"fmt"
	"strings"
)

var validModeNames = map[string]struct{}{
	"all":                     {},
	"burndown":                {},
	"burndown-file":           {},
	"burndown-person":         {},
	"burndown-project":        {},
	"burndown-repository":     {},
	"burndown-repos-combined": {},
	"bus-factor":              {},
	"couples":                 {},
	"couples-files":           {},
	"couples-people":          {},
	"couples-shotness":        {},
	"devs":                    {},
	"devs-efforts":            {},
	"devs-parallel":           {},
	"hotspot-risk":            {},
	"knowledge-diffusion":     {},
	"languages":               {},
	"old-vs-new":              {},
	"overwrites-matrix":       {},
	"ownership":               {},
	"ownership-concentration": {},
	"refactoring-proxy":       {},
	"run-times":               {},
	"sentiment":               {},
	"shotness":                {},
	"temporal-activity":       {},
}

var pythonAllModes = []string{
	"burndown-project",
	"overwrites-matrix",
	"ownership",
	"couples-files",
	"couples-people",
	"couples-shotness",
	"shotness",
	"devs",
	"devs-efforts",
}

// ResolveModes validates and expands raw mode values (which may be repeated
// and/or comma-separated) into the concrete list of modes to execute. It
// applies the Python labours compatibility aliases: "burndown" resolves to
// "burndown-project", "couples" fans out to the three coupling modes, and
// "all" replaces the whole list with the Python "all" mode composition.
// An unknown mode name yields an error.
func ResolveModes(rawModes []string) ([]string, error) {
	modes := splitModeValues(rawModes)
	if len(modes) == 0 {
		return nil, nil
	}

	// Handle mode aliases for Python compatibility
	var resolvedModes []string
	for _, mode := range modes {
		if !isValidMode(mode) {
			return nil, fmt.Errorf("unknown mode: %s", mode)
		}
		switch mode {
		case "burndown":
			// Python compatibility: burndown defaults to burndown-project
			resolvedModes = append(resolvedModes, "burndown-project")
		case "couples":
			// Python compatibility: couples runs all coupling analyses
			resolvedModes = append(resolvedModes, "couples-files", "couples-people", "couples-shotness")
		default:
			resolvedModes = append(resolvedModes, mode)
		}
	}
	modes = resolvedModes

	if contains(modes, "all") {
		// Match Python's "all" mode composition exactly
		modes = append([]string{}, pythonAllModes...)
	}
	return modes, nil
}

func splitModeValues(rawModes []string) []string {
	var modes []string
	for _, raw := range rawModes {
		for _, part := range strings.Split(raw, ",") {
			mode := strings.TrimSpace(part)
			if mode != "" {
				modes = append(modes, mode)
			}
		}
	}
	return modes
}

func isValidMode(mode string) bool {
	_, ok := validModeNames[mode]
	return ok
}

func contains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}

// NormalizeInputFormat canonicalizes an input format hint. An empty value
// normalizes to "auto"; anything other than "auto", "yaml", or "pb" is an
// error.
func NormalizeInputFormat(inputFormat string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(inputFormat))
	if format == "" {
		format = "auto"
	}
	switch format {
	case "auto", "yaml", "pb":
		return format, nil
	default:
		return "", fmt.Errorf("unsupported input format %q: expected auto, yaml, or pb", inputFormat)
	}
}
