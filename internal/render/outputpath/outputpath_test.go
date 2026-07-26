package outputpath

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStableSlugIsRuneSafeStableAndCollisionResistant(t *testing.T) {
	long := strings.Repeat("界", 100)
	first := StableSlug(long)
	second := StableSlug(long)
	if first != second {
		t.Fatalf("StableSlug() is not stable: %q != %q", first, second)
	}
	if !utf8.ValidString(first) {
		t.Fatalf("StableSlug() split a UTF-8 rune: %q", first)
	}
	if count := utf8.RuneCountInString(strings.Split(first, "-")[0]); count > maxSlugRunes {
		t.Fatalf("StableSlug() retained %d label runes, want at most %d", count, maxSlugRunes)
	}

	left := StableSlug("team/a:b")
	right := StableSlug("team_a_b")
	if left == right {
		t.Fatalf("sanitization collision was not disambiguated: %q", left)
	}
}

func TestFanoutPathsRejectDuplicateIdentitiesBeforeRendering(t *testing.T) {
	output := filepath.Join(t.TempDir(), "burndown-file.svg")
	_, err := FanoutPaths(output, "burndown_file", []string{"same", "same"})
	if err == nil {
		t.Fatal("FanoutPaths() accepted duplicate planned paths")
	}
}

func TestFanoutPathsKeepCollidingAndUnicodeIdentitiesUnique(t *testing.T) {
	output := filepath.Join(t.TempDir(), "burndown-file.svg")
	paths, err := FanoutPaths(output, "burndown_file", []string{
		"src/a:b.go",
		"src_a_b.go",
		strings.Repeat("開発者", 40),
	})
	if err != nil {
		t.Fatalf("FanoutPaths() failed: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("FanoutPaths() returned %d paths, want 3", len(paths))
	}
	if paths[0] == paths[1] {
		t.Fatalf("colliding sanitized identities planned the same path: %q", paths[0])
	}
	for _, path := range paths {
		if !utf8.ValidString(filepath.Base(path)) {
			t.Fatalf("planned path is not valid UTF-8: %q", path)
		}
		if filepath.Ext(path) != ".svg" {
			t.Fatalf("planned path has extension %q, want .svg", filepath.Ext(path))
		}
	}
}
