package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render/burndown"
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
	return nil, fmt.Errorf("missing files data")
}

func (r stubReader) GetPeopleBurndown() ([]readers.PeopleBurndown, error) {
	return nil, fmt.Errorf("missing people data")
}

func (r stubReader) GetOwnershipBurndown() ([]string, map[string][][]int, error) {
	return nil, nil, fmt.Errorf("missing people data")
}

func (r stubReader) GetPeopleInteraction() ([]string, [][]int, error) {
	return nil, nil, fmt.Errorf("missing people interaction")
}

func (r stubReader) GetFileCooccurrence() ([]string, [][]int, error) {
	return nil, nil, fmt.Errorf("missing couples data")
}

func (r stubReader) GetPeopleCooccurrence() ([]string, [][]int, error) {
	return nil, nil, fmt.Errorf("missing couples data")
}

func (r stubReader) GetShotnessCooccurrence() ([]string, [][]int, error) {
	return nil, nil, fmt.Errorf("missing shotness data")
}

func (r stubReader) GetShotnessRecords() ([]readers.ShotnessRecord, error) {
	return nil, fmt.Errorf("missing shotness data")
}

func (r stubReader) GetDeveloperStats() ([]readers.DeveloperStat, error) {
	if r.developerStats == nil {
		return nil, fmt.Errorf("missing Devs data")
	}
	return r.developerStats, nil
}

func (r stubReader) GetLanguageStats() ([]readers.LanguageStat, error) {
	return nil, fmt.Errorf("missing Devs data")
}

func (r stubReader) GetRuntimeStats() (map[string]float64, error) {
	return map[string]float64{"Burndown": 1.25}, nil
}

func (r stubReader) GetDeveloperTimeSeriesData() (*readers.DeveloperTimeSeriesData, error) {
	return nil, fmt.Errorf("missing Devs data")
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
	previousQuiet := viper.GetBool("quiet")
	defer viper.Set("quiet", previousQuiet)
	viper.Set("quiet", true)

	output := captureStdout(t, func() {
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
	previousQuiet := viper.GetBool("quiet")
	defer viper.Set("quiet", previousQuiet)
	viper.Set("quiet", true)

	output := captureStdout(t, func() {
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
	previousQuiet := viper.GetBool("quiet")
	defer viper.Set("quiet", previousQuiet)
	viper.Set("quiet", true)

	oldHandlers := make(map[string]func(readers.Reader, string, *time.Time, *time.Time) error, len(pythonAllModes))
	var called []string
	for _, mode := range pythonAllModes {
		oldHandlers[mode] = modeHandlers[mode]
		modeName := mode
		modeHandlers[mode] = func(readers.Reader, string, *time.Time, *time.Time) error {
			called = append(called, modeName)
			return nil
		}
	}
	defer func() {
		for mode, handler := range oldHandlers {
			modeHandlers[mode] = handler
		}
	}()

	if err := runAllModes(stubReader{}, t.TempDir(), nil, nil); err != nil {
		t.Fatalf("runAllModes() unexpected error: %v", err)
	}
	if strings.Join(called, ",") != strings.Join(pythonAllModes, ",") {
		t.Fatalf("runAllModes() called %#v, want %#v", called, pythonAllModes)
	}
}

func TestExecuteModesJSONWritesReaderData(t *testing.T) {
	previousQuiet := viper.GetBool("quiet")
	defer viper.Set("quiet", previousQuiet)
	viper.Set("quiet", true)

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

func TestRunWithResultsClassifiesOutcomes(t *testing.T) {
	previousQuiet := viper.GetBool("quiet")
	defer viper.Set("quiet", previousQuiet)
	viper.Set("quiet", true)

	oldHandler := modeHandlers["devs"]
	defer func() { modeHandlers["devs"] = oldHandler }()

	tests := []struct {
		name        string
		handlerErr  error
		wantErr     bool
		wantWarning bool
	}{
		{name: "success", handlerErr: nil, wantErr: false, wantWarning: false},
		{name: "hard failure", handlerErr: fmt.Errorf("render exploded"), wantErr: true, wantWarning: false},
		{name: "downgraded warning", handlerErr: fmt.Errorf("failed: %w", readers.ErrAnalysisMissing), wantErr: false, wantWarning: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modeHandlers["devs"] = func(readers.Reader, string, *time.Time, *time.Time) error {
				return tt.handlerErr
			}
			var results []ModeResult
			captureStdout(t, func() {
				results = RunWithResults(stubReader{}, []string{"devs"}, Options{
					Output: filepath.Join(t.TempDir(), "devs.png"),
				})
			})
			if len(results) != 1 {
				t.Fatalf("RunWithResults() returned %d results, want 1", len(results))
			}
			result := results[0]
			if result.Mode != "devs" {
				t.Fatalf("result.Mode = %q, want %q", result.Mode, "devs")
			}
			if (result.Err != nil) != tt.wantErr {
				t.Fatalf("result.Err = %v, wantErr %v", result.Err, tt.wantErr)
			}
			if (result.Warning != "") != tt.wantWarning {
				t.Fatalf("result.Warning = %q, wantWarning %v", result.Warning, tt.wantWarning)
			}
		})
	}
}

func TestRunWithResultsReportsUnimplementedModeAsWarning(t *testing.T) {
	previousQuiet := viper.GetBool("quiet")
	defer viper.Set("quiet", previousQuiet)
	viper.Set("quiet", true)

	oldHandler := modeHandlers["devs"]
	delete(modeHandlers, "devs")
	defer func() { modeHandlers["devs"] = oldHandler }()

	var results []ModeResult
	captureStdout(t, func() {
		results = RunWithResults(stubReader{}, []string{"devs"}, Options{
			Output: filepath.Join(t.TempDir(), "devs.png"),
		})
	})
	if len(results) != 1 {
		t.Fatalf("RunWithResults() returned %d results, want 1", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("unimplemented mode should not be a hard error: %v", results[0].Err)
	}
	if !strings.Contains(results[0].Warning, "Mode not implemented yet: devs") {
		t.Fatalf("unexpected warning: %q", results[0].Warning)
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
	_, ok := missingAnalysisWarning("devs", fmt.Errorf("plot failed at %s", time.Unix(0, 0)))
	if ok {
		t.Fatal("unexpected warning classification for a non-missing processing error")
	}
}
