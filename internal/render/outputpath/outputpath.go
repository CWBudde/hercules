// Package outputpath plans collision-resistant renderer output paths.
package outputpath

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	maxSlugRunes = 40
	hashLength   = 10
)

var (
	errFanoutInputLength   = errors.New("fan-out labels and identities differ in length")
	errDuplicateOutputPath = errors.New("duplicate planned output path")
)

// StableSlug returns a filesystem-safe, rune-aware label with a stable hash
// derived from the complete, unmodified identity. The hash prevents distinct
// identities which sanitize to the same label from overwriting each other.
func StableSlug(identity string) string {
	return StableLabeledSlug(identity, identity)
}

// StableLabeledSlug returns a filesystem-safe, rune-aware public label with a
// stable hash derived from the complete, unmodified identity. This allows
// callers to keep private identity aliases out of filenames without losing the
// collision resistance of the complete identity.
func StableLabeledSlug(publicLabel, identity string) string {
	var builder strings.Builder
	lastSeparator := false

	for _, character := range publicLabel {
		switch {
		case unicode.IsLetter(character), unicode.IsNumber(character):
			builder.WriteRune(character)

			lastSeparator = false
		case !lastSeparator && builder.Len() > 0:
			builder.WriteByte('-')

			lastSeparator = true
		}
	}

	label := strings.Trim(builder.String(), "-")
	if label == "" {
		label = "item"
	}

	runes := []rune(label)
	if len(runes) > maxSlugRunes {
		label = string(runes[:maxSlugRunes])
	}

	digest := sha256.Sum256([]byte(identity))

	return label + "-" + hex.EncodeToString(digest[:])[:hashLength]
}

// FanoutPaths plans one sibling output per identity using baseOutput as the
// requested basename. When baseOutput is empty, defaultBase and .png are used.
// Duplicate planned paths are rejected before a renderer writes any files.
func FanoutPaths(baseOutput, defaultBase string, identities []string) ([]string, error) {
	return FanoutLabeledPaths(baseOutput, defaultBase, identities, identities)
}

// FanoutLabeledPaths plans one sibling output per identity using a public label
// for the readable slug and the full identity only for its stable hash.
func FanoutLabeledPaths(
	baseOutput, defaultBase string,
	labels, identities []string,
) ([]string, error) {
	if len(labels) != len(identities) {
		return nil, fmt.Errorf(
			"%w: %d != %d",
			errFanoutInputLength,
			len(labels), len(identities),
		)
	}

	directory, base, extension := splitOutput(baseOutput, defaultBase)

	paths := make([]string, len(identities))
	for index, identity := range identities {
		paths[index] = filepath.Join(
			directory,
			fmt.Sprintf("%s_%s%s", base, StableLabeledSlug(labels[index], identity), extension),
		)
	}

	err := RejectDuplicates(paths)
	if err != nil {
		return nil, err
	}

	return paths, nil
}

// AssetFanoutPaths plans a set of extensions for every identity in an asset
// directory, rejecting duplicate destinations before rendering begins.
func AssetFanoutPaths(
	directory, base string, identities, extensions []string,
) ([][]string, error) {
	paths := make([][]string, len(identities))

	flat := make([]string, 0, len(identities)*len(extensions))
	for index, identity := range identities {
		stem := filepath.Join(directory, base+"_"+StableSlug(identity))

		paths[index] = make([]string, len(extensions))
		for extensionIndex, extension := range extensions {
			if extension != "" && !strings.HasPrefix(extension, ".") {
				extension = "." + extension
			}

			path := stem + extension
			paths[index][extensionIndex] = path
			flat = append(flat, path)
		}
	}

	err := RejectDuplicates(flat)
	if err != nil {
		return nil, err
	}

	return paths, nil
}

// RejectDuplicates checks planned paths using a case-folded, cleaned spelling
// so plans are also safe on common case-insensitive filesystems.
func RejectDuplicates(paths []string) error {
	seen := make(map[string]string, len(paths))
	for _, path := range paths {
		key := strings.ToLower(filepath.Clean(path))
		if previous, exists := seen[key]; exists {
			return fmt.Errorf("%w %q (also planned as %q)", errDuplicateOutputPath, path, previous)
		}

		seen[key] = path
	}

	return nil
}

func splitOutput(output, defaultBase string) (string, string, string) {
	if output == "" {
		return ".", defaultBase, ".png"
	}

	directory := filepath.Dir(output)

	extension := filepath.Ext(output)
	if extension == "" {
		extension = ".png"
	}

	base := strings.TrimSuffix(filepath.Base(output), filepath.Ext(output))

	return directory, base, extension
}
