package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestSelectReportAnalysisFlagsDefault(t *testing.T) {
	available := map[string]struct{}{
		"burndown":            {},
		"burndown-files":      {},
		"devs":                {},
		"onboarding":          {},
		"refactoring-proxy":   {},
		"temporal-activity":   {},
		"hotspot-risk":        {},
		"knowledge-diffusion": {},
	}
	flags, err := selectReportAnalysisFlags(available, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{
		"burndown",
		"burndown-files",
		"burndown-people",
		"devs",
		"hotspot-risk",
		"knowledge-diffusion",
		"onboarding",
		"refactoring-proxy",
		"temporal-activity",
	}
	if !reflect.DeepEqual(flags, expected) {
		t.Fatalf("unexpected flags: got %v want %v", flags, expected)
	}
}

func TestSelectReportModesDefaultIncludesMilestoneFourEasyPath(t *testing.T) {
	modes, err := selectReportModes(nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{"knowledge-diffusion", "refactoring-proxy", "hotspot-risk"} {
		found := slices.Contains(modes, expected)
		if !found {
			t.Fatalf("default report modes do not include %q: %v", expected, modes)
		}
	}
}

func TestSelectReportAnalysisFlagsRequestedUnknown(t *testing.T) {
	available := map[string]struct{}{"devs": {}}
	_, err := selectReportAnalysisFlags(available, []string{"missing"}, false)
	if err == nil {
		t.Fatal("expected error for unknown analysis flag")
	}
}

func TestSelectReportAnalysisFlagsAllUsesReportList(t *testing.T) {
	available := map[string]struct{}{
		"burndown": {},
		"shotness": {},
		"devs":     {},
	}
	flags, err := selectReportAnalysisFlags(available, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"burndown", "burndown-files", "burndown-people", "devs", "shotness"}
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
