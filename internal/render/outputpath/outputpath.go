// Package outputpath plans collision-resistant renderer output paths.
package outputpath

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	maxSlugRunes = 40
	hashLength   = 10
)

// StableSlug returns a filesystem-safe, rune-aware label with a stable hash
// derived from the complete, unmodified identity. The hash prevents distinct
// identities which sanitize to the same label from overwriting each other.
func StableSlug(identity string) string {
	var builder strings.Builder
	lastSeparator := false
	for _, character := range identity {
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
	directory, base, extension := splitOutput(baseOutput, defaultBase)
	paths := make([]string, len(identities))
	for index, identity := range identities {
		paths[index] = filepath.Join(
			directory, fmt.Sprintf("%s_%s%s", base, StableSlug(identity), extension),
		)
	}
	if err := RejectDuplicates(paths); err != nil {
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
	if err := RejectDuplicates(flat); err != nil {
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
			return fmt.Errorf("duplicate planned output path %q (also planned as %q)", path, previous)
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
