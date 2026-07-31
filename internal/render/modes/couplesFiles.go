package modes

import (
	"container/heap"
	"errors"
	"fmt"
	"image/color"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// CouplesFiles generates file coupling analysis and visualization.
func CouplesFiles(reader readers.Reader, output string) error {
	return CouplesFilesWithOptions(reader, output, defaultOptions())
}

func CouplesFilesWithOptions(reader readers.Reader, output string, opts Options) error {
	return runCouplingMode(
		"File Coupling Analysis",
		"file coupling",
		output,
		reader.GetFileCooccurrence,
		func(names []string, matrix readers.SparseMatrix, output string) error {
			return plotFileCouplingWithOptions(analyzeFileCoupling(names, matrix), output, opts.Graphics)
		},
		opts.Quiet,
	)
}

func runCouplingMode(
	title, label, output string,
	read func() ([]string, readers.SparseMatrix, error),
	plot func([]string, readers.SparseMatrix, string) error,
	quietValues ...bool,
) error {
	quiet := false
	if len(quietValues) > 0 {
		quiet = quietValues[0]
	}

	progEstimator := progress.NewProgressEstimator(!quiet)
	progEstimator.StartMultiOperation(3, title)
	progEstimator.NextOperation("Extracting " + label + " data")

	names, matrix, err := read()
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to get %s data: %w", label, err)
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
		return fmt.Errorf("failed to generate %s plots: %w", label, err)
	}

	progEstimator.FinishMultiOperation()

	if !quiet {
		fmt.Printf("%s completed successfully.\n", title)
	}

	return nil
}

// FileCouplingPair represents a coupling relationship between two files.
type FileCouplingPair struct {
	File1            string
	File2            string
	CouplingScore    float64
	CooccuranceCount int
}

// FileCouplingAnalysis represents the complete coupling analysis results.
type FileCouplingAnalysis struct {
	FileNames      []string
	CouplingMatrix readers.SparseMatrix
	TopCoupling    []FileCouplingPair
	Statistics     CouplingStatistics
}

// CouplingStatistics provides summary statistics about file coupling.
type CouplingStatistics struct {
	TotalFiles      int
	TotalCoupling   int
	AverageCoupling float64
	MaxCoupling     int
	MinCoupling     int
}

// analyzeFileCoupling performs analysis on file coupling data.
func analyzeFileCoupling(
	fileNames []string, couplingMatrix readers.SparseMatrix,
) FileCouplingAnalysis {
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

func analyzeCouplingPairs(
	names []string, matrix readers.SparseMatrix, limit int,
) ([]commonCouplingPair, commonCouplingStats) {
	analysis := couplingPairAnalysis{
		names: names,
		limit: limit,
		pairs: &couplingPairHeap{},
	}
	if limit > 0 {
		heap.Init(analysis.pairs)
	}

	matrix.ForEachNonZero(func(row, column, coupling int) bool {
		analysis.observe(row, column, coupling)
		return true
	})

	return analysis.result()
}

type couplingPairAnalysis struct {
	names         []string
	limit         int
	pairs         *couplingPairHeap
	total         int
	max           int
	min           int
	positivePairs int
}

func (analysis *couplingPairAnalysis) observe(row, column, coupling int) {
	if row >= len(analysis.names) || column >= len(analysis.names) || row >= column || coupling <= 0 {
		return
	}

	analysis.total += coupling
	analysis.positivePairs++

	analysis.max = max(analysis.max, coupling)
	if analysis.min == 0 || coupling < analysis.min {
		analysis.min = coupling
	}

	if analysis.limit <= 0 {
		return
	}

	analysis.retain(rankedCouplingPair{
		commonCouplingPair: commonCouplingPair{
			Name1: analysis.names[row],
			Name2: analysis.names[column],
			Score: float64(coupling),
			Count: coupling,
		},
		row: row, column: column,
	})
}

func (analysis *couplingPairAnalysis) retain(pair rankedCouplingPair) {
	if analysis.pairs.Len() < analysis.limit {
		heap.Push(analysis.pairs, pair)
		return
	}

	if betterCouplingPair(pair, (*analysis.pairs)[0]) {
		heap.Pop(analysis.pairs)
		heap.Push(analysis.pairs, pair)
	}
}

func (analysis *couplingPairAnalysis) result() ([]commonCouplingPair, commonCouplingStats) {
	average := 0.0
	if analysis.positivePairs > 0 {
		average = float64(analysis.total) / float64(analysis.positivePairs)
	}

	ranked := make([]commonCouplingPair, analysis.pairs.Len())
	// The heap keeps the worst retained pair at its root, so draining it back to
	// front yields the ranked order.
	for index := range slices.Backward(ranked) {
		ranked[index] = heap.Pop(analysis.pairs).(rankedCouplingPair).commonCouplingPair
	}

	return ranked, commonCouplingStats{
		Total:   analysis.total,
		Average: average,
		Max:     analysis.max,
		Min:     analysis.min,
	}
}

type rankedCouplingPair struct {
	commonCouplingPair

	row    int
	column int
}

// couplingPairHeap keeps the worst retained pair at its root.
type couplingPairHeap []rankedCouplingPair

func (pairs couplingPairHeap) Len() int { return len(pairs) }
func (pairs couplingPairHeap) Less(i, j int) bool {
	return betterCouplingPair(pairs[j], pairs[i])
}
func (pairs couplingPairHeap) Swap(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] }
func (pairs *couplingPairHeap) Push(value any) {
	*pairs = append(*pairs, value.(rankedCouplingPair))
}

func (pairs *couplingPairHeap) Pop() any {
	old := *pairs
	last := len(old) - 1
	value := old[last]
	*pairs = old[:last]

	return value
}

func betterCouplingPair(left, right rankedCouplingPair) bool {
	if left.Count != right.Count {
		return left.Count > right.Count
	}

	if left.row != right.row {
		return left.row < right.row
	}

	return left.column < right.column
}

// plotFileCoupling generates coupling visualization plots.
func plotFileCoupling(analysis FileCouplingAnalysis, output string) error {
	return plotFileCouplingWithOptions(analysis, output, graphics.DefaultOptions())
}

func plotFileCouplingWithOptions(analysis FileCouplingAnalysis, output string, opts graphics.Options) error {
	// Create heatmap for top coupled files
	err := plotCouplingHeatmap(analysis, output, opts)
	if err != nil {
		return err
	}

	// Create bar chart of top coupling pairs
	err = plotTopCouplingPairsWithOptions(analysis, output, opts)
	if err != nil {
		return err
	}

	return nil
}

// plotCouplingHeatmap creates a heatmap of file coupling relationships.
func plotCouplingHeatmap(
	analysis FileCouplingAnalysis,
	output string,
	optionValues ...graphics.Options,
) error {
	if analysis.CouplingMatrix.Rows == 0 {
		return errors.New("no coupling matrix data available")
	}

	outputFile := filepath.Join(output, "file_coupling_heatmap.png")

	err := plotPythonCouplingHeatmap(
		"File Coupling Heatmap",
		outputFile,
		analysis.FileNames,
		analysis.CouplingMatrix,
		"Reds",
		optionValues...,
	)
	if err != nil {
		return fmt.Errorf("failed to save heatmap: %w", err)
	}

	fmt.Printf("Saved file coupling heatmap to %s\n", outputFile)

	return nil
}

// plotTopCouplingPairs creates a bar chart of the most coupled file pairs.
func plotTopCouplingPairs(analysis FileCouplingAnalysis, output string) error {
	return plotTopCouplingPairsWithOptions(analysis, output, graphics.DefaultOptions())
}

func plotTopCouplingPairsWithOptions(analysis FileCouplingAnalysis, output string, opts graphics.Options) error {
	if len(analysis.TopCoupling) == 0 {
		return errors.New("no coupling pairs data available")
	}

	// Prepare data for bar chart
	maxPairs := len(analysis.TopCoupling)
	if maxPairs > 15 {
		maxPairs = 15 // Show top 15 pairs
	}

	values := make([]float64, maxPairs)
	rankLabels := make([]string, maxPairs)

	barLabels := make([]string, maxPairs)
	for i := range maxPairs {
		pair := analysis.TopCoupling[i]
		values[i] = pair.CouplingScore

		rankLabels[i] = strconv.Itoa(i + 1)
		if i < 10 {
			barLabels[i] = compactCouplingPairLabel(filepath.Base(pair.File1)+"-"+filepath.Base(pair.File2), 28)
		}
	}

	outputFile := filepath.Join(output, "top_file_coupling_pairs.png")

	widthBar, heightBar := graphics.GetPlotSizeInchesWithOptions(graphics.ChartTypeDefault, opts)

	err := graphics.PlotBarChartMatplotlib(rankLabels, values, graphics.MatplotlibBarOptions{
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
		FontSize:      opts.PlotFontSize(),
	})
	if err != nil {
		return fmt.Errorf("failed to save coupling pairs plot: %w", err)
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
