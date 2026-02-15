//go:build !babelfish
// +build !babelfish

package uast

import (
	"bytes"
	"testing"

	"github.com/meko-christian/hercules/internal/test"
)

func TestChangesSaverStub(t *testing.T) {
	saver := &ChangesSaver{}
	if saver.Flag() != "dump-uast-changes" {
		t.Fatalf("unexpected flag: %s", saver.Flag())
	}
	if len(saver.ListConfigurationOptions()) != 1 {
		t.Fatalf("unexpected options: %+v", saver.ListConfigurationOptions())
	}
	if err := saver.Initialize(test.Repository); err == nil {
		t.Fatal("expected babelfish-required error")
	}
	if err := saver.Serialize(nil, false, &bytes.Buffer{}); err == nil {
		t.Fatal("expected babelfish-required error")
	}
}
