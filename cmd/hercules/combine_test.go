package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/spf13/cobra"

	"github.com/cwbudde/hercules"
	"github.com/cwbudde/hercules/internal/pb"
)

const combineTestDevs = "Devs"

// combineTestRefactoring is a mergeable leaf whose merge rebases tick axes.
const (
	combineTestRefactoring          = "RefactoringProxy"
	combineTestRefactoringThreshold = 0.5
	combineTestBeginTime            = 1556224895
)

// combineTestUnmergeable is a registered leaf which does not implement
// ResultMergeablePipelineItem, so combine can never merge it.
const combineTestUnmergeable = "FileHistoryAnalysis"

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
	if _, exists := result.GetContents()[combineTestDevs]; !exists {
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

func TestRunCombineMigratesSupportedV1BeforeEmittingCurrent(t *testing.T) {
	burndown := &pb.BurndownAnalysisResults{
		Project: &pb.BurndownSparseMatrix{
			Name:            "project",
			NumberOfRows:    1,
			NumberOfColumns: 1,
			Rows:            []*pb.BurndownSparseMatrixRow{{Columns: []uint32{1}}},
		},
	}
	input := writeCombineEnvelope(t, &pb.AnalysisResults{
		Header: &pb.Metadata{Version: 1, Repository: "legacy"},
		Contents: map[string][]byte{
			"Burndown": marshalCombineProto(t, burndown),
		},
	})
	var output bytes.Buffer

	if err := runCombine(newCombineTestCommand(&output, ""), []string{input}); err != nil {
		t.Fatalf("runCombine() failed for supported schema 1: %v", err)
	}

	var result pb.AnalysisResults
	if err := proto.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("combined output is malformed: %v", err)
	}
	if result.GetHeader().GetVersion() != pb.SchemaVersion {
		t.Fatalf("combined version = %d, want %d", result.GetHeader().GetVersion(), pb.SchemaVersion)
	}
	if _, exists := result.GetContents()["Burndown"]; !exists {
		t.Fatalf("combined output lacks migrated Burndown: %v", result.GetContents())
	}
}

func TestRunCombineNeverRelabelsUnvalidatedV1Content(t *testing.T) {
	input := writeCombineEnvelope(t, &pb.AnalysisResults{
		Header: &pb.Metadata{Version: 1, Repository: "legacy"},
		Contents: map[string][]byte{
			combineTestDevs: marshalCombineProto(t, &pb.DevsAnalysisResults{}),
		},
	})
	var output bytes.Buffer

	err := runCombine(newCombineTestCommand(&output, ""), []string{input})
	if err == nil || !strings.Contains(err.Error(), "analysis version unsupported") {
		t.Fatalf("runCombine() error = %v, want unsupported schema error", err)
	}
	if output.Len() != 0 {
		t.Fatalf("runCombine() emitted %d bytes for unvalidated v1 content", output.Len())
	}
}

func TestRunCombineRejectsIncompatibleInputVersion(t *testing.T) {
	input := writeCombineEnvelope(t, &pb.AnalysisResults{
		Header: &pb.Metadata{Version: pb.SchemaVersion + 1, Repository: "future"},
	})
	var output bytes.Buffer

	err := runCombine(newCombineTestCommand(&output, ""), []string{input})
	if err == nil || !strings.Contains(err.Error(), "analysis version unsupported") {
		t.Fatalf("runCombine() error = %v, want unsupported schema error", err)
	}
	if output.Len() != 0 {
		t.Fatalf("runCombine() emitted %d bytes for a future schema", output.Len())
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

// TestRunCombineOnlyRestrictsWhatIsRead pins the escape hatch of PLAN.md B4: --only must keep an
// unmergeable analysis in the input from failing the run, which it can only do by filtering before
// the merge rather than during it.
func TestRunCombineOnlyRestrictsWhatIsRead(t *testing.T) {
	input := writeCombineTestInputWithUnmergeable(t, "repository")
	var output bytes.Buffer

	if err := runCombine(newCombineTestCommand(&output, combineTestDevs), []string{input}); err != nil {
		t.Fatalf("runCombine() failed despite --only: %v", err)
	}

	contents := unmarshalCombineOutput(t, &output)
	if _, exists := contents[combineTestDevs]; !exists {
		t.Fatalf("combined output lacks the requested analysis: %v", contents)
	}
	if _, exists := contents[combineTestUnmergeable]; exists {
		t.Fatalf("--only %s emitted %s as well", combineTestDevs, combineTestUnmergeable)
	}
}

// TestRunCombineSkipsUnmergeableAnalyses covers the same input without --only: an analysis which
// cannot be merged at all is a property of that analysis, not a defect in the file, so it must not
// take every mergeable analysis down with it.
func TestRunCombineSkipsUnmergeableAnalyses(t *testing.T) {
	input := writeCombineTestInputWithUnmergeable(t, "repository")
	var output bytes.Buffer

	if err := runCombine(newCombineTestCommand(&output, ""), []string{input}); err != nil {
		t.Fatalf("runCombine() failed over an unmergeable analysis: %v", err)
	}

	contents := unmarshalCombineOutput(t, &output)
	if _, exists := contents[combineTestDevs]; !exists {
		t.Fatalf("combined output lacks the mergeable analysis: %v", contents)
	}
}

// TestRunCombineRejectsUnmergeableOnly keeps the skip from swallowing an explicit request: naming
// an unmergeable analysis in --only means the caller wants exactly that, so it has to fail loudly.
func TestRunCombineRejectsUnmergeableOnly(t *testing.T) {
	input := writeCombineTestInputWithUnmergeable(t, "repository")
	var output bytes.Buffer

	err := runCombine(newCombineTestCommand(&output, combineTestUnmergeable), []string{input})
	if err == nil {
		t.Fatalf("runCombine() succeeded for unmergeable --only %s", combineTestUnmergeable)
	}
	if !strings.Contains(err.Error(), "ResultMergeablePipelineItem is not implemented") {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("runCombine() wrote %d bytes for an unmergeable request", output.Len())
	}
}

// TestGetOptionsStringListsOnlyMergeableLeaves guards the --only help text against advertising
// choices combine cannot honour.
func TestGetOptionsStringListsOnlyMergeableLeaves(t *testing.T) {
	choices := getOptionsString()
	if !strings.Contains(choices, combineTestDevs) {
		t.Fatalf("--only choices lack a mergeable leaf: %s", choices)
	}
	if strings.Contains(choices, combineTestUnmergeable) {
		t.Fatalf("--only choices advertise unmergeable %s: %s", combineTestUnmergeable, choices)
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
		map[string]any{combineTestDevs: struct{}{}},
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

// TestRunCombineMergesRefactoringProxy pins PLAN.md B4: RefactoringProxy implements
// ResultMergeablePipelineItem, so --only must accept it and combine must actually merge the two
// tick axes rather than reject the request.
func TestRunCombineMergesRefactoringProxy(t *testing.T) {
	const day = 24 * 3600
	first := writeCombineRefactoringInput(t, "first", combineTestBeginTime,
		[]int32{0, 1}, []float32{0.1, 0.2}, []int32{10, 10})
	second := writeCombineRefactoringInput(t, "second", combineTestBeginTime+2*day,
		[]int32{0, 1}, []float32{0.3, 0.4}, []int32{20, 20})
	var output bytes.Buffer

	err := runCombine(newCombineTestCommand(&output, combineTestRefactoring), []string{first, second})
	if err != nil {
		t.Fatalf("runCombine() failed for --only %s: %v", combineTestRefactoring, err)
	}

	contents := unmarshalCombineOutput(t, &output)
	payload, exists := contents[combineTestRefactoring]
	if !exists {
		t.Fatalf("combined output lacks %s: %v", combineTestRefactoring, contents)
	}

	var merged pb.RefactoringProxyResults
	if err := proto.Unmarshal(payload, &merged); err != nil {
		t.Fatalf("merged %s is malformed: %v", combineTestRefactoring, err)
	}
	if want := []int32{0, 1, 2, 3}; !slices.Equal(merged.GetTicks(), want) {
		t.Fatalf("merged ticks = %v, want %v", merged.GetTicks(), want)
	}
	if want := []int32{10, 10, 20, 20}; !slices.Equal(merged.GetTotalChanges(), want) {
		t.Fatalf("merged total changes = %v, want %v", merged.GetTotalChanges(), want)
	}
}

func writeCombineRefactoringInput(
	t *testing.T, repository string, beginTime int64,
	ticks []int32, ratios []float32, totals []int32,
) string {
	t.Helper()

	isRefactoring := make([]bool, len(ticks))
	for i, ratio := range ratios {
		isRefactoring[i] = ratio > combineTestRefactoringThreshold
	}
	message := pb.AnalysisResults{
		Header: &pb.Metadata{
			Version:       pb.SchemaVersion,
			Repository:    repository,
			Commits:       1,
			BeginUnixTime: beginTime,
			EndUnixTime:   beginTime + 4*24*3600,
		},
		Contents: map[string][]byte{
			combineTestRefactoring: marshalCombineProto(t, &pb.RefactoringProxyResults{
				Ticks:         ticks,
				RenameRatios:  ratios,
				IsRefactoring: isRefactoring,
				TotalChanges:  totals,
				Threshold:     combineTestRefactoringThreshold,
				TickSize:      int64(24 * time.Hour),
			}),
		},
	}
	return writeCombineEnvelope(t, &message)
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
		Contents: map[string][]byte{combineTestDevs: devs},
	}
	return writeCombineEnvelope(t, &message)
}

func writeCombineTestInputWithUnmergeable(t *testing.T, repository string) string {
	t.Helper()

	message := pb.AnalysisResults{
		Header: &pb.Metadata{
			Version:    pb.SchemaVersion,
			Repository: repository,
			Commits:    1,
		},
		Contents: map[string][]byte{
			combineTestDevs:        marshalCombineProto(t, &pb.DevsAnalysisResults{}),
			combineTestUnmergeable: marshalCombineProto(t, &pb.FileHistoryResultMessage{}),
		},
	}
	return writeCombineEnvelope(t, &message)
}

func unmarshalCombineOutput(t *testing.T, output *bytes.Buffer) map[string][]byte {
	t.Helper()

	var result pb.AnalysisResults
	if err := proto.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("combined output is malformed: %v", err)
	}
	return result.GetContents()
}

func writeCombineEnvelope(t *testing.T, message *pb.AnalysisResults) string {
	t.Helper()
	serialized := marshalCombineProto(t, message)
	path := filepath.Join(t.TempDir(), "analysis.pb")
	if err := os.WriteFile(path, serialized, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func marshalCombineProto(t *testing.T, message proto.Message) []byte {
	t.Helper()
	serialized, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return serialized
}
