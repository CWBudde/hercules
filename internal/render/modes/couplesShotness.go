package modes

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

var errNoCouplingPairs = errors.New("no coupling pairs data available")

// CouplesShotness visualizes dot-product similarity between aligned Shotness
// entity co-occurrence profiles. The heatmap diagonal is each entity's squared
// profile magnitude; ranked pairs contain only distinct entities.
func CouplesShotness(reader readers.Reader, output string) error {
	return CouplesShotnessWithOptions(reader, output, defaultOptions())
}

func CouplesShotnessWithOptions(reader readers.Reader, output string, opts Options) error {
	return runCouplingMode(
		"Shotness Coupling Analysis",
		"shotness coupling",
		output,
		reader.GetShotnessCooccurrence,
		func(names []string, matrix readers.SparseMatrix, output string) error {
			return plotShotnessCouplingWithOptions(analyzeShotnessCoupling(names, matrix), output, opts.Graphics)
		},
		opts.Quiet,
	)
}

// ShotnessCouplingPair represents a coupling relationship between two shotness entities.
type ShotnessCouplingPair struct {
	Entity1          string
	Entity2          string
	CouplingScore    float64
	CooccuranceCount int
}

// ShotnessCouplingAnalysis represents the complete shotness coupling analysis results.
type ShotnessCouplingAnalysis struct {
	EntityNames    []string
	CouplingMatrix readers.SparseMatrix
	TopCoupling    []ShotnessCouplingPair
	Statistics     ShotnessCouplingStatistics
}

// ShotnessCouplingStatistics provides summary statistics about shotness coupling.
type ShotnessCouplingStatistics struct {
	TotalEntities   int
	TotalCouplings  int
	AverageCoupling float64
	MaxCoupling     int
	MinCoupling     int
}

// analyzeShotnessCoupling derives ranked pairs and summary statistics directly
// from the same symmetric entity matrix that is rendered as the heatmap.
func analyzeShotnessCoupling(
	entityNames []string, couplingMatrix readers.SparseMatrix,
) ShotnessCouplingAnalysis {
	pairs, stats := analyzeCouplingPairs(entityNames, couplingMatrix, 25)
	if len(pairs) == 0 {
		stats.Min = 0
	}

	shotnessPairs := make([]ShotnessCouplingPair, len(pairs))
	for i, pair := range pairs {
		shotnessPairs[i] = ShotnessCouplingPair{
			Entity1:          pair.Name1,
			Entity2:          pair.Name2,
			CouplingScore:    pair.Score,
			CooccuranceCount: pair.Count,
		}
	}

	analysis := ShotnessCouplingAnalysis{
		EntityNames:    entityNames,
		CouplingMatrix: couplingMatrix,
		TopCoupling:    shotnessPairs,
		Statistics: ShotnessCouplingStatistics{
			TotalEntities:   len(entityNames),
			TotalCouplings:  stats.Total,
			AverageCoupling: stats.Average,
			MaxCoupling:     stats.Max,
			MinCoupling:     stats.Min,
		},
	}

	return analysis
}

// plotShotnessCoupling generates coupling visualization plots.
func plotShotnessCoupling(analysis ShotnessCouplingAnalysis, output string) error {
	return plotShotnessCouplingWithOptions(analysis, output, graphics.DefaultOptions())
}

func plotShotnessCouplingWithOptions(
	analysis ShotnessCouplingAnalysis,
	output string,
	visuals graphics.Options,
) error {
	// Create heatmap for shotness entities
	err := plotShotnessCouplingHeatmap(analysis, output, visuals)
	if err != nil {
		return err
	}

	if len(analysis.TopCoupling) == 0 {
		return nil
	}

	// Create bar chart of top coupling pairs
	err = plotTopShotnessCouplingPairs(analysis, output, visuals)
	if err != nil {
		return err
	}

	return nil
}

// plotShotnessCouplingHeatmap creates a heatmap of shotness coupling relationships.
func plotShotnessCouplingHeatmap(
	analysis ShotnessCouplingAnalysis,
	output string,
	optionValues ...graphics.Options,
) error {
	if analysis.CouplingMatrix.Rows == 0 {
		return errNoCouplingMatrix
	}

	pngFile := filepath.Join(output, "shotness_coupling_heatmap.png")

	err := plotPythonCouplingHeatmap(
		"Shotness Coupling Heatmap", pngFile, analysis.EntityNames,
		analysis.CouplingMatrix, "Greens", optionValues...,
	)
	if err != nil {
		return fmt.Errorf("failed to save heatmap: %w", err)
	}

	svgFile := filepath.Join(output, "shotness_coupling_heatmap.svg")

	err = plotPythonCouplingHeatmap(
		"Shotness Coupling Heatmap", svgFile, analysis.EntityNames,
		analysis.CouplingMatrix, "Greens", optionValues...,
	)
	if err != nil {
		return fmt.Errorf("failed to save heatmap: %w", err)
	}

	fmt.Printf("Saved shotness coupling heatmap to %s and %s\n", pngFile, svgFile)

	return nil
}

// plotTopShotnessCouplingPairs creates a bar chart of the most coupled shotness entities.
func plotTopShotnessCouplingPairs(
	analysis ShotnessCouplingAnalysis,
	output string,
	optionValues ...graphics.Options,
) error {
	visuals := graphics.DefaultOptions()
	if len(optionValues) > 0 {
		visuals = optionValues[0]
	}

	if len(analysis.TopCoupling) == 0 {
		return errNoCouplingPairs
	}

	values, rankLabels, barLabels := shotnessCouplingBarData(analysis.TopCoupling)

	if output == "" {
		output = "."
	}

	err := os.MkdirAll(output, 0o750)
	if err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", output, err)
	}

	opts := graphics.MatplotlibBarOptions{
		Title:         "Top Shotness Coupling Pairs",
		XLabel:        "Coupling Pair Rank",
		YLabel:        "Coupling Score",
		WidthInches:   16,
		HeightInches:  8,
		Color:         color.RGBA{R: 76, G: 120, B: 168, A: 255},
		DisableGrid:   true,
		YMax:          maxCouplingValue(values) * 1.05,
		BarLabels:     barLabels,
		BarLabelAngle: 70,
		FontSize:      visuals.PlotFontSize(),
	}

	outputs := []string{
		filepath.Join(output, "top_shotness_coupling_pairs.png"),
		filepath.Join(output, "top_shotness_coupling_pairs.svg"),
	}
	for _, outputPath := range outputs {
		opts.Output = outputPath

		err := graphics.PlotBarChartMatplotlib(rankLabels, values, opts)
		if err != nil {
			return fmt.Errorf("failed to save coupling pairs plot: %w", err)
		}
	}

	fmt.Printf("Saved top shotness coupling pairs plots to %s and %s\n", outputs[0], outputs[1])
	printShotnessCouplingSummary(analysis)

	return nil
}

func shotnessCouplingBarData(pairs []ShotnessCouplingPair) ([]float64, []string, []string) {
	maxPairs := min(len(pairs), 20)
	values := make([]float64, maxPairs)
	rankLabels := make([]string, maxPairs)

	barLabels := make([]string, maxPairs)
	for i, pair := range pairs[:maxPairs] {
		values[i] = pair.CouplingScore

		rankLabels[i] = strconv.Itoa(i + 1)
		if i < 10 {
			barLabels[i] = compactCouplingPairLabel(
				filepath.Base(pair.Entity1)+"-"+filepath.Base(pair.Entity2), 28,
			)
		}
	}

	return values, rankLabels, barLabels
}

func printShotnessCouplingSummary(analysis ShotnessCouplingAnalysis) {
	fmt.Println("Shotness Coupling Analysis Summary:")
	fmt.Printf("  Total entities: %d\n", analysis.Statistics.TotalEntities)
	fmt.Printf("  Total coupling relationships: %d\n", len(analysis.TopCoupling))
	fmt.Printf("  Average coupling score: %.2f\n", analysis.Statistics.AverageCoupling)
	fmt.Printf("  Max coupling score: %d\n", analysis.Statistics.MaxCoupling)
}
