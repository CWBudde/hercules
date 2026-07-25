package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/spf13/cobra"

	"github.com/cwbudde/hercules"
	"github.com/cwbudde/hercules/internal/pb"
)

func TestRunCombineValidatesSingleInput(t *testing.T) {
	input := filepath.Join(t.TempDir(), "invalid.pb")
	if err := os.WriteFile(input, []byte("not protobuf"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := runCombine(newCombineTestCommand(&output, ""), []string{input})
	if err == nil {
		t.Fatal("runCombine() succeeded for malformed single input")
	}
	if output.Len() != 0 {
		t.Fatalf("runCombine() wrote %d bytes for malformed input", output.Len())
	}
}

func TestRunCombineWritesValidatedSingleInput(t *testing.T) {
	input := writeCombineTestInput(t, "repository")
	var output bytes.Buffer

	if err := runCombine(newCombineTestCommand(&output, ""), []string{input}); err != nil {
		t.Fatalf("runCombine() failed: %v", err)
	}

	var result pb.AnalysisResults
	if err := proto.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("combined output is malformed: %v", err)
	}
	if _, exists := result.GetContents()["Devs"]; !exists {
		t.Fatalf("combined output lacks validated Devs result: %v", result.GetContents())
	}
}

func TestRunCombineMixedValidityWritesPartialOutputAndFails(t *testing.T) {
	valid := writeCombineTestInput(t, "valid")
	invalid := filepath.Join(t.TempDir(), "invalid.pb")
	if err := os.WriteFile(invalid, []byte("not protobuf"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer

	err := runCombine(newCombineTestCommand(&output, ""), []string{valid, invalid})
	if err == nil {
		t.Fatal("runCombine() succeeded despite malformed input")
	}
	if output.Len() == 0 {
		t.Fatal("runCombine() did not preserve the independently valid result")
	}
}

func TestRunCombineFailsWhenOnlyCannotBeHonored(t *testing.T) {
	input := writeCombineTestInput(t, "repository")
	var output bytes.Buffer

	err := runCombine(newCombineTestCommand(&output, "Burndown"), []string{input})
	if err == nil {
		t.Fatal("runCombine() succeeded without the requested analysis")
	}
	if !strings.Contains(err.Error(), `requested analysis "Burndown"`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("runCombine() wrote %d bytes without the requested analysis", output.Len())
	}
}

func TestRunCombineReportsOutputWriteFailure(t *testing.T) {
	input := writeCombineTestInput(t, "repository")

	err := runCombine(newCombineTestCommand(errorWriter{}, ""), []string{input})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("runCombine() error = %v, want closed-pipe failure", err)
	}
}

func TestMergeResultsUpdatesMetadataOnlyAfterResultSuccess(t *testing.T) {
	mergedResults := map[string]any{}
	mergedMetadata := &hercules.CommonAnalysisResult{}
	inputMetadata := &hercules.CommonAnalysisResult{CommitsNumber: 7}

	merged, mergeErrors := mergeResults(
		mergedResults,
		mergedMetadata,
		map[string]any{"Devs": struct{}{}},
		inputMetadata,
		"Burndown",
	)
	if merged != 0 || len(mergeErrors) != 0 {
		t.Fatalf("mergeResults() = (%d, %v), want no selected result", merged, mergeErrors)
	}
	if mergedMetadata.CommitsNumber != 0 {
		t.Fatalf("metadata merged without a successful result: %#v", mergedMetadata)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func newCombineTestCommand(output io.Writer, only string) *cobra.Command {
	command := &cobra.Command{}
	command.SetOut(output)
	command.Flags().Bool("profile", false, "")
	command.Flags().String("only", only, "")
	return command
}

func writeCombineTestInput(t *testing.T, repository string) string {
	t.Helper()

	devs, err := proto.Marshal(&pb.DevsAnalysisResults{})
	if err != nil {
		t.Fatal(err)
	}
	message := pb.AnalysisResults{
		Header: &pb.Metadata{
			Version:    pb.SchemaVersion,
			Repository: repository,
			Commits:    1,
		},
		Contents: map[string][]byte{"Devs": devs},
	}
	serialized, err := proto.Marshal(&message)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "analysis.pb")
	if err := os.WriteFile(path, serialized, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
