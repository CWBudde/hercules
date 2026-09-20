package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
)

// maxHintedPacks caps how many pack file names the hint spells out.
const maxHintedPacks = 3

// unreadablePackHint describes pack files in a repository that go-git cannot see.
//
// go-git's filesystem storage enumerates pack files by the "pack-" prefix, while git itself
// accepts any name. `git maintenance run --task=loose-objects` (git's own background
// maintenance, enabled per repository) writes its packs as "loose-<hash>.pack", so every object
// that ends up there becomes invisible to go-git while remaining perfectly reachable for git.
// The symptom is a bare "object not found" the moment the commit list touches such an object,
// on a repository whose `git fsck` and `git log` are clean - which is what this hint explains.
//
// The empty string means nothing suspicious was found; the caller then reports the plain error.
func unreadablePackHint(repository string) string {
	names := unreadablePacks(repository)
	if len(names) == 0 {
		return ""
	}

	listed := names
	suffix := ""

	if len(listed) > maxHintedPacks {
		listed = listed[:maxHintedPacks]
		suffix = fmt.Sprintf(" and %d more", len(names)-maxHintedPacks)
	}

	return fmt.Sprintf(
		"%s contains %d pack file(s) that are not named pack-*.pack (%s%s); "+
			"go-git only reads pack-*.pack, so every object stored there is invisible to "+
			"hercules although git itself finds it. Such packs are written by "+
			"`git maintenance run --task=loose-objects`. Run `git repack -A -d` in the "+
			"repository to fold them into a conventional pack",
		repository, len(names), strings.Join(listed, ", "), suffix,
	)
}

// unreadablePacks returns the sorted names of pack files go-git will not enumerate.
func unreadablePacks(repository string) []string {
	var names []string

	for _, directory := range []string{
		filepath.Join(repository, ".git", "objects", "pack"),
		filepath.Join(repository, "objects", "pack"),
	} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".pack") {
				continue
			}

			if strings.HasPrefix(name, "pack-") {
				continue
			}

			names = append(names, name)
		}
	}

	sort.Strings(names)

	return names
}

// annotateMissingObject appends the pack hint to a missing-object failure.
//
// Any other error, and any repository without suspicious packs, is returned untouched: the hint
// is a guess about a cause, so it must never displace the real message.
func annotateMissingObject(err error, repository string) error {
	if err == nil || !errors.Is(err, plumbing.ErrObjectNotFound) {
		return err
	}

	hint := unreadablePackHint(repository)
	if hint == "" {
		return err
	}

	return fmt.Errorf("%w (%s)", err, hint)
}
