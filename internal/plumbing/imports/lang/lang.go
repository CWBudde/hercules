// Package lang implements per-language import extraction over a pure-Go
// tree-sitter runtime (github.com/odvcencio/gotreesitter).
//
// It replaces github.com/src-d/imports + smacker/go-tree-sitter, which were
// abandoned upstream and forced cgo into the default hercules build. The
// public surface intentionally mirrors what the hercules pipeline already
// consumes: File and Extract.
package lang

import (
	"fmt"
	"sort"

	"github.com/src-d/enry/v2"
)

// File mirrors the original src-d/imports.File shape so downstream consumers
// (hercules pipeline, labours JSON) keep working unchanged.
type File struct {
	Path    string   `json:"file"`
	Lang    string   `json:"lang,omitempty"`
	Imports []string `json:"imports,omitempty"`
}

// Language is implemented by per-language extractors.
type Language interface {
	// Aliases returns the enry-style language names this extractor handles
	// (e.g. "Go", "C#"). The first matching alias wins on registration.
	Aliases() []string
	// Imports parses the content and returns a sorted, deduplicated list of
	// imported module/package names.
	Imports(content []byte) ([]string, error)
}

var registry = map[string]Language{
	"C#":   csharpExtractor{},
	"Go":   goExtractor{},
	"Java": javaExtractor{},
}

// RegisterLanguage adds a language extractor to the global registry and
// returns a value suitable for declaration-time registration.
func RegisterLanguage(l Language) struct{} {
	for _, name := range l.Aliases() {
		registry[name] = l
	}

	return struct{}{}
}

// LanguageByName looks up a registered extractor by its enry alias.
func LanguageByName(name string) Language {
	return registry[name]
}

// Extract runs language detection on (path, content) and dispatches to the
// matching extractor, if any.
//
// The function never accesses the filesystem or fetches dependency manifests;
// it operates purely on the supplied bytes. Files in unknown or unsupported
// languages return a *File with Path/Lang populated but no Imports.
func Extract(path string, content []byte) (*File, error) {
	langName := enry.GetLanguage(path, content)

	f := &File{Path: path, Lang: langName}
	if langName == enry.OtherLanguage {
		return f, nil
	}

	l := LanguageByName(langName)
	if l == nil {
		return f, nil
	}

	list, err := l.Imports(content)
	if err != nil {
		return f, fmt.Errorf("extract %s imports from %q: %w", langName, path, err)
	}

	sort.Strings(list)
	f.Imports = dedup(list)

	return f, nil
}

// dedup removes consecutive duplicates from a sorted slice in place.
func dedup(sorted []string) []string {
	if len(sorted) == 0 {
		return []string{}
	}

	j := 0
	for i := 1; i < len(sorted); i++ {
		if sorted[j] == sorted[i] {
			continue
		}

		j++
		sorted[j] = sorted[i]
	}

	return sorted[:j+1]
}
