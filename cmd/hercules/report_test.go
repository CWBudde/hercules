package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/hercules/internal/pb"
)

func TestSelectReportAnalysisFlagsDefault(t *testing.T) {
	available := map[string]struct{}{
		reportAnalysisBurndown:           {},
		reportAnalysisBurndownFiles:      {},
		reportAnalysisDevs:               {},
		reportAnalysisOnboarding:         {},
		reportAnalysisRefactoringProxy:   {},
		reportAnalysisTemporalActivity:   {},
		reportAnalysisHotspotRisk:        {},
		reportAnalysisKnowledgeDiffusion: {},
	}
	flags, err := selectReportAnalysisFlags(available, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{
		reportAnalysisBurndown,
		reportAnalysisBurndownFiles,
		reportAnalysisBurndownPeople,
		reportAnalysisDevs,
		reportAnalysisHotspotRisk,
		reportAnalysisKnowledgeDiffusion,
		reportAnalysisOnboarding,
		reportAnalysisRefactoringProxy,
		reportAnalysisTemporalActivity,
	}
	if !reflect.DeepEqual(flags, expected) {
		t.Fatalf("unexpected flags: got %v want %v", flags, expected)
	}
}

func TestDefaultReportAnalysesLinkToMetricDefinitions(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	readme := string(payload)
	start := strings.Index(readme, "Every analysis enabled by the default report")
	end := strings.Index(readme, "The exact files emitted by each mode")
	if start < 0 || end <= start {
		t.Fatal("README default-analysis metric table is missing")
	}
	table := readme[start:end]

	for _, analysis := range reportDefaultAnalysisFlags {
		flag := "`--" + analysis + "`"
		var linked bool
		for line := range strings.Lines(table) {
			if strings.Contains(line, flag) && strings.Contains(line, "](docs/SCHEMAS.md#") {
				linked = true
				break
			}
		}
		if !linked {
			t.Errorf("default report analysis %s does not link to a metric definition", flag)
		}
	}
}

func TestSelectReportModesDefaultIncludesMilestoneFourEasyPath(t *testing.T) {
	modes, err := selectReportModes(nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		reportAnalysisKnowledgeDiffusion,
		reportAnalysisRefactoringProxy,
		reportAnalysisHotspotRisk,
	} {
		found := slices.Contains(modes, expected)
		if !found {
			t.Fatalf("default report modes do not include %q: %v", expected, modes)
		}
	}
}

func TestSelectReportAnalysisFlagsRequestedUnknown(t *testing.T) {
	available := map[string]struct{}{reportAnalysisDevs: {}}
	_, err := selectReportAnalysisFlags(available, []string{"missing"}, false)
	if err == nil {
		t.Fatal("expected error for unknown analysis flag")
	}
}

func TestSelectReportAnalysisFlagsAllUsesReportList(t *testing.T) {
	available := map[string]struct{}{
		reportAnalysisBurndown: {},
		reportAnalysisShotness: {},
		reportAnalysisDevs:     {},
	}
	flags, err := selectReportAnalysisFlags(available, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{
		reportAnalysisBurndown,
		reportAnalysisBurndownFiles,
		reportAnalysisBurndownPeople,
		reportAnalysisDevs,
		reportAnalysisShotness,
	}
	if !reflect.DeepEqual(flags, expected) {
		t.Fatalf("unexpected flags: got %v want %v", flags, expected)
	}
}

func TestSelectReportModesAll(t *testing.T) {
	modes, err := selectReportModes(nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modes) != len(reportAllModes) {
		t.Fatalf("unexpected mode count: got %d want %d", len(modes), len(reportAllModes))
	}
}

func TestValidateReportLaboursFlags(t *testing.T) {
	if err := validateReportLaboursFlags("", nil); err != nil {
		t.Fatalf("in-process default should be valid: %v", err)
	}
	if err := validateReportLaboursFlags("./labours", []string{"--relative"}); err != nil {
		t.Fatalf("labours-arg with labours-cmd should be valid: %v", err)
	}
	if err := validateReportLaboursFlags("", []string{"--relative"}); err == nil {
		t.Fatal("expected error for --labours-arg without --labours-cmd")
	}
	for _, argument := range []string{"-o", "--output=/tmp/out", "--mode=devs", "--backend"} {
		if err := validateReportLaboursFlags("./labours", []string{argument}); err == nil {
			t.Fatalf("expected error for report-controlled labours argument %q", argument)
		}
	}
}

func TestSanitizePathComponent(t *testing.T) {
	if got, want := sanitizePathComponent("bus factor/2026"), "bus_factor_2026"; got != want {
		t.Fatalf("unexpected sanitized value: got %q want %q", got, want)
	}
}

func TestCollectReportAssets(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(path string) {
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		if err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
		err = os.WriteFile(path, []byte("x"), 0o600)
		if err != nil {
			t.Fatalf("write failed: %v", err)
		}
	}
	mustWrite(filepath.Join(tmp, "charts", "a.png"))
	mustWrite(filepath.Join(tmp, "charts", "b.svg"))
	mustWrite(filepath.Join(tmp, "report.pb"))
	mustWrite(filepath.Join(tmp, "chart.json"))

	plots, assets, err := collectReportAssets(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedPlots := []string{"charts/a.png", "charts/b.svg"}
	expectedAssets := []string{"chart.json", "report.pb"}
	if !reflect.DeepEqual(plots, expectedPlots) {
		t.Fatalf("unexpected plots: got %v want %v", plots, expectedPlots)
	}
	if !reflect.DeepEqual(assets, expectedAssets) {
		t.Fatalf("unexpected assets: got %v want %v", assets, expectedAssets)
	}
}

func TestFinalizeReportWritesPartialReportAndReturnsFailure(t *testing.T) {
	output := t.TempDir()
	requireTestFile(t, filepath.Join(output, "charts", "devs.png"), "chart")
	err := finalizeReport(
		reportOptions{outputDir: output, format: "png"},
		pb.AnalysisResults{},
		nil,
		[]string{"devs", "languages"},
		reportModeResults{
			Warnings: []reportModeWarning{{Mode: "devs", Warning: "limited data"}},
			Failures: []reportModeFailure{{Mode: "languages", Error: "missing font"}},
		},
	)
	if err == nil {
		t.Fatal("finalizeReport() succeeded despite a hard mode failure")
	}
	if _, statErr := os.Stat(filepath.Join(output, "index.html")); statErr != nil {
		t.Fatalf("partial report index was not written: %v", statErr)
	}

	index, readErr := os.ReadFile(filepath.Join(output, "index.html"))
	if readErr != nil {
		t.Fatalf("read report index: %v", readErr)
	}
	if !strings.Contains(string(index), "limited data") ||
		!strings.Contains(string(index), "missing font") {
		t.Fatalf("report index omitted warnings or failures:\n%s", index)
	}

	payload, readErr := os.ReadFile(filepath.Join(output, "manifest.json"))
	if readErr != nil {
		t.Fatalf("read report manifest: %v", readErr)
	}
	var manifest reportManifest
	if unmarshalErr := json.Unmarshal(payload, &manifest); unmarshalErr != nil {
		t.Fatalf("decode report manifest: %v", unmarshalErr)
	}
	for _, expected := range []string{
		"charts/devs.png", "index.html", "manifest.json",
	} {
		if !slices.Contains(manifest.Files, expected) {
			t.Fatalf("manifest files %v do not include %q", manifest.Files, expected)
		}
	}
	if len(manifest.Warnings) != 1 || len(manifest.Failures) != 1 {
		t.Fatalf("manifest omitted outcomes: %#v", manifest)
	}
}

func TestPublishReportReplacesOldTreeWithoutStaleAssets(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "report")
	requireTestFile(t, filepath.Join(output, "charts", "stale.png"), "stale")
	requireTestFile(t, filepath.Join(output, "index.html"), "old")

	destination, stagingDir, err := prepareReportStaging(output)
	if err != nil {
		t.Fatalf("prepareReportStaging() failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stagingDir) })
	requireTestFile(t, filepath.Join(stagingDir, "charts", "current.png"), "current")
	requireTestFile(t, filepath.Join(stagingDir, "index.html"), "new")

	if err := publishReport(stagingDir, destination); err != nil {
		if errors.Is(err, errAtomicCacheReplacement) {
			t.Skipf("atomic directory replacement is unavailable: %v", err)
		}
		t.Fatalf("publishReport() failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(output, "charts", "stale.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale chart survived report replacement: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(output, "charts", "current.png"))
	if err != nil {
		t.Fatalf("read current chart: %v", err)
	}
	if string(content) != "current" {
		t.Fatalf("current chart content = %q, want current", content)
	}
}

func TestReportPublicationFailureLeavesPreviousReportIntact(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "report")
	requireTestFile(t, filepath.Join(output, "index.html"), "complete previous report")

	destination, stagingDir, err := prepareReportStaging(output)
	if err != nil {
		t.Fatalf("prepareReportStaging() failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stagingDir) })
	requireTestFile(t, filepath.Join(stagingDir, "index.html"), "incomplete replacement")

	previousSwap := swapReportDirectories
	swapReportDirectories = func(_, _ string) error {
		return errors.New("injected atomic publication failure")
	}
	t.Cleanup(func() { swapReportDirectories = previousSwap })

	err = publishReport(stagingDir, destination)
	if err == nil {
		t.Fatal("publishReport() succeeded despite injected swap failure")
	}
	content, readErr := os.ReadFile(filepath.Join(output, "index.html"))
	if readErr != nil {
		t.Fatalf("read previous report: %v", readErr)
	}
	if string(content) != "complete previous report" {
		t.Fatalf("previous report changed after failed publication: %q", content)
	}
}

func TestAbandonedStrictReportStagingLeavesPreviousReportIntact(t *testing.T) {
	output := filepath.Join(t.TempDir(), "report")
	requireTestFile(t, filepath.Join(output, "index.html"), "complete previous report")

	_, stagingDir, err := prepareReportStaging(output)
	if err != nil {
		t.Fatalf("prepareReportStaging() failed: %v", err)
	}
	requireTestFile(t, filepath.Join(stagingDir, "charts", "partial.png"), "partial")
	if err := os.RemoveAll(stagingDir); err != nil {
		t.Fatalf("remove failed strict-mode staging directory: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(output, "index.html"))
	if err != nil {
		t.Fatalf("read previous report: %v", err)
	}
	if string(content) != "complete previous report" {
		t.Fatalf("strict failure changed previous report: %q", content)
	}
}

func requireTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create test directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}
