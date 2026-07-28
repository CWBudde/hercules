package modes

import (
	"fmt"
	"image/color"
	"path/filepath"
	"sort"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// ShotnessResult represents a processed shotness record with aggregated statistics
type ShotnessResult struct {
	Type           string
	Name           string
	File           string
	TotalHits      int32   // Total number of modifications
	AvgHitsPerTime float64 // Average modifications per time period
	TimeSpan       int32   // Number of different time periods with modifications
	FirstHit       int32   // First time period with modifications
	LastHit        int32   // Last time period with modifications
	OriginalIndex  int
}

// Shotness generates code hotspot analysis showing which structural
// units (functions, classes, etc.) have been modified most frequently.
// Provides both text-based statistics (primary) and visualization (optional).
func Shotness(reader readers.Reader, output string) error {
	return ShotnessWithOptions(reader, output, defaultOptions())
}

func ShotnessWithOptions(reader readers.Reader, output string, opts Options) error {
	// Step 1: Read shotness records
	records, err := reader.GetShotnessRecords()
	if err != nil {
		return fmt.Errorf("get shotness records: %w", err)
	}

	if len(records) == 0 {
		return fmt.Errorf("%w: Shotness", readers.ErrAnalysisMissing)
	}

	// Step 2: Process and aggregate shotness data
	results := processShotnessRecords(records)

	// Step 3: Print Python-compatible text statistics (primary output)
	printShotnessStats(results)

	// Step 4: Generate visualization (optional - only if output directory specified)
	if output != "" {
		if err := plotShotness(results, output, opts.Graphics); err != nil {
			return fmt.Errorf("plot shotness: %w", err)
		}
	}

	return nil
}

// processShotnessRecords processes raw shotness records and calculates aggregate statistics
func processShotnessRecords(records []readers.ShotnessRecord) []ShotnessResult {
	results := make([]ShotnessResult, len(records))
	for i, record := range records {
		results[i] = processShotnessRecord(record, i)
	}

	// Sort by total hits (descending) to identify the hottest spots
	sort.Slice(results, func(i, j int) bool { return shotnessResultGreater(results[i], results[j]) })

	return results
}

func processShotnessRecord(record readers.ShotnessRecord, index int) ShotnessResult {
	var totalHits int32
	firstHit, lastHit := int32(-1), int32(-1)
	for timePoint, count := range record.Counters {
		totalHits += count
		if firstHit == -1 || timePoint < firstHit {
			firstHit = timePoint
		}
		if lastHit == -1 || timePoint > lastHit {
			lastHit = timePoint
		}
	}

	timeSpan := safeInt32(len(record.Counters))
	return ShotnessResult{
		Type:           record.Type,
		Name:           record.Name,
		File:           record.File,
		TotalHits:      totalHits,
		AvgHitsPerTime: averageShotnessHits(totalHits, timeSpan),
		TimeSpan:       timeSpan,
		FirstHit:       firstHit,
		LastHit:        lastHit,
		OriginalIndex:  index,
	}
}

func averageShotnessHits(total, span int32) float64 {
	if span == 0 {
		return 0
	}
	return float64(total) / float64(span)
}

func shotnessResultGreater(left, right ShotnessResult) bool {
	if left.TotalHits != right.TotalHits {
		return left.TotalHits > right.TotalHits
	}
	if left.File != right.File {
		return left.File > right.File
	}
	if left.Name != right.Name {
		return left.Name > right.Name
	}
	if left.Type != right.Type {
		return left.Type > right.Type
	}
	return left.OriginalIndex > right.OriginalIndex
}

func safeInt32(value int) int32 {
	const maxInt32 = int(^uint32(0) >> 1)
	if value > maxInt32 {
		return int32(maxInt32)
	}
	return int32(value) // #nosec G115 - value is bounded above and non-negative len input.
}

// plotShotness creates a bar chart showing the hottest code spots by modification frequency
func plotShotness(
	results []ShotnessResult,
	output string,
	optionValues ...graphics.Options,
) error {
	visuals := graphics.DefaultOptions()
	if len(optionValues) > 0 {
		visuals = optionValues[0]
	}
	// Limit to top 20 hottest spots for better visualization
	maxItems := 20
	if len(results) > maxItems {
		results = results[:maxItems]
	}

	labels := make([]string, len(results))
	values := make([]float64, len(results))
	maxValue := 0.0
	for i, result := range results {
		labels[i] = compactPlotLabel(fmt.Sprintf("%s:%s", result.Type, result.Name), 24)
		values[i] = float64(result.TotalHits)
		if values[i] > maxValue {
			maxValue = values[i]
		}
	}

	width, height := graphics.GetPlotSizeInchesWithOptions(graphics.ChartTypeWide, visuals)
	outputFile := filepath.Join(output, "shotness.png")
	if err := graphics.PlotBarChartMatplotlib(labels, values, graphics.MatplotlibBarOptions{
		Title:        "Code Hotspots (Most Frequently Modified Structural Units)",
		XLabel:       "Structural Units",
		YLabel:       "Total Modifications",
		Output:       outputFile,
		WidthInches:  width,
		HeightInches: height,
		Color:        color.RGBA{R: 228, G: 87, B: 86, A: 255},
		RotateX:      true,
		DisableGrid:  true,
		// Matplotlib's bar autoscale leaves asymmetric room for edge bars and labels.
		ManualXLim: true,
		XMin:       -0.53,
		XMax:       float64(len(results)) + 0.60,
		YMax:       maxValue * 1.05,
		FontSize:   visuals.PlotFontSize(),
	}); err != nil {
		return fmt.Errorf("failed to save shotness plot: %w", err)
	}

	fmt.Printf("Shotness chart saved to %s\n", outputFile)

	// Print text summary
	printShotnessSummary(results)

	return nil
}

func compactPlotLabel(label string, limit int) string {
	if len(label) <= limit {
		return label
	}
	if limit <= 3 {
		return label[:limit]
	}
	return "..." + label[len(label)-limit+3:]
}

// printShotnessStats prints shotness statistics in Python-compatible format
// Matches the format from Python's show_shotness_stats function:
// "%8d  %s:%s [%s]" % (count, r.file, r.name, r.internal_role)
func printShotnessStats(results []ShotnessResult) {
	fmt.Println("Shotness Analysis - Code Hotspots:")

	if len(results) == 0 {
		fmt.Println("No hotspots found.")
		return
	}

	// Print in Python-compatible format: count  file:name [type]
	for _, result := range results {
		fmt.Printf("%8d  %s:%s [%s]\n",
			result.TotalHits,
			result.File,
			result.Name,
			result.Type)
	}

	fmt.Printf("\nTotal: %d hotspots analyzed\n", len(results))
}

// printShotnessSummary prints a detailed text summary of the shotness analysis
func printShotnessSummary(results []ShotnessResult) {
	fmt.Println("\nCode Hotspot Analysis (Shotness):")
	fmt.Println("==================================")

	if len(results) == 0 {
		fmt.Println("No hotspots found.")
		return
	}

	totalModifications, typeCount := summarizeShotnessResults(results)
	fmt.Printf("Total structural units analyzed: %d\n", len(results))
	fmt.Printf("Total modifications tracked: %d\n", totalModifications)
	fmt.Println("\nStructural unit types:")
	for unitType, count := range typeCount {
		fmt.Printf("  %-12s: %d units\n", unitType, count)
	}

	fmt.Println("\nTop Hotspots:")
	fmt.Println("Rank | Type       | Name                    | File                     | Hits | Avg/Time | Span")
	fmt.Println("-----|------------|-------------------------|--------------------------|------|----------|-----")

	maxDisplay := min(15, len(results))
	printShotnessRows(results[:maxDisplay])
	if len(results) > maxDisplay {
		fmt.Printf("\n... and %d more hotspots\n", len(results)-maxDisplay)
	}

	hottest := results[0]
	fmt.Printf("\nHottest spot: %s '%s' in %s (%d modifications)\n",
		hottest.Type, hottest.Name, filepath.Base(hottest.File), hottest.TotalHits)
	fmt.Printf("Average modifications per unit: %.1f\n",
		float64(totalModifications)/float64(len(results)))
}

func summarizeShotnessResults(results []ShotnessResult) (int32, map[string]int) {
	total := int32(0)
	types := make(map[string]int)
	for _, result := range results {
		total += result.TotalHits
		types[result.Type]++
	}
	return total, types
}

func printShotnessRows(results []ShotnessResult) {
	for i, result := range results {
		fmt.Printf(
			"%4d | %-10s | %-23s | %-24s | %4d | %8.1f | %4d\n",
			i+1,
			result.Type,
			compactEndEllipsis(result.Name, 23),
			compactEndEllipsis(filepath.Base(result.File), 24),
			result.TotalHits,
			result.AvgHitsPerTime,
			result.TimeSpan,
		)
	}
}

func compactEndEllipsis(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit-3] + "..."
}
