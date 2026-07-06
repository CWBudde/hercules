package modes

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strconv"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
	"github.com/spf13/viper"
)

// CouplesFiles generates file coupling analysis and visualization
func CouplesFiles(reader readers.Reader, output string) error {
	return runCouplingMode(
		"File Coupling Analysis",
		"file coupling",
		output,
		reader.GetFileCooccurrence,
		func(names []string, matrix [][]int, output string) error {
			return plotFileCoupling(analyzeFileCoupling(names, matrix), output)
		},
	)
}

func runCouplingMode(title, label, output string, read func() ([]string, [][]int, error), plot func([]string, [][]int, string) error) error {
	quiet := viper.GetBool("quiet")
	progEstimator := progress.NewProgressEstimator(!quiet)
	progEstimator.StartMultiOperation(3, title)
	progEstimator.NextOperation("Extracting " + label + " data")
	names, matrix, err := read()
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to get %s data: %v", label, err)
	}
	if len(names) == 0 {
		progEstimator.FinishMultiOperation()
		if !quiet {
			fmt.Printf("No %s data available\n", label)
		}
		return nil
	}
	progEstimator.NextOperation("Analyzing coupling patterns")
	progEstimator.NextOperation("Generating visualization")
	if err := plot(names, matrix, output); err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to generate %s plots: %v", label, err)
	}
	progEstimator.FinishMultiOperation()
	if !quiet {
		fmt.Printf("%s completed successfully.\n", title)
	}
	return nil
}

// FileCouplingPair represents a coupling relationship between two files
type FileCouplingPair struct {
	File1            string
	File2            string
	CouplingScore    float64
	CooccuranceCount int
}

// FileCouplingAnalysis represents the complete coupling analysis results
type FileCouplingAnalysis struct {
	FileNames      []string
	CouplingMatrix [][]int
	TopCoupling    []FileCouplingPair
	Statistics     CouplingStatistics
}

// CouplingStatistics provides summary statistics about file coupling
type CouplingStatistics struct {
	TotalFiles      int
	TotalCoupling   int
	AverageCoupling float64
	MaxCoupling     int
	MinCoupling     int
}

// analyzeFileCoupling performs analysis on file coupling data
func analyzeFileCoupling(fileNames []string, couplingMatrix [][]int) FileCouplingAnalysis {
	pairs, stats := analyzeCouplingPairs(fileNames, couplingMatrix, 20)
	filePairs := make([]FileCouplingPair, len(pairs))
	for i, pair := range pairs {
		filePairs[i] = FileCouplingPair{
			File1:            pair.Name1,
			File2:            pair.Name2,
			CouplingScore:    pair.Score,
			CooccuranceCount: pair.Count,
		}
	}

	analysis := FileCouplingAnalysis{
		FileNames:      fileNames,
		CouplingMatrix: couplingMatrix,
		TopCoupling:    filePairs,
		Statistics: CouplingStatistics{
			TotalFiles:      len(fileNames),
			TotalCoupling:   stats.Total,
			AverageCoupling: stats.Average,
			MaxCoupling:     stats.Max,
			MinCoupling:     stats.Min,
		},
	}

	return analysis
}

type commonCouplingPair struct {
	Name1 string
	Name2 string
	Score float64
	Count int
}

type commonCouplingStats struct {
	Total   int
	Average float64
	Max     int
	Min     int
}

func analyzeCouplingPairs(names []string, matrix [][]int, limit int) ([]commonCouplingPair, commonCouplingStats) {
	var pairs []commonCouplingPair
	totalCoupling := 0
	maxCoupling := 0
	minCoupling := int(^uint(0) >> 1)

	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if i >= len(matrix) || j >= len(matrix[i]) {
				continue
			}
			coupling := matrix[i][j]
			totalCoupling += coupling
			if coupling > maxCoupling {
				maxCoupling = coupling
			}
			if coupling < minCoupling && coupling > 0 {
				minCoupling = coupling
			}
			if coupling > 0 {
				pairs = append(pairs, commonCouplingPair{
					Name1: names[i],
					Name2: names[j],
					Score: float64(coupling),
					Count: coupling,
				})
			}
		}
	}

	for i := 0; i < len(pairs)-1; i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[i].Score < pairs[j].Score {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}
	avgCoupling := 0.0
	if len(pairs) > 0 {
		avgCoupling = float64(totalCoupling) / float64(len(pairs))
	}
	if len(pairs) > limit {
		pairs = pairs[:limit]
	}
	return pairs, commonCouplingStats{
		Total:   totalCoupling,
		Average: avgCoupling,
		Max:     maxCoupling,
		Min:     minCoupling,
	}
}

// plotFileCoupling generates coupling visualization plots
func plotFileCoupling(analysis FileCouplingAnalysis, output string) error {
	// Create heatmap for top coupled files
	if err := plotCouplingHeatmap(analysis, output); err != nil {
		return err
	}

	// Create bar chart of top coupling pairs
	if err := plotTopCouplingPairs(analysis, output); err != nil {
		return err
	}

	return nil
}

// plotCouplingHeatmap creates a heatmap of file coupling relationships
func plotCouplingHeatmap(analysis FileCouplingAnalysis, output string) error {
	if len(analysis.CouplingMatrix) == 0 {
		return fmt.Errorf("no coupling matrix data available")
	}

	outputFile := filepath.Join(output, "file_coupling_heatmap.png")
	if err := plotPythonCouplingHeatmap("File Coupling Heatmap", outputFile, analysis.FileNames, analysis.CouplingMatrix, "Reds"); err != nil {
		return fmt.Errorf("failed to save heatmap: %v", err)
	}

	fmt.Printf("Saved file coupling heatmap to %s\n", outputFile)
	return nil
}

// plotTopCouplingPairs creates a bar chart of the most coupled file pairs
func plotTopCouplingPairs(analysis FileCouplingAnalysis, output string) error {
	if len(analysis.TopCoupling) == 0 {
		return fmt.Errorf("no coupling pairs data available")
	}

	// Prepare data for bar chart
	maxPairs := len(analysis.TopCoupling)
	if maxPairs > 15 {
		maxPairs = 15 // Show top 15 pairs
	}

	values := make([]float64, maxPairs)
	rankLabels := make([]string, maxPairs)
	barLabels := make([]string, maxPairs)
	for i := 0; i < maxPairs; i++ {
		pair := analysis.TopCoupling[i]
		values[i] = pair.CouplingScore
		rankLabels[i] = strconv.Itoa(i + 1)
		if i < 10 {
			barLabels[i] = compactCouplingPairLabel(filepath.Base(pair.File1)+"-"+filepath.Base(pair.File2), 28)
		}
	}

	outputFile := filepath.Join(output, "top_file_coupling_pairs.png")
	widthBar, heightBar := graphics.GetPlotSizeInches(graphics.ChartTypeDefault)
	if err := graphics.PlotBarChartMatplotlib(rankLabels, values, graphics.MatplotlibBarOptions{
		Title:         "Top File Coupling Pairs",
		XLabel:        "Coupling Pair Rank",
		YLabel:        "Coupling Score",
		Output:        outputFile,
		WidthInches:   widthBar,
		HeightInches:  heightBar,
		Color:         color.RGBA{R: 76, G: 120, B: 168, A: 255},
		DisableGrid:   true,
		YMax:          maxCouplingValue(values) * 1.05,
		BarLabels:     barLabels,
		BarLabelAngle: 70,
	}); err != nil {
		return fmt.Errorf("failed to save coupling pairs plot: %v", err)
	}

	fmt.Printf("Saved top coupling pairs plot to %s\n", outputFile)

	// Print summary information
	fmt.Printf("File Coupling Analysis Summary:\n")
	fmt.Printf("  Total files: %d\n", analysis.Statistics.TotalFiles)
	fmt.Printf("  Total coupling relationships: %d\n", len(analysis.TopCoupling))
	fmt.Printf("  Average coupling score: %.2f\n", analysis.Statistics.AverageCoupling)
	fmt.Printf("  Max coupling score: %d\n", analysis.Statistics.MaxCoupling)

	return nil
}

func compactCouplingPairLabel(label string, limit int) string {
	if limit <= 0 || len(label) <= limit {
		return label
	}
	if limit <= 3 {
		return label[len(label)-limit:]
	}
	return "..." + label[len(label)-(limit-3):]
}

func maxCouplingValue(values []float64) float64 {
	maxValue := 0.0
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}
