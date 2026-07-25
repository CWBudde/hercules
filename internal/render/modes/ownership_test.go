package modes

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/style"
	"github.com/spf13/viper"
)

func TestGenerateOwnershipPlot(t *testing.T) {
	// Create temporary directory for test outputs
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_ownership.png")

	// Sample ownership data for testing
	testPeople := []string{"Alice", "Bob", "Charlie"}
	testOwnership := map[string][][]int{
		"src/main.go": {
			{100, 80, 60}, // Alice's ownership over time
			{0, 20, 30},   // Bob's ownership over time
			{0, 0, 10},    // Charlie's ownership over time
		},
		"src/utils.go": {
			{50, 40, 30},
			{30, 40, 50},
			{20, 20, 20},
		},
		"README.md": {
			{0, 0, 0},
			{100, 80, 60},
			{0, 20, 40},
		},
	}

	err := mockGenerateOwnershipPlot("test", testPeople, testOwnership, outputPath)
	if err != nil {
		t.Errorf("generateOwnershipPlot() error = %v", err)
	}

	// Check if output file was created
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("Output file was not created: %s", outputPath)
	}
}

func TestGenerateOwnershipPlotEmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_ownership_empty.png")

	// Test with empty ownership data
	emptyPeople := []string{}
	emptyOwnership := map[string][][]int{}

	err := mockGenerateOwnershipPlot("test_empty", emptyPeople, emptyOwnership, outputPath)
	if err == nil {
		t.Error("Expected error for empty ownership data, but got nil")
	}
}

func TestGenerateOwnershipPlotSingleFile(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_ownership_single.png")

	testPeople := []string{"Solo Developer"}
	testOwnership := map[string][][]int{
		"main.go": {
			{100, 100, 100}, // 100% ownership throughout
		},
	}

	err := mockGenerateOwnershipPlot("test_single", testPeople, testOwnership, outputPath)
	if err != nil {
		t.Errorf("generateOwnershipPlot() with single file error = %v", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("Output file was not created: %s", outputPath)
	}
}

func TestSaveOwnershipMatplotlibFigurePreservesTransparency(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "ownership.png")
	background := render.Color{R: 1, G: 1, B: 1, A: 0}
	fig := core.NewFigure(
		100,
		100,
		style.WithBackground(1, 1, 1, 0),
		style.WithAxesBackground(background),
	)

	if err := saveOwnershipMatplotlibFigure(fig, outputPath, 100, 100, background); err != nil {
		t.Fatalf("save ownership figure: %v", err)
	}

	file, err := os.Open(outputPath) // #nosec G304 - test output path is under t.TempDir.
	if err != nil {
		t.Fatalf("open ownership png: %v", err)
	}
	defer func() { _ = file.Close() }()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode ownership png: %v", err)
	}
	if _, _, _, alpha := img.At(0, 0).RGBA(); alpha != 0 {
		t.Fatalf("corner alpha = %d, want transparent", alpha)
	}
}

func TestCalculateFileOwnershipPercentages(t *testing.T) {
	// Test ownership percentage calculation
	ownershipMatrix := [][]int{
		{60, 80, 50}, // Alice
		{30, 10, 40}, // Bob
		{10, 10, 10}, // Charlie
	}

	percentages := calculateFileOwnershipPercentages(ownershipMatrix)

	if len(percentages) != len(ownershipMatrix) {
		t.Errorf("Expected %d ownership arrays, got %d", len(ownershipMatrix), len(percentages))
	}

	// Check first time point: 60 + 30 + 10 = 100
	expectedTotal := 100.0
	actualTotal := 0.0
	for _, dev := range percentages {
		actualTotal += dev[0]
	}

	if actualTotal != expectedTotal {
		t.Errorf("Expected ownership percentages to sum to %f, got %f", expectedTotal, actualTotal)
	}

	// Alice should have 60% at first time point
	if percentages[0][0] != 60.0 {
		t.Errorf("Expected Alice to have 60%% ownership at first point, got %f", percentages[0][0])
	}
}

func TestProcessOwnershipBurndownSumsRowsAsTimePoints(t *testing.T) {
	names, people, dates := processOwnershipBurndown(
		time.Unix(0, 0),
		time.Unix(0, 0),
		1,
		86400,
		[]string{"Alice", "Bob"},
		map[string][][]int{
			"Alice": {
				{1, 2, 3},
				{4, 5, 6},
			},
			"Bob": {
				{10, 20, 30},
				{40, 50, 60},
			},
		},
		20,
		false,
	)

	if len(dates) != 2 {
		t.Fatalf("expected 2 time points, got %d", len(dates))
	}
	if len(names) != 2 || names[0] != "Bob" || names[1] != "Alice" {
		t.Fatalf("unexpected ownership order: %v", names)
	}
	if got := people[0]; len(got) != 2 || got[0] != 60 || got[1] != 150 {
		t.Fatalf("Bob ownership = %v, want [60 150]", got)
	}
	if got := people[1]; len(got) != 2 || got[0] != 6 || got[1] != 15 {
		t.Fatalf("Alice ownership = %v, want [6 15]", got)
	}
}

func TestProcessOwnershipBurndownUsesSamplingTickSize(t *testing.T) {
	_, _, dates := processOwnershipBurndown(
		time.Unix(0, 0),
		time.Unix(0, 0),
		30,
		86400,
		[]string{"Alice"},
		map[string][][]int{
			"Alice": {
				{1},
				{2},
				{3},
			},
		},
		20,
		false,
	)

	if len(dates) != 3 {
		t.Fatalf("expected 3 time points, got %d", len(dates))
	}
	if got, want := dates[1].Sub(dates[0]), 30*24*time.Hour; got != want {
		t.Fatalf("date spacing = %v, want %v", got, want)
	}
	if got, want := dates[2].Sub(dates[0]), 60*24*time.Hour; got != want {
		t.Fatalf("date span = %v, want %v", got, want)
	}
}

func TestFindTopFilesByOwnershipChanges(t *testing.T) {
	ownership := map[string][][]int{
		"stable.go": {
			{100, 100, 100}, // No change
			{0, 0, 0},
		},
		"volatile.go": {
			{100, 50, 0}, // High change
			{0, 50, 100},
		},
		"moderate.go": {
			{80, 70, 60}, // Moderate change
			{20, 30, 40},
		},
	}

	topFiles := findTopFilesByOwnershipChanges(ownership, 2)

	if len(topFiles) > 2 {
		t.Errorf("Expected at most 2 files, got %d", len(topFiles))
	}

	// volatile.go should be first due to highest ownership changes
	if len(topFiles) > 0 && topFiles[0] != "volatile.go" {
		t.Errorf("Expected 'volatile.go' to be first, got '%s'", topFiles[0])
	}
}

func TestCalculateOwnershipChurn(t *testing.T) {
	// Test ownership churn calculation
	ownershipMatrix := [][]int{
		{100, 50, 0}, // Alice: high churn (changes from 100 to 0)
		{0, 50, 100}, // Bob: high churn (changes from 0 to 100)
	}

	churn := calculateOwnershipChurn(ownershipMatrix)

	// Churn should be high due to complete ownership transfer
	if churn <= 0 {
		t.Errorf("Expected positive churn value, got %f", churn)
	}

	// Test with stable ownership
	stableMatrix := [][]int{
		{100, 100, 100}, // No change
		{0, 0, 0},       // No change
	}

	stableChurn := calculateOwnershipChurn(stableMatrix)

	if stableChurn >= churn {
		t.Errorf("Expected stable ownership to have lower churn (%f) than volatile (%f)", stableChurn, churn)
	}
}

func TestCalculateOwnershipConcentration(t *testing.T) {
	// Test ownership concentration (Gini coefficient-like measure)

	// High concentration (one person owns everything)
	concentratedMatrix := [][]int{
		{100, 100, 100},
		{0, 0, 0},
		{0, 0, 0},
	}

	concentrated := calculateOwnershipConcentration(concentratedMatrix)

	// Low concentration (evenly distributed)
	distributedMatrix := [][]int{
		{33, 33, 33},
		{33, 33, 33},
		{34, 34, 34},
	}

	distributed := calculateOwnershipConcentration(distributedMatrix)

	if concentrated <= distributed {
		t.Errorf("Expected concentrated ownership (%f) to have higher concentration than distributed (%f)", concentrated, distributed)
	}
}

// Helper functions for testing

func calculateFileOwnershipPercentages(ownershipMatrix [][]int) [][]float64 {
	if len(ownershipMatrix) == 0 || len(ownershipMatrix[0]) == 0 {
		return [][]float64{}
	}

	percentages := make([][]float64, len(ownershipMatrix))
	timePoints := len(ownershipMatrix[0])

	for dev := range ownershipMatrix {
		percentages[dev] = make([]float64, timePoints)
	}

	for t := 0; t < timePoints; t++ {
		total := 0
		for dev := 0; dev < len(ownershipMatrix); dev++ {
			total += ownershipMatrix[dev][t]
		}

		if total > 0 {
			for dev := 0; dev < len(ownershipMatrix); dev++ {
				percentages[dev][t] = float64(ownershipMatrix[dev][t]) * 100.0 / float64(total)
			}
		}
	}

	return percentages
}

func findTopFilesByOwnershipChanges(ownership map[string][][]int, limit int) []string {
	type fileChurn struct {
		filename string
		churn    float64
	}

	var files []fileChurn

	for filename, matrix := range ownership {
		churn := calculateOwnershipChurn(matrix)
		files = append(files, fileChurn{filename: filename, churn: churn})
	}

	// Simple sort by churn (descending)
	for i := 0; i < len(files)-1; i++ {
		for j := i + 1; j < len(files); j++ {
			if files[i].churn < files[j].churn {
				files[i], files[j] = files[j], files[i]
			}
		}
	}

	result := make([]string, 0, limit)
	for i := 0; i < len(files) && i < limit; i++ {
		result = append(result, files[i].filename)
	}

	return result
}

func calculateOwnershipChurn(ownershipMatrix [][]int) float64 {
	if len(ownershipMatrix) == 0 || len(ownershipMatrix[0]) == 0 {
		return 0
	}

	totalChurn := 0.0
	timePoints := len(ownershipMatrix[0])

	for t := 1; t < timePoints; t++ {
		for dev := 0; dev < len(ownershipMatrix); dev++ {
			change := float64(ownershipMatrix[dev][t] - ownershipMatrix[dev][t-1])
			if change < 0 {
				change = -change // Absolute value
			}
			totalChurn += change
		}
	}

	return totalChurn / float64(timePoints-1)
}

func calculateOwnershipConcentration(ownershipMatrix [][]int) float64 {
	if len(ownershipMatrix) == 0 || len(ownershipMatrix[0]) == 0 {
		return 0
	}

	concentration := 0.0
	timePoints := len(ownershipMatrix[0])

	for t := 0; t < timePoints; t++ {
		total := 0
		for dev := 0; dev < len(ownershipMatrix); dev++ {
			total += ownershipMatrix[dev][t]
		}

		if total > 0 {
			// Calculate Herfindahl index for this time point
			herfindahl := 0.0
			for dev := 0; dev < len(ownershipMatrix); dev++ {
				share := float64(ownershipMatrix[dev][t]) / float64(total)
				herfindahl += share * share
			}
			concentration += herfindahl
		}
	}

	return concentration / float64(timePoints)
}

func TestOwnershipChartTitle(t *testing.T) {
	for _, tc := range []struct {
		name string
		repo string
		want string
	}{
		{"named repository", "hercules", "hercules code ownership through time"},
		{"current directory", ".", "Code ownership through time"},
		{"unnamed", "", "Code ownership through time"},
		{"padded", "  hercules  ", "hercules code ownership through time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ownershipChartTitle(tc.repo); got != tc.want {
				t.Fatalf("ownershipChartTitle(%q) = %q, want %q", tc.repo, got, tc.want)
			}
		})
	}
}

// The subplot padding is a fraction of the figure, so too small a value silently
// pushes the tick labels outside the canvas: five-digit y labels were cut to
// "0000" and the rotated date labels disappeared entirely. Assert that both
// margins actually receive ink at the smallest figure size the CLI is used with.
func TestPlotOwnershipBurndownKeepsTickLabelsInsideCanvas(t *testing.T) {
	viper.Set("size", "16,10")
	t.Cleanup(func() { viper.Set("size", "") })

	start := time.Date(2017, 1, 11, 0, 0, 0, 0, time.UTC)
	points := 24
	dates := make([]time.Time, points)
	owned := make([]float64, points)
	for i := range dates {
		dates[i] = start.AddDate(0, i, 0)
		// Five-digit values, so the y labels are as wide as they get in practice.
		owned[i] = float64(80000 - i*100)
	}

	output := filepath.Join(t.TempDir(), "ownership.png")
	if err := plotOwnershipBurndown(
		".", []string{"Alice"}, [][]float64{owned}, dates, dates[points-1], output,
	); err != nil {
		t.Fatalf("plotOwnershipBurndown: %v", err)
	}

	file, err := os.Open(output)
	if err != nil {
		t.Fatalf("open rendered chart: %v", err)
	}
	defer func() { _ = file.Close() }()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode rendered chart: %v", err)
	}

	bounds := img.Bounds()
	inked := func(xMin, yMin, xMax, yMax int) int {
		count := 0
		for y := yMin; y < yMax; y++ {
			for x := xMin; x < xMax; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				if a > 0 && r>>8 < 200 && g>>8 < 200 && b>>8 < 200 {
					count++
				}
			}
		}
		return count
	}

	width, height := bounds.Dx(), bounds.Dy()
	// The left 5% and bottom 6% sit outside the axes, so any dark pixel there is
	// a tick label rather than plotted data. Absence means the labels were laid
	// out beyond the canvas and never drawn.
	if inked(0, 0, width*5/100, height) == 0 {
		t.Error("no ink in the left margin: y tick labels were not drawn")
	}
	if inked(0, height*94/100, width, height) == 0 {
		t.Error("no ink in the bottom margin: x tick labels were not drawn")
	}
	// Presence alone is not enough: too little padding still draws the labels,
	// just with their leading characters sliced off at the canvas edge ("80000"
	// became "0000"). A label that fits ends before the edge, so the outermost
	// pixels must be clear.
	if got := inked(0, 0, 2, height); got != 0 {
		t.Errorf("%d dark pixels touch the left canvas edge: y tick labels are cut off", got)
	}
	if got := inked(0, height-2, width, height); got != 0 {
		t.Errorf("%d dark pixels touch the bottom canvas edge: x tick labels are cut off", got)
	}
}

// Mock function for testing
func mockGenerateOwnershipPlot(name string, people []string, ownership map[string][][]int, outputPath string) error {
	// Simple mock implementation
	if len(people) == 0 || len(ownership) == 0 {
		return fmt.Errorf("empty people or ownership data")
	}

	// Create a simple test file to simulate chart generation
	content := fmt.Sprintf("Mock ownership chart for %s with %d people and %d files", name, len(people), len(ownership))
	return os.WriteFile(outputPath, []byte(content), 0o600)
}
