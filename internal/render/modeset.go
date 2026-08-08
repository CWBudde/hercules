package render

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	// errUnknownMode reports a --mode value that is not in validModeNames.
	errUnknownMode = errors.New("unknown mode")
	// errUnsupportedInputFormat reports an --input-format value that is
	// neither auto, yaml, nor pb.
	errUnsupportedInputFormat = errors.New("unsupported input format")
)

var validModeNames = map[string]struct{}{
	ModeAll:                    {},
	ModeBurndown:               {},
	ModeBurndownFile:           {},
	ModeBurndownPerson:         {},
	ModeBurndownProject:        {},
	ModeBurndownRepository:     {},
	ModeBurndownReposCombined:  {},
	ModeBusFactor:              {},
	ModeCouples:                {},
	ModeCouplesFiles:           {},
	ModeCouplesPeople:          {},
	ModeCouplesShotness:        {},
	ModeDevs:                   {},
	ModeDevsEfforts:            {},
	ModeDevsParallel:           {},
	ModeHotspotRisk:            {},
	ModeKnowledgeDiffusion:     {},
	ModeLanguages:              {},
	ModeOldVsNew:               {},
	ModeOverwritesMatrix:       {},
	ModeOwnership:              {},
	ModeOwnershipConcentration: {},
	ModeRefactoringProxy:       {},
	ModeRunTimes:               {},
	ModeSentiment:              {},
	ModeShotness:               {},
	ModeTemporalActivity:       {},
}

var pythonAllModes = []string{
	ModeBurndownProject,
	ModeOverwritesMatrix,
	ModeOwnership,
	ModeCouplesFiles,
	ModeCouplesPeople,
	ModeCouplesShotness,
	ModeShotness,
	ModeDevs,
	ModeDevsEfforts,
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
			return nil, fmt.Errorf("%w: %s", errUnknownMode, mode)
		}

		switch mode {
		case ModeBurndown:
			// Python compatibility: burndown defaults to burndown-project
			resolvedModes = append(resolvedModes, ModeBurndownProject)
		case ModeCouples:
			// Python compatibility: couples runs all coupling analyses
			resolvedModes = append(resolvedModes, ModeCouplesFiles, ModeCouplesPeople, ModeCouplesShotness)
		default:
			resolvedModes = append(resolvedModes, mode)
		}
	}

	modes = resolvedModes

	if contains(modes, ModeAll) {
		// Match Python's "all" mode composition exactly
		modes = append([]string{}, pythonAllModes...)
	}

	return modes, nil
}

func splitModeValues(rawModes []string) []string {
	var modes []string

	for _, raw := range rawModes {
		for part := range strings.SplitSeq(raw, ",") {
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
	return slices.Contains(slice, value)
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
		return "", fmt.Errorf("%w %q: expected auto, yaml, or pb", errUnsupportedInputFormat, inputFormat)
	}
}
