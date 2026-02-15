//go:build !tensorflow
// +build !tensorflow

package leaves

import (
	"strings"
	"testing"

	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/test"
)

func TestCommentSentimentStubMeta(t *testing.T) {
	sent := &CommentSentimentAnalysis{}
	if sent.Name() != "Sentiment" {
		t.Fatalf("unexpected name: %s", sent.Name())
	}
	if sent.Flag() != "sentiment" {
		t.Fatalf("unexpected flag: %s", sent.Flag())
	}
	if len(sent.Provides()) != 0 || len(sent.Requires()) != 0 {
		t.Fatalf("stub sentiment should not require/provide deps")
	}
	if !strings.Contains(sent.Description(), "Unavailable in this build") {
		t.Fatalf("unexpected description: %s", sent.Description())
	}
}

func TestCommentSentimentStubInitializeError(t *testing.T) {
	sent := &CommentSentimentAnalysis{}
	err := sent.Initialize(test.Repository)
	if err == nil {
		t.Fatal("expected initialize error in non-tensorflow build")
	}
	if !strings.Contains(err.Error(), "rebuild with -tags tensorflow") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCommentSentimentStubRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&CommentSentimentAnalysis{}).Name())
	if len(summoned) != 1 {
		t.Fatalf("expected sentiment in registry, got %d", len(summoned))
	}
	if summoned[0].Name() != "Sentiment" {
		t.Fatalf("unexpected registry item: %s", summoned[0].Name())
	}
}
