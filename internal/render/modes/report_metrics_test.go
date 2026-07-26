package modes

import (
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/readers"
)

func TestReportMetricModesCreateOutputFiles(t *testing.T) {
	testReportMetricModesCreateOutputFiles(t, "png")
}

func TestReportMetricModesCreateSVGOutputFiles(t *testing.T) {
	testReportMetricModesCreateOutputFiles(t, "svg")
}

func testReportMetricModesCreateOutputFiles(t *testing.T, ext string) {
	t.Helper()
	reader := &reportMetricsReader{}
	tests := []struct {
		name      string
		run       func(string) error
		extras    []string
		noPrimary bool // mode does not write to args.output directly; only extras are checked
	}{
		{
			name: "temporal-activity",
			run: func(output string) error {
				return TemporalActivity(reader, output, 32, 10, nil, nil)
			},
			extras: []string{
				"temporal-activity_weekdays_commits." + ext,
				"temporal-activity_hours_commits." + ext,
				"temporal-activity_months_commits." + ext,
				"temporal-activity_weeks_commits." + ext,
				"temporal-activity_weekdays_lines." + ext,
				"temporal-activity_hours_lines." + ext,
				"temporal-activity_months_lines." + ext,
				"temporal-activity_weeks_lines." + ext,
				"temporal-activity_heatmap_commits." + ext,
				"temporal-activity_heatmap_lines." + ext,
			},
			noPrimary: true,
		},
		{
			name: "bus-factor",
			run: func(output string) error {
				return BusFactor(reader, output)
			},
			extras:    []string{"bus-factor_timeline." + ext, "bus-factor_gauge." + ext, "bus-factor_subsystems." + ext},
			noPrimary: true,
		},
		{
			name: "ownership-concentration",
			run: func(output string) error {
				return OwnershipConcentration(reader, output)
			},
			extras:    []string{"ownership-concentration_timeline." + ext, "ownership-concentration_subsystems." + ext},
			noPrimary: true,
		},
		{
			name: "knowledge-diffusion",
			run: func(output string) error {
				return KnowledgeDiffusion(reader, output, false)
			},
			extras: []string{
				"knowledge-diffusion_distribution." + ext,
				"knowledge-diffusion_silos." + ext,
				"knowledge-diffusion_lorenz." + ext,
			},
			noPrimary: true,
		},
		{
			name: "hotspot-risk",
			run: func(output string) error {
				return HotspotRisk(reader, output)
			},
			extras: []string{"hotspot-risk_table.tsv"},
		},
		{
			name: "old-vs-new",
			run: func(output string) error {
				return OldVsNew(reader, output, nil, nil, "")
			},
		},
		{
			name: "devs-parallel",
			run: func(output string) error {
				return DevsParallel(reader, output, 20, true, true)
			},
		},
		{
			name: "run-times",
			run: func(output string) error {
				return RunTimes(reader, output, true)
			},
		},
		{
			name: "devs-efforts",
			run: func(output string) error {
				return DevsEfforts(reader, output, 20, true)
			},
			extras: []string{"devs-efforts_productivity_ranking." + ext},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			output := filepath.Join(dir, tt.name+"."+ext)
			if err := tt.run(output); err != nil {
				t.Fatalf("%s() unexpected error: %v", tt.name, err)
			}
			if !tt.noPrimary {
				assertNonEmptyFile(t, output)
			}
			for _, extra := range tt.extras {
				assertNonEmptyFile(t, filepath.Join(dir, extra))
			}
		})
	}
}

func TestBusFactorSubsystemOutputPreservesTransparentBackground(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "bus-factor.png")
	if err := BusFactor(&reportMetricsReader{}, output); err != nil {
		t.Fatalf("BusFactor() unexpected error: %v", err)
	}

	file, err := os.Open(filepath.Join(dir, "bus-factor_subsystems.png")) // #nosec G304 - test path is under t.TempDir.
	if err != nil {
		t.Fatalf("open subsystem png: %v", err)
	}
	defer func() { _ = file.Close() }()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode subsystem png: %v", err)
	}
	if _, _, _, alpha := img.At(0, 0).RGBA(); alpha != 0 {
		t.Fatalf("corner alpha = %d, want transparent", alpha)
	}
}

func TestDirectoryChartModesCreatePNGAndSVGAssets(t *testing.T) {
	reader := &reportMetricsReader{}
	tests := []struct {
		name  string
		run   func(string) error
		files []string
	}{
		{
			name: "couples-shotness",
			run: func(output string) error {
				return CouplesShotness(reader, output)
			},
			files: []string{
				"shotness_coupling_heatmap.png",
				"shotness_coupling_heatmap.svg",
				"top_shotness_coupling_pairs.png",
				"top_shotness_coupling_pairs.svg",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := tt.run(dir); err != nil {
				t.Fatalf("%s() unexpected error: %v", tt.name, err)
			}
			for _, file := range tt.files {
				assertNonEmptyFile(t, filepath.Join(dir, file))
			}
		})
	}
}

func TestTopStringIntPairsPreservesPathLabels(t *testing.T) {
	labels, values := topStringIntPairs(map[string]int{
		"/":                               1,
		"cmd/hercules":                    1,
		"vendor/github.com/jeffail/tunny": 2,
	}, 0, false)

	expectedLabels := []string{"/", "cmd/hercules", "vendor/github.com/jeffail/tunny"}
	expectedValues := []int{1, 1, 2}
	if len(labels) != len(expectedLabels) {
		t.Fatalf("got %d labels, want %d: %v", len(labels), len(expectedLabels), labels)
	}
	for i := range expectedLabels {
		if labels[i] != expectedLabels[i] {
			t.Fatalf("label %d = %q, want %q", i, labels[i], expectedLabels[i])
		}
		if values[i] != expectedValues[i] {
			t.Fatalf("value %d = %d, want %d", i, values[i], expectedValues[i])
		}
	}
}

func TestBuildTemporalHourCommitSeriesPreservesDeveloperStacks(t *testing.T) {
	data := &readers.TemporalActivityData{
		People: []string{"alice", "bob"},
		Activities: map[int]readers.TemporalDeveloperActivity{
			1: {
				Hours: readers.TemporalDimensionData{
					Commits: []int{3, 0, 5},
				},
			},
			0: {
				Hours: readers.TemporalDimensionData{
					Commits: []int{2, 4},
				},
			},
		},
	}

	series := buildTemporalHourCommitSeries(data)
	if len(series) != 2 {
		t.Fatalf("series length = %d, want 2", len(series))
	}
	if series[0].Name != "alice" || series[1].Name != "bob" {
		t.Fatalf("series names = %q, %q; want alice, bob", series[0].Name, series[1].Name)
	}
	if got, want := series[0].Values[0], 2; got != want {
		t.Fatalf("alice hour 0 = %d, want %d", got, want)
	}
	if got, want := series[0].Values[1], 4; got != want {
		t.Fatalf("alice hour 1 = %d, want %d", got, want)
	}
	if got, want := series[1].Values[0], 3; got != want {
		t.Fatalf("bob hour 0 = %d, want %d", got, want)
	}
	if got, want := series[1].Values[2], 5; got != want {
		t.Fatalf("bob hour 2 = %d, want %d", got, want)
	}
}

func TestBusFactorSubsystemPairsMatchesPythonParityTieOrder(t *testing.T) {
	labels, values := busFactorSubsystemPairs(map[string]int{
		"/":                               1,
		"cmd/hercules":                    1,
		"contrib/_plugin_example":         1,
		"doc":                             1,
		"pb":                              1,
		"rbtree":                          1,
		"test_data":                       1,
		"toposort":                        1,
		"vendor/github.com/jeffail/tunny": 1,
		"yaml":                            1,
	}, 20)

	expectedLabels := []string{
		"yaml",
		"rbtree",
		"contrib/_plugin_example",
		"toposort",
		"cmd/hercules",
		"/",
		"pb",
		"doc",
		"vendor/github.com/jeffail/tunny",
		"test_data",
	}
	if len(labels) != len(expectedLabels) {
		t.Fatalf("labels length = %d, want %d", len(labels), len(expectedLabels))
	}
	for i, expected := range expectedLabels {
		if labels[i] != expected {
			t.Fatalf("label %d = %q, want %q", i, labels[i], expected)
		}
		if values[i] != 1 {
			t.Fatalf("value %d = %d, want 1", i, values[i])
		}
	}
}

func TestBusFactorSubsystemPairsCanReturnAllSubsystems(t *testing.T) {
	values := map[string]int{}
	for i := 0; i < 25; i++ {
		values[fmt.Sprintf("subsystem-%02d", i)] = i % 4
	}

	labels, gotValues := busFactorSubsystemPairs(values, 0)

	if len(labels) != len(values) {
		t.Fatalf("labels length = %d, want %d", len(labels), len(values))
	}
	if len(gotValues) != len(values) {
		t.Fatalf("values length = %d, want %d", len(gotValues), len(values))
	}
}

func TestBusFactorSubsystemPlotPixelsMatchPythonDynamicHeight(t *testing.T) {
	width, height := busFactorSubsystemPlotPixels(32)

	if width != 1200 {
		t.Fatalf("width = %d, want 1200", width)
	}
	if height != 1480 {
		t.Fatalf("height = %d, want 1480", height)
	}
}

func TestBusFactorSubsystemPlotPixelsKeepPythonMinimumHeight(t *testing.T) {
	width, height := busFactorSubsystemPlotPixels(2)

	if width != 1200 {
		t.Fatalf("width = %d, want 1200", width)
	}
	if height != 400 {
		t.Fatalf("height = %d, want 400", height)
	}
}

func TestBusFactorSubsystemYAxisMatchesPythonOrientation(t *testing.T) {
	if busFactorSubsystemInvertY() {
		t.Fatal("bus factor subsystem chart should keep Python barh default Y orientation")
	}
}

func assertNonEmptyFile(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected output file %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("expected regular file at %s, got mode %s", path, info.Mode())
	}
	if info.Size() == 0 {
		t.Fatalf("expected non-empty output file %s", path)
	}
}

type reportMetricsReader struct{}

func (r *reportMetricsReader) Read(file io.Reader) error { return nil }
func (r *reportMetricsReader) GetName() string           { return "report-metrics" }
func (r *reportMetricsReader) GetHeader() (int64, int64) { return 0, 0 }
func (r *reportMetricsReader) GetProjectBurndown() (string, [][]int) {
	return "", nil
}

func (r *reportMetricsReader) GetBurndownParameters() (burndown.BurndownParameters, error) {
	return burndown.BurndownParameters{}, nil
}

func (r *reportMetricsReader) GetProjectBurndownWithHeader() (burndown.BurndownHeader, string, [][]int, error) {
	return burndown.BurndownHeader{}, "", nil, nil
}

func (r *reportMetricsReader) GetFilesBurndown() ([]readers.FileBurndown, error) {
	return nil, fmt.Errorf("%w: files burndown", readers.ErrAnalysisMissing)
}

func (r *reportMetricsReader) GetPeopleBurndown() ([]readers.PeopleBurndown, error) {
	return nil, fmt.Errorf("%w: people burndown", readers.ErrAnalysisMissing)
}

func (r *reportMetricsReader) GetOwnershipBurndown() ([]string, map[string][][]int, error) {
	return nil, nil, fmt.Errorf("%w: ownership burndown", readers.ErrAnalysisMissing)
}

func (r *reportMetricsReader) GetPeopleInteraction() ([]string, [][]int, error) {
	return nil, nil, fmt.Errorf("%w: people interaction", readers.ErrAnalysisMissing)
}

func (r *reportMetricsReader) GetFileCooccurrence() ([]string, readers.SparseMatrix, error) {
	return nil, readers.SparseMatrix{}, fmt.Errorf("%w: file coupling", readers.ErrAnalysisMissing)
}

func (r *reportMetricsReader) GetPeopleCooccurrence() ([]string, readers.SparseMatrix, error) {
	return nil, readers.SparseMatrix{}, fmt.Errorf("%w: people coupling", readers.ErrAnalysisMissing)
}

func (r *reportMetricsReader) GetShotnessCooccurrence() ([]string, readers.SparseMatrix, error) {
	matrix := readers.SparseMatrixFromDense([][]int{
		{4, 3, 1},
		{3, 5, 2},
		{1, 2, 2},
	})
	return []string{"main.go:funcA", "main.go:funcB", "doc.md:section"}, matrix, nil
}

func (r *reportMetricsReader) GetShotnessRecords() ([]readers.ShotnessRecord, error) {
	return nil, fmt.Errorf("%w: shotness", readers.ErrAnalysisMissing)
}

func (r *reportMetricsReader) GetDeveloperStats() ([]readers.DeveloperStat, error) {
	return []readers.DeveloperStat{
		{Name: "alice", Commits: 10, LinesAdded: 100, LinesRemoved: 20, LinesModified: 30, FilesTouched: 4},
		{Name: "bob", Commits: 5, LinesAdded: 60, LinesRemoved: 10, LinesModified: 15, FilesTouched: 2},
	}, nil
}

func (r *reportMetricsReader) GetLanguageStats() ([]readers.LanguageStat, error) {
	return nil, fmt.Errorf("%w: language stats", readers.ErrAnalysisMissing)
}

func (r *reportMetricsReader) GetRuntimeStats() (map[string]float64, error) {
	return map[string]float64{
		"burndown": 20,
		"couples":  10,
		"devs":     5,
	}, nil
}

func (r *reportMetricsReader) GetDeveloperTimeSeriesData() (*readers.DeveloperTimeSeriesData, error) {
	return nil, fmt.Errorf("%w: developer time series", readers.ErrAnalysisMissing)
}

func (r *reportMetricsReader) GetTemporalActivity() (*readers.TemporalActivityData, error) {
	weekdaysCommits := make([]int, 7)
	weekdaysLines := make([]int, 7)
	weekdaysCommits[1] = 3
	weekdaysCommits[2] = 2
	weekdaysLines[1] = 30
	weekdaysLines[2] = 20
	monthsCommits := make([]int, 12)
	monthsLines := make([]int, 12)
	monthsCommits[0] = 5
	monthsLines[0] = 50
	weeksCommits := make([]int, 53)
	weeksLines := make([]int, 53)
	weeksCommits[0] = 5
	weeksLines[0] = 50
	return &readers.TemporalActivityData{
		People: []string{"alice"},
		Activities: map[int]readers.TemporalDeveloperActivity{
			0: {
				Weekdays: readers.TemporalDimensionData{Commits: weekdaysCommits, Lines: weekdaysLines},
				Hours: readers.TemporalDimensionData{
					Commits: []int{0, 2, 3},
					Lines:   []int{0, 20, 30},
				},
				Months: readers.TemporalDimensionData{Commits: monthsCommits, Lines: monthsLines},
				Weeks:  readers.TemporalDimensionData{Commits: weeksCommits, Lines: weeksLines},
			},
		},
		Ticks: map[int]map[int]readers.TemporalActivityTick{
			0: {0: {Commits: 2, Lines: 20, Hour: 1, Weekday: 1, Month: 0, Week: 0}},
			1: {0: {Commits: 3, Lines: 30, Hour: 2, Weekday: 2, Month: 0, Week: 0}},
		},
		TickSize: int64(24 * 60 * 60 * 1_000_000_000),
	}, nil
}

func (r *reportMetricsReader) GetBusFactor() (*readers.BusFactorData, error) {
	return &readers.BusFactorData{
		People: []string{"alice", "bob"},
		Snapshots: map[int]readers.BusFactorSnapshot{
			0: {BusFactor: 1, TotalLines: 100, AuthorLines: map[int]int64{0: 80, 1: 20}},
			1: {BusFactor: 2, TotalLines: 120, AuthorLines: map[int]int64{0: 70, 1: 50}},
		},
		SubsystemBusFactor: map[string]int{"core": 1},
		Threshold:          0.8,
	}, nil
}

func (r *reportMetricsReader) GetOwnershipConcentration() (*readers.OwnershipConcentrationData, error) {
	return &readers.OwnershipConcentrationData{
		People: []string{"alice", "bob"},
		Snapshots: map[int]readers.OwnershipConcentrationSnapshot{
			0: {Gini: 0.2, HHI: 0.4, TotalLines: 100},
			1: {Gini: 0.3, HHI: 0.5, TotalLines: 120},
		},
		SubsystemGini: map[string]float64{"core": 0.2},
		SubsystemHHI:  map[string]float64{"core": 0.4},
	}, nil
}

func (r *reportMetricsReader) GetKnowledgeDiffusion() (*readers.KnowledgeDiffusionData, error) {
	return &readers.KnowledgeDiffusionData{
		People: []string{"alice", "bob"},
		Files: map[string]readers.KnowledgeDiffusionFile{
			"main.go": {UniqueEditors: 2, RecentEditors: 1, UniqueEditorsOverTime: map[int]int{0: 1, 1: 2}},
			"doc.md":  {UniqueEditors: 1, RecentEditors: 1, UniqueEditorsOverTime: map[int]int{0: 1}},
		},
		Distribution: map[int]int{1: 1, 2: 1},
		WindowMonths: 6,
	}, nil
}

func (r *reportMetricsReader) GetHotspotRisk() (*readers.HotspotRiskData, error) {
	return &readers.HotspotRiskData{
		WindowDays: 90,
		Files: []readers.HotspotRiskFile{
			{Path: "main.go", RiskScore: 0.9, Size: 100, Churn: 20},
			{Path: "doc.md", RiskScore: 0.2, Size: 10, Churn: 1},
		},
	}, nil
}
