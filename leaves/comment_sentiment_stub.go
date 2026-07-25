//go:build !tensorflow

package leaves

import (
	"errors"
	"io"

	"github.com/go-git/go-git/v5"

	"github.com/cwbudde/hercules/internal/core"
)

const (
	ConfigCommentSentimentMinLength = "CommentSentiment.MinLength"
	ConfigCommentSentimentGap       = "CommentSentiment.Gap"

	DefaultCommentSentimentCommentMinLength = 20
	DefaultCommentSentimentGap              = float32(0.5)
)

var errTensorflowRequired = errors.New(
	"sentiment analysis is unavailable in this build; rebuild with -tags tensorflow",
)

// CommentSentimentAnalysis is a placeholder in non-tensorflow builds.
type CommentSentimentAnalysis struct {
	core.NoopMerger

	MinCommentLength int
	Gap              float32

	l core.Logger
}

// CommentSentimentResult is preserved for API compatibility in non-tensorflow builds.
type CommentSentimentResult struct{}

func (sent *CommentSentimentAnalysis) Name() string { return commentSentimentAnalysisName }

func (sent *CommentSentimentAnalysis) Provides() []string { return []string{} }

func (sent *CommentSentimentAnalysis) Requires() []string { return []string{} }

func (sent *CommentSentimentAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	return []core.ConfigurationOption{
		{
			Name:        ConfigCommentSentimentMinLength,
			Description: "Minimum length of the comment to be analyzed.",
			Flag:        "min-comment-len",
			Type:        core.IntConfigurationOption,
			Default:     DefaultCommentSentimentCommentMinLength,
		},
		{
			Name: ConfigCommentSentimentGap,
			Description: "Sentiment value threshold, values between 0.5 - X/2 and 0.5 + x/2 will not " +
				"be considered. Must be >= 0 and < 1. The purpose is to exclude neutral comments.",
			Flag:    "sentiment-gap",
			Type:    core.FloatConfigurationOption,
			Default: DefaultCommentSentimentGap,
		},
	}
}

func (sent *CommentSentimentAnalysis) Flag() string { return "sentiment" }

func (sent *CommentSentimentAnalysis) Description() string {
	return "[EXPERIMENTAL] Unavailable in this build. Rebuild with -tags tensorflow."
}

func (sent *CommentSentimentAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		sent.l = l
	}

	if val, exists := facts[ConfigCommentSentimentGap]; exists {
		sent.Gap = val.(float32)
	}

	if val, exists := facts[ConfigCommentSentimentMinLength]; exists {
		sent.MinCommentLength = val.(int)
	}

	return nil
}

func (*CommentSentimentAnalysis) ConfigureUpstream(facts map[string]any) error { return nil }

func (sent *CommentSentimentAnalysis) Initialize(repository *git.Repository) error {
	sent.l = core.NewLogger()
	return errTensorflowRequired
}

func (sent *CommentSentimentAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	return noDependencies(), nil
}

func (sent *CommentSentimentAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(sent, n)
}

func (sent *CommentSentimentAnalysis) Finalize() any { return CommentSentimentResult{} }

func (sent *CommentSentimentAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	return errTensorflowRequired
}

var _ = core.RegisterPipelineItem(&CommentSentimentAnalysis{})
