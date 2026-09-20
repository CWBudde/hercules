package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

func writePack(t *testing.T, directory, name string) {
	t.Helper()

	err := os.WriteFile(filepath.Join(directory, name), []byte("PACK"), 0o600)
	if err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestUnreadablePackHint(t *testing.T) {
	repository := t.TempDir()
	packs := filepath.Join(repository, ".git", "objects", "pack")

	err := os.MkdirAll(packs, 0o700)
	if err != nil {
		t.Fatalf("create pack directory: %v", err)
	}

	writePack(t, packs, "pack-0123456789abcdef0123456789abcdef01234567.pack")

	if hint := unreadablePackHint(repository); hint != "" {
		t.Fatalf("conventional packs produced a hint: %q", hint)
	}

	writePack(t, packs, "loose-0123456789abcdef0123456789abcdef01234567.pack")

	hint := unreadablePackHint(repository)
	if !strings.Contains(hint, "loose-0123456789abcdef0123456789abcdef01234567.pack") {
		t.Errorf("hint does not name the unreadable pack: %q", hint)
	}

	if !strings.Contains(hint, "git repack -A -d") {
		t.Errorf("hint does not say how to fix it: %q", hint)
	}
}

func TestUnreadablePackHintBareRepository(t *testing.T) {
	repository := t.TempDir()
	packs := filepath.Join(repository, "objects", "pack")

	err := os.MkdirAll(packs, 0o700)
	if err != nil {
		t.Fatalf("create pack directory: %v", err)
	}

	writePack(t, packs, "loose-0123456789abcdef0123456789abcdef01234567.pack")

	if hint := unreadablePackHint(repository); hint == "" {
		t.Error("a bare repository with an unreadable pack produced no hint")
	}
}

func TestAnnotateMissingObject(t *testing.T) {
	repository := t.TempDir()
	packs := filepath.Join(repository, ".git", "objects", "pack")

	err := os.MkdirAll(packs, 0o700)
	if err != nil {
		t.Fatalf("create pack directory: %v", err)
	}

	writePack(t, packs, "loose-0123456789abcdef0123456789abcdef01234567.pack")

	missing := plumbing.ErrObjectNotFound
	annotated := annotateMissingObject(missing, repository)

	if !errors.Is(annotated, plumbing.ErrObjectNotFound) {
		t.Error("annotation dropped the wrapped error")
	}

	if !strings.Contains(annotated.Error(), "git repack -A -d") {
		t.Errorf("annotation carries no hint: %q", annotated.Error())
	}

	other := errors.New("some other failure")
	annotatedOther := annotateMissingObject(other, repository)

	if !errors.Is(annotatedOther, other) {
		t.Error("an unrelated error lost its identity")
	}

	if strings.Contains(annotatedOther.Error(), "git repack") {
		t.Error("an unrelated error was annotated")
	}

	if annotateMissingObject(nil, repository) != nil {
		t.Error("nil was annotated")
	}
}
