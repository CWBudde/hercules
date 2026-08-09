package main

import (
	"bytes"
	"errors"
	"fmt"
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
	"github.com/cwbudde/hercules/leaves"
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

// combineTestCouples is a mergeable leaf whose result is keyed by file path.
const (
	combineTestCouples = "Couples"
	combineTestReadme  = "README.md"
)

// combineTestHotspotRisk is a mergeable leaf which carries its own configuration, because
// combine summons it zero-valued. The length is deliberately above leaves.DefaultTopN.
const (
	combineTestHotspotRisk = "HotspotRisk"
	combineTestHotspotTopN = 30
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
// take every mergeable analysis down with it. Skipping quietly would be its own defect, so the
// "it merged anyway" and "it told you" halves are pinned together.
func TestRunCombineSkipsUnmergeableAnalyses(t *testing.T) {
	input := writeCombineTestInputWithUnmergeable(t, "repository")
	var output, diagnostics bytes.Buffer

	command := newCombineTestCommandWithStderr(&output, &diagnostics, "")
	if err := runCombine(command, []string{input}); err != nil {
		t.Fatalf("runCombine() failed over an unmergeable analysis: %v", err)
	}

	contents := unmarshalCombineOutput(t, &output)
	if _, exists := contents[combineTestDevs]; !exists {
		t.Fatalf("combined output lacks the mergeable analysis: %v", contents)
	}
	if _, exists := contents[combineTestUnmergeable]; exists {
		t.Fatalf("combined output carries the unmergeable analysis: %v", contents)
	}
	if !strings.Contains(diagnostics.String(), combineTestUnmergeable) {
		t.Fatalf("nothing warned that %s was dropped: %q", combineTestUnmergeable, diagnostics.String())
	}
}

// TestRunCombineWarnsOncePerDroppedAnalysis pins PLAN.md T1.3(a). The org-wide merge names the same
// handful of unmergeable leaves in every one of hundreds of inputs, and a per-file warning that long
// scrolls past behind the progress bar indistinguishably from silence. One line per analysis, saying
// what the user loses rather than which Go interface was missing.
func TestRunCombineWarnsOncePerDroppedAnalysis(t *testing.T) {
	inputs := []string{
		writeCombineTestInputWithUnmergeable(t, "alpha"),
		writeCombineTestInputWithUnmergeable(t, "beta"),
	}
	var output, diagnostics bytes.Buffer

	command := newCombineTestCommandWithStderr(&output, &diagnostics, "")
	if err := runCombine(command, inputs); err != nil {
		t.Fatalf("runCombine() failed over an unmergeable analysis: %v", err)
	}

	warning := diagnostics.String()
	if got := strings.Count(warning, combineTestUnmergeable); got != 1 {
		t.Fatalf("%s named %d times, want once:\n%s", combineTestUnmergeable, got, warning)
	}
	if !strings.Contains(warning, "not in the combined output") {
		t.Fatalf("warning never says the data is missing from the output:\n%s", warning)
	}
	if !strings.Contains(warning, "2 of 2 inputs") {
		t.Fatalf("warning does not report both inputs:\n%s", warning)
	}
}

// TestRunCombineDoesNotWarnWithoutDroppedAnalyses is the other half: a warning which appears when
// nothing was dropped teaches the reader to ignore it.
func TestRunCombineDoesNotWarnWithoutDroppedAnalyses(t *testing.T) {
	input := writeCombineTestInput(t, "repository")
	var output, diagnostics bytes.Buffer

	command := newCombineTestCommandWithStderr(&output, &diagnostics, "")
	if err := runCombine(command, []string{input}); err != nil {
		t.Fatalf("runCombine() failed: %v", err)
	}

	if strings.Contains(diagnostics.String(), "Skipped:") {
		t.Fatalf("clean input produced a skip warning:\n%s", diagnostics.String())
	}
}

// TestRunCombineQualifiesPathsWithTheirRepository covers the reason combine can rank files at all
// once several repositories are involved: the same path in two repositories is two files, and
// nothing but the repository name distinguishes them.
func TestRunCombineQualifiesPathsWithTheirRepository(t *testing.T) {
	alpha := writeCombineCouplesInput(t, "alpha", []string{combineTestReadme, "alpha.go"})
	beta := writeCombineCouplesInput(t, "beta", []string{combineTestReadme, "beta.go"})
	var output bytes.Buffer

	if err := runCombine(newCombineTestCommand(&output, ""), []string{alpha, beta}); err != nil {
		t.Fatalf("runCombine() failed: %v", err)
	}

	index := combinedCouplesIndex(t, &output)
	want := []string{"alpha:" + combineTestReadme, "alpha:alpha.go", "beta:" + combineTestReadme, "beta:beta.go"}
	if !slices.Equal(index, want) {
		t.Fatalf("combined file index = %v, want %v", index, want)
	}
}

// TestRunCombineLeavesCombinedInputQualifiedOnce guards against nesting one prefix inside another:
// a file which is itself the output of combine already carries qualified paths and says so in its
// header. The single-input case is the one a header heuristic gets wrong - its header names one
// repository and is indistinguishable from a plain analysis - so both are covered.
func TestRunCombineLeavesCombinedInputQualifiedOnce(t *testing.T) {
	for _, test := range []struct {
		name    string
		sources []string
		want    []string
	}{
		{
			name:    "two inputs",
			sources: []string{"alpha", "beta"},
			want:    []string{"alpha:" + combineTestReadme, "beta:" + combineTestReadme},
		},
		{
			name:    "one input",
			sources: []string{"alpha"},
			want:    []string{"alpha:" + combineTestReadme},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			inputs := make([]string, len(test.sources))
			for index, repository := range test.sources {
				inputs[index] = writeCombineCouplesInput(t, repository, []string{combineTestReadme})
			}
			var combined bytes.Buffer
			if err := runCombine(newCombineTestCommand(&combined, ""), inputs); err != nil {
				t.Fatalf("runCombine() failed: %v", err)
			}

			recombinedInput := filepath.Join(t.TempDir(), "combined.pb")
			if err := os.WriteFile(recombinedInput, combined.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := runCombine(newCombineTestCommand(&output, ""), []string{recombinedInput}); err != nil {
				t.Fatalf("runCombine() failed over a combined input: %v", err)
			}

			if index := combinedCouplesIndex(t, &output); !slices.Equal(index, test.want) {
				t.Fatalf("re-combined file index = %v, want %v", index, test.want)
			}
		})
	}
}

// TestRunCombineQualifiesFilesWrittenBeforeTheHeaderFlag covers the other half: a file which does
// not carry the flag has unqualified paths whatever its header names, so it must be qualified.
func TestRunCombineQualifiesFilesWrittenBeforeTheHeaderFlag(t *testing.T) {
	input := writeCombineCouplesInput(t, "alpha & beta", []string{combineTestReadme})
	var output bytes.Buffer

	if err := runCombine(newCombineTestCommand(&output, ""), []string{input}); err != nil {
		t.Fatalf("runCombine() failed: %v", err)
	}

	index := combinedCouplesIndex(t, &output)
	want := []string{"alpha & beta:" + combineTestReadme}
	if !slices.Equal(index, want) {
		t.Fatalf("file index = %v, want %v", index, want)
	}
}

func combinedCouplesIndex(t *testing.T, output *bytes.Buffer) []string {
	t.Helper()

	payload, exists := unmarshalCombineOutput(t, output)[combineTestCouples]
	if !exists {
		t.Fatal("combined output lacks the couples result")
	}
	var merged pb.CouplesAnalysisResults
	if err := proto.Unmarshal(payload, &merged); err != nil {
		t.Fatalf("merged %s is malformed: %v", combineTestCouples, err)
	}
	return merged.GetFileCouples().GetIndex()
}

// writeCombineCouplesInput serializes a couples result through the leaf itself, so the envelope
// matches what a real run writes.
func writeCombineCouplesInput(t *testing.T, repository string, files []string) string {
	t.Helper()

	result := leaves.CouplesResult{
		Files:       files,
		FilesLines:  make([]int, len(files)),
		FilesMatrix: make([]map[int]int64, len(files)),
		// One row for the unknown author, which is what a people-less result carries.
		PeopleMatrix: []map[int]int64{{}},
	}
	for index := range files {
		result.FilesLines[index] = 1
		result.FilesMatrix[index] = map[int]int64{index: 1}
	}
	serialized := bytes.Buffer{}
	if err := (&leaves.CouplesAnalysis{}).Serialize(result, true, &serialized); err != nil {
		t.Fatal(err)
	}

	return writeCombineEnvelope(t, &pb.AnalysisResults{
		Header: &pb.Metadata{
			Version:       pb.SchemaVersion,
			Repository:    repository,
			Commits:       1,
			BeginUnixTime: combineTestBeginTime,
			EndUnixTime:   combineTestBeginTime + 4*24*3600,
		},
		Contents: map[string][]byte{combineTestCouples: serialized.Bytes()},
	})
}

// TestRunCombineHotspotRiskIsSoundAcrossRepositories covers T1.2 end to end. On the org-wide merge
// the combined report came out truncated to DefaultTopN whatever the runs were configured with,
// with README.md present twice and every RiskScore exactly 1.0.
func TestRunCombineHotspotRiskIsSoundAcrossRepositories(t *testing.T) {
	const topN = combineTestHotspotTopN

	inputs := []string{
		writeCombineHotspotInput(t, "alpha", 1),
		writeCombineHotspotInput(t, "beta", 100),
	}
	var output bytes.Buffer

	if err := runCombine(newCombineTestCommand(&output, ""), inputs); err != nil {
		t.Fatalf("runCombine() failed: %v", err)
	}

	merged := combinedHotspotRisk(t, &output)
	if got := merged.GetTopN(); got != topN {
		t.Fatalf("merged top_n = %d, want %d", got, topN)
	}

	files := merged.GetFiles()
	if len(files) <= leaves.DefaultTopN {
		t.Fatalf("merged file count = %d, want more than DefaultTopN (%d): "+
			"the configured length was not honoured", len(files), leaves.DefaultTopN)
	}

	seen := map[string]bool{}
	distinctScores := map[float64]bool{}
	for _, file := range files {
		if seen[file.GetPath()] {
			t.Fatalf("path %q appears twice in the merged report", file.GetPath())
		}
		seen[file.GetPath()] = true
		distinctScores[file.GetRiskScore()] = true

		if !strings.HasPrefix(file.GetPath(), "alpha:") &&
			!strings.HasPrefix(file.GetPath(), "beta:") {
			t.Fatalf("path %q is not qualified with its repository", file.GetPath())
		}
	}
	if len(distinctScores) < 2 {
		t.Fatalf("every merged RiskScore is %v: the two runs were not rescaled onto one scale",
			files[0].GetRiskScore())
	}

	// beta's files are a hundred times larger than alpha's, so after rescaling they must lead.
	if !strings.HasPrefix(files[0].GetPath(), "beta:") {
		t.Fatalf("top merged file is %q, want one of beta's", files[0].GetPath())
	}
}

func combinedHotspotRisk(t *testing.T, output *bytes.Buffer) *pb.HotspotRiskResults {
	t.Helper()

	payload, exists := unmarshalCombineOutput(t, output)[combineTestHotspotRisk]
	if !exists {
		t.Fatal("combined output lacks the hotspot risk result")
	}
	merged := &pb.HotspotRiskResults{}
	if err := proto.Unmarshal(payload, merged); err != nil {
		t.Fatalf("merged %s is malformed: %v", combineTestHotspotRisk, err)
	}
	return merged
}

// writeCombineHotspotInput serializes a hotspot risk result through the leaf itself, so the
// envelope matches what a real run writes. scale multiplies the raw factors, which is how the
// two repositories are given incomparable per-run maxima.
func writeCombineHotspotInput(t *testing.T, repository string, scale int) string {
	t.Helper()

	result := leaves.HotspotRiskResult{
		WindowDays: 90,
		TopN:       combineTestHotspotTopN,
		Weights: leaves.HotspotRiskWeights{
			Size: 1, Churn: 1, Coupling: 1, Ownership: 1,
		},
	}
	// Every repository contributes a README.md, which is what collapsed the org-wide report.
	paths := make([]string, 0, combineTestHotspotTopN)
	paths = append(paths, combineTestReadme)
	for i := range combineTestHotspotTopN - 1 {
		paths = append(paths, fmt.Sprintf("file%02d.go", i))
	}
	for index, path := range paths {
		result.Files = append(result.Files, leaves.FileRisk{
			Path:  path,
			Size:  (len(paths) - index) * scale,
			Churn: (len(paths) - index) * scale,
		})
	}

	serialized := bytes.Buffer{}
	if err := (&leaves.HotspotRiskAnalysis{}).Serialize(result, true, &serialized); err != nil {
		t.Fatal(err)
	}

	return writeCombineEnvelope(t, &pb.AnalysisResults{
		Header: &pb.Metadata{
			Version:       pb.SchemaVersion,
			Repository:    repository,
			Commits:       1,
			BeginUnixTime: combineTestBeginTime,
			EndUnixTime:   combineTestBeginTime + 4*24*3600,
		},
		Contents: map[string][]byte{combineTestHotspotRisk: serialized.Bytes()},
	})
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

// newCombineTestCommandWithStderr is newCombineTestCommand for the tests which read the
// diagnostics. It is a sibling rather than a third parameter so that the call sites which do not
// care about stderr stay as short as they are.
func newCombineTestCommandWithStderr(output, diagnostics io.Writer, only string) *cobra.Command {
	command := newCombineTestCommand(output, only)
	command.SetErr(diagnostics)
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
			// A real run always dates its result, and merging two undated ones panics.
			BeginUnixTime: combineTestBeginTime,
			EndUnixTime:   combineTestBeginTime + 4*24*3600,
		},
		Contents: map[string][]byte{
			// The tick size is what a real run writes and what merging two of these needs; without
			// it the Devs merge divides by a zero tick.
			combineTestDevs: marshalCombineProto(t, &pb.DevsAnalysisResults{
				TickSize: int64(24 * time.Hour),
			}),
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
