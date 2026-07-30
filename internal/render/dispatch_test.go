package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/hercules/internal/render/burndown"
	renderModes "github.com/cwbudde/hercules/internal/render/modes"
	"github.com/cwbudde/hercules/internal/render/readers"
)

type stubReader struct {
	developerStats []readers.DeveloperStat
}

func (r stubReader) Read(file io.Reader) error { return nil }
func (r stubReader) GetName() string           { return "test-repo" }
func (r stubReader) GetHeader() (int64, int64) { return 0, 0 }
func (r stubReader) GetProjectBurndown() (string, [][]int) {
	return "test-repo", [][]int{{1, 2}, {3, 4}}
}

func (r stubReader) GetBurndownParameters() (burndown.BurndownParameters, error) {
	return burndown.BurndownParameters{Sampling: 1, Granularity: 1, TickSize: 86400}, nil
}

func (r stubReader) GetProjectBurndownWithHeader() (burndown.BurndownHeader, string, [][]int, error) {
	return burndown.BurndownHeader{Start: 0, Last: 86400, Sampling: 1, Granularity: 1, TickSize: 86400}, "test-repo", [][]int{{1, 2}, {3, 4}}, nil
}

func (r stubReader) GetFilesBurndown() ([]readers.FileBurndown, error) {
	return nil, fmt.Errorf("%w: files burndown", readers.ErrAnalysisMissing)
}

func (r stubReader) GetPeopleBurndown() ([]readers.PeopleBurndown, error) {
	return nil, fmt.Errorf("%w: people burndown", readers.ErrAnalysisMissing)
}

func (r stubReader) GetOwnershipBurndown() ([]string, map[string][][]int, error) {
	return nil, nil, fmt.Errorf("%w: people burndown", readers.ErrAnalysisMissing)
}

func (r stubReader) GetPeopleInteraction() ([]string, [][]int, error) {
	return nil, nil, fmt.Errorf("%w: people interaction", readers.ErrAnalysisMissing)
}

func (r stubReader) GetFileCooccurrence() ([]string, readers.SparseMatrix, error) {
	return nil, readers.SparseMatrix{}, fmt.Errorf("%w: file coupling", readers.ErrAnalysisMissing)
}

func (r stubReader) GetPeopleCooccurrence() ([]string, readers.SparseMatrix, error) {
	return nil, readers.SparseMatrix{}, fmt.Errorf("%w: people coupling", readers.ErrAnalysisMissing)
}

func (r stubReader) GetShotnessCooccurrence() ([]string, readers.SparseMatrix, error) {
	return nil, readers.SparseMatrix{}, fmt.Errorf("%w: shotness", readers.ErrAnalysisMissing)
}

func (r stubReader) GetShotnessRecords() ([]readers.ShotnessRecord, error) {
	return nil, fmt.Errorf("%w: shotness", readers.ErrAnalysisMissing)
}

func (r stubReader) GetDeveloperStats() ([]readers.DeveloperStat, error) {
	if r.developerStats == nil {
		return nil, fmt.Errorf("%w: Devs", readers.ErrAnalysisMissing)
	}
	return r.developerStats, nil
}

func (r stubReader) GetLanguageStats() ([]readers.LanguageStat, error) {
	return nil, fmt.Errorf("%w: Devs", readers.ErrAnalysisMissing)
}

func (r stubReader) GetRuntimeStats() (map[string]float64, error) {
	return map[string]float64{"Burndown": 1.25}, nil
}

func (r stubReader) GetDeveloperTimeSeriesData() (*readers.DeveloperTimeSeriesData, error) {
	return nil, fmt.Errorf("%w: Devs", readers.ErrAnalysisMissing)
}

func TestExecuteModesWithNoModesIsNoop(t *testing.T) {
	output := captureStdout(t, func() {
		executeModes(nil, stubReader{}, "", nil, nil)
	})
	if output != "" {
		t.Fatalf("executeModes() wrote %q, want no output", output)
	}
}

func TestExecuteModesPrintsPythonMissingDataWarning(t *testing.T) {
	output := captureStderr(t, func() {
		executeModes([]string{"devs"}, stubReader{}, filepath.Join(t.TempDir(), "devs.png"), nil, nil)
	})

	if !strings.Contains(output, "Devs stats were not collected. Re-run hercules with --devs.") {
		t.Fatalf("missing Python-compatible warning in output: %q", output)
	}
	if strings.Contains(output, "Error in mode devs") {
		t.Fatalf("missing data should not be reported as hard mode error: %q", output)
	}
}

func TestExecuteModesPrintsDevsParallelPeopleBurndownWarning(t *testing.T) {
	output := captureStderr(t, func() {
		executeModes([]string{"devs-parallel"}, stubReader{}, filepath.Join(t.TempDir(), "devs-parallel.png"), nil, nil)
	})

	if !strings.Contains(output, "devs-parallel: Burndown stats for people were not collected. Re-run hercules with --burndown --burndown-people.") {
		t.Fatalf("missing devs-parallel people burndown warning in output: %q", output)
	}
	if strings.Contains(output, "Error in mode devs-parallel") {
		t.Fatalf("missing data should not be reported as hard mode error: %q", output)
	}
}

func TestRunAllModesUsesPythonAllComposition(t *testing.T) {
	oldHandlers := make(map[string]modeHandler, len(pythonAllModes))
	var called []string
	for _, mode := range pythonAllModes {
		oldHandlers[mode] = modeHandlers[mode]
		modeName := mode
		modeHandlers[mode] = func(readers.Reader, string, *time.Time, *time.Time, renderModes.Options) error {
			called = append(called, modeName)
			return nil
		}
	}
	defer func() {
		maps.Copy(modeHandlers, oldHandlers)
	}()

	renderer, err := NewRenderer(Options{Quiet: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := renderer.runAllModes(stubReader{}, t.TempDir(), nil, nil); err != nil {
		t.Fatalf("runAllModes() unexpected error: %v", err)
	}
	if strings.Join(called, ",") != strings.Join(pythonAllModes, ",") {
		t.Fatalf("runAllModes() called %#v, want %#v", called, pythonAllModes)
	}
}

func TestExecuteModesJSONWritesReaderData(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "devs.json")
	reader := stubReader{
		developerStats: []readers.DeveloperStat{{
			Name:         "Ada",
			Commits:      3,
			LinesAdded:   10,
			LinesRemoved: 2,
		}},
	}

	executeModes([]string{"devs"}, reader, outputPath, nil, nil)

	raw, err := os.ReadFile(outputPath) // #nosec G304 - test path is under t.TempDir.
	if err != nil {
		t.Fatalf("failed to read JSON output: %v", err)
	}
	var payload struct {
		Results map[string]struct {
			DeveloperStats []readers.DeveloperStat `json:"developer_stats"`
			Message        string                  `json:"message"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, raw)
	}
	stats := payload.Results["devs"].DeveloperStats
	if len(stats) != 1 || stats[0].Name != "Ada" || stats[0].Commits != 3 {
		t.Fatalf("JSON output did not contain developer data: %#v", payload.Results["devs"])
	}
	if payload.Results["devs"].Message != "" {
		t.Fatalf("JSON output should not use placeholder message: %#v", payload.Results["devs"])
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	os.Stdout = writePipe

	fn()

	if err := writePipe.Close(); err != nil {
		t.Fatalf("failed to close stdout pipe: %v", err)
	}
	os.Stdout = original

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, readPipe); err != nil {
		t.Fatalf("failed to capture stdout: %v", err)
	}
	return buf.String()
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stderr
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stderr pipe: %v", err)
	}
	os.Stderr = writePipe

	fn()

	if err := writePipe.Close(); err != nil {
		t.Fatalf("failed to close stderr pipe: %v", err)
	}
	os.Stderr = original

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, readPipe); err != nil {
		t.Fatalf("failed to capture stderr: %v", err)
	}
	return buf.String()
}

func TestRunWithResultsClassifiesOutcomes(t *testing.T) {
	oldHandler := modeHandlers["devs"]
	defer func() { modeHandlers["devs"] = oldHandler }()

	tests := []struct {
		name        string
		handlerErr  error
		wantErr     bool
		wantWarning bool
	}{
		{name: "success", handlerErr: nil, wantErr: false, wantWarning: false},
		{name: "hard failure", handlerErr: errors.New("render exploded"), wantErr: true, wantWarning: false},
		{name: "downgraded warning", handlerErr: fmt.Errorf("failed: %w", readers.ErrAnalysisMissing), wantErr: false, wantWarning: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modeHandlers["devs"] = func(readers.Reader, string, *time.Time, *time.Time, renderModes.Options) error {
				return tt.handlerErr
			}
			var aggregate Result
			captureStdout(t, func() {
				aggregate = Run(stubReader{}, []string{"devs"}, Options{
					Output: filepath.Join(t.TempDir(), "devs.png"),
					Quiet:  true,
				})
			})
			results := aggregate.Modes
			if len(results) != 1 {
				t.Fatalf("RunWithResults() returned %d results, want 1", len(results))
			}
			modeResult := results[0]
			if modeResult.Mode != "devs" {
				t.Fatalf("result.Mode = %q, want %q", modeResult.Mode, "devs")
			}
			if (modeResult.Err != nil) != tt.wantErr {
				t.Fatalf("result.Err = %v, wantErr %v", modeResult.Err, tt.wantErr)
			}
			if (modeResult.Warning != "") != tt.wantWarning {
				t.Fatalf("result.Warning = %q, wantWarning %v", modeResult.Warning, tt.wantWarning)
			}
			if (aggregate.Err() != nil) != tt.wantErr {
				t.Fatalf("aggregate.Err() = %v, wantErr %v", aggregate.Err(), tt.wantErr)
			}
		})
	}
}

func TestRunWithResultsReportsUnimplementedModeAsWarning(t *testing.T) {
	oldHandler := modeHandlers["devs"]
	delete(modeHandlers, "devs")
	defer func() { modeHandlers["devs"] = oldHandler }()

	var result Result
	captureStderr(t, func() {
		result = Run(stubReader{}, []string{"devs"}, Options{
			Output: filepath.Join(t.TempDir(), "devs.png"),
		})
	})
	results := result.Modes
	if len(results) != 1 {
		t.Fatalf("RunWithResults() returned %d results, want 1", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("unimplemented mode should be a hard error")
	}
	if results[0].Warning != "" {
		t.Fatalf("unimplemented mode should not be a warning: %q", results[0].Warning)
	}
}

func TestMissingAnalysisWarningClassifiesTypedErrors(t *testing.T) {
	warning, ok := missingAnalysisWarning("temporal-activity", fmt.Errorf("failed to get temporal activity data: %w", readers.ErrAnalysisMissing))
	if !ok {
		t.Fatal("expected typed missing analysis error to become a warning")
	}
	want := "Temporal activity stats were not collected. Re-run hercules with --temporal-activity."
	if warning != want {
		t.Fatalf("warning = %q, want %q", warning, want)
	}
}

func TestMissingAnalysisWarningDoesNotHideProcessingErrors(t *testing.T) {
	for _, message := range []string{
		"no output device",
		"plot target not found",
		"missing font",
	} {
		t.Run(message, func(t *testing.T) {
			_, ok := missingAnalysisWarning("devs", errors.New(message))
			if ok {
				t.Fatal("unexpected warning classification for a non-missing processing error")
			}
		})
	}
}

func TestRunContinuesIndependentModesAndAggregatesStatus(t *testing.T) {
	oldDevs := modeHandlers["devs"]
	oldLanguages := modeHandlers["languages"]
	oldShotness := modeHandlers["shotness"]
	defer func() {
		modeHandlers["devs"] = oldDevs
		modeHandlers["languages"] = oldLanguages
		modeHandlers["shotness"] = oldShotness
	}()

	var called []string
	modeHandlers["devs"] = func(readers.Reader, string, *time.Time, *time.Time, renderModes.Options) error {
		called = append(called, "devs")
		return nil
	}
	modeHandlers["languages"] = func(readers.Reader, string, *time.Time, *time.Time, renderModes.Options) error {
		called = append(called, "languages")
		return errors.New("missing font")
	}
	modeHandlers["shotness"] = func(readers.Reader, string, *time.Time, *time.Time, renderModes.Options) error {
		called = append(called, "shotness")
		return fmt.Errorf("%w: Shotness", readers.ErrAnalysisMissing)
	}

	var result Result
	captureStderr(t, func() {
		result = Run(
			stubReader{},
			[]string{"devs", "languages", "shotness"},
			Options{Output: t.TempDir(), Quiet: true},
		)
	})

	if strings.Join(called, ",") != "devs,languages,shotness" {
		t.Fatalf("modes called = %v, want all independent modes", called)
	}
	if len(result.Modes) != 3 {
		t.Fatalf("Run() returned %d mode results, want 3", len(result.Modes))
	}
	if result.Modes[0].Err != nil || result.Modes[0].Warning != "" {
		t.Fatalf("success result = %#v", result.Modes[0])
	}
	if result.Modes[1].Err == nil || result.Modes[1].Warning != "" {
		t.Fatalf("hard failure result = %#v", result.Modes[1])
	}
	if result.Modes[2].Err != nil || result.Modes[2].Warning == "" {
		t.Fatalf("warning result = %#v", result.Modes[2])
	}
	if result.Err() == nil {
		t.Fatal("aggregate status did not expose hard failure")
	}
	if !result.HasWarnings() {
		t.Fatal("aggregate status did not expose warning")
	}
}

func TestRunReportsJSONOutputWriteFailure(t *testing.T) {
	output := filepath.Join(t.TempDir(), "missing", "results.json")
	var result Result
	captureStderr(t, func() {
		result = Run(stubReader{
			developerStats: []readers.DeveloperStat{{Name: "Ada"}},
		}, []string{"devs"}, Options{Output: output, Quiet: true})
	})

	if result.OutputError == nil {
		t.Fatal("Run() did not retain the JSON output-write error")
	}
	if result.Err() == nil {
		t.Fatal("Run().Err() succeeded despite JSON output-write failure")
	}
}

func TestRunReportsImageOutputFailure(t *testing.T) {
	oldHandler := modeHandlers["devs"]
	defer func() { modeHandlers["devs"] = oldHandler }()
	modeHandlers["devs"] = func(readers.Reader, string, *time.Time, *time.Time, renderModes.Options) error {
		return fmt.Errorf("save image: %w", os.ErrPermission)
	}

	var result Result
	captureStderr(t, func() {
		result = Run(stubReader{}, []string{"devs"}, Options{
			Output: filepath.Join(t.TempDir(), "devs.png"),
			Quiet:  true,
		})
	})
	if !errors.Is(result.Err(), os.ErrPermission) {
		t.Fatalf("Run().Err() = %v, want image write failure", result.Err())
	}
}
