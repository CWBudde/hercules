package modes

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

func TestBuildDevEffortsMatrixClampsNegativeDailyTotals(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	timeSeries := &readers.DeveloperTimeSeriesData{
		People: []string{"Negative Spike Developer"},
		Days: map[int]map[int]readers.DevDay{
			0: {0: {LinesAdded: 10}},
			1: {0: {LinesModified: -50000}},
			2: {0: {LinesRemoved: 5}},
		},
	}

	matrix, err := buildDevEffortsMatrix(
		timeSeries,
		start.Unix(),
		start.AddDate(0, 0, 2).Unix(),
		8,
		true,
	)
	if err != nil {
		t.Fatalf("build devs-efforts matrix: %v", err)
	}

	want := []float64{10, 10, 15}
	if len(matrix.CumLayers) == 0 || len(matrix.CumLayers[0]) != len(want) {
		t.Fatalf("cumulative layers have unexpected shape: %v", matrix.CumLayers)
	}
	for i, value := range matrix.CumLayers[0] {
		if value != want[i] {
			t.Fatalf("cumulative effort = %v, want %v", matrix.CumLayers[0], want)
		}
	}
}

func TestDevsEffortsVisualHistoricalFixtureStaysNonNegative(t *testing.T) {
	fixture := filepath.Join("..", "testdata", "hercules", "report_default.pb")
	file, err := os.Open(fixture) // #nosec G304 - fixture path is fixed above.
	if err != nil {
		t.Fatalf("open historical fixture: %v", err)
	}
	defer func() { _ = file.Close() }()

	reader := &readers.ProtobufReader{}
	err = reader.Read(file)
	if err != nil {
		t.Fatalf("read historical fixture: %v", err)
	}
	timeSeries, err := reader.GetDeveloperTimeSeriesData()
	if err != nil {
		t.Fatalf("get historical developer time series: %v", err)
	}
	startUnix, endUnix := reader.GetHeader()

	maxDailyEffort := 0
	for _, developers := range timeSeries.Days {
		for _, stats := range developers {
			if effort := nonNegativeDevEffort(stats); effort > maxDailyEffort {
				maxDailyEffort = effort
			}
		}
	}
	if maxDailyEffort < 1000 {
		t.Fatalf("fixture no longer contains the large daily effort spike: max=%d", maxDailyEffort)
	}

	matrix, err := buildDevEffortsMatrix(timeSeries, startUnix, endUnix, 8, true)
	if err != nil {
		t.Fatalf("build historical devs-efforts matrix: %v", err)
	}
	for i, layer := range matrix.CumLayers {
		for j, value := range layer {
			if value < 0 {
				t.Fatalf("cumulative layer[%d][%d] = %v, want non-negative", i, j, value)
			}
		}
	}

	output := filepath.Join(t.TempDir(), "devs-efforts.png")
	err = plotDevEffortsTimeSeries(matrix, output, graphics.DefaultOptions())
	if err != nil {
		t.Fatalf("render historical devs-efforts chart: %v", err)
	}
	rendered, err := os.Open(output) // #nosec G304 - test path is under t.TempDir.
	if err != nil {
		t.Fatalf("open rendered devs-efforts chart: %v", err)
	}
	defer func() { _ = rendered.Close() }()
	_, err = png.Decode(rendered)
	if err != nil {
		t.Fatalf("decode rendered devs-efforts chart: %v", err)
	}
}
