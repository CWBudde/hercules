//go:build !tensorflow

package main

import (
	"strings"
	"testing"
)

func TestSelectReportAnalysisFlagsSentimentUnavailableWithoutTensorflow(t *testing.T) {
	available := map[string]struct{}{
		reportAnalysisSentiment: {},
	}
	_, err := selectReportAnalysisFlags(available, []string{reportAnalysisSentiment}, false)
	if err == nil {
		t.Fatal("expected sentiment-unavailable error")
	}
	if !strings.Contains(err.Error(), "rebuild with -tags tensorflow") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSelectReportAnalysisFlagsAllSkipsSentimentWithoutTensorflow(t *testing.T) {
	available := map[string]struct{}{
		reportAnalysisBurndown:  {},
		reportAnalysisSentiment: {},
	}
	flags, err := selectReportAnalysisFlags(available, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, flag := range flags {
		if flag == reportAnalysisSentiment {
			t.Fatalf("sentiment should be skipped in non-tensorflow builds: %v", flags)
		}
	}
}
