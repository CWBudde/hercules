package modes

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"sort"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

type ParallelismMetrics struct {
	TotalPeriods       int
	ParallelPeriods    int
	ParallelismIndex   float64
	PeakConcurrency    int
	AverageConcurrency float64
	DeveloperOverlaps  map[string]map[string]float64
	PeriodConcurrency  []int
	ActiveDevelopers   []string
}

type ParallelDeveloperData struct {
	Name                string
	CommitsRank         int
	Commits             int
	LinesRank           int
	Lines               int
	OwnershipRank       int
	Ownership           int
	CouplesIndex        int
	CouplesCluster      int
	CommitCooccIndex    int
	CommitCooccCluster  int
	AverageOverlapScore float64
}

// DevsParallel analyzes parallel development patterns. The concurrency timeline
// is a Go-only chart (Python labours needs a shared couples_people_data.tsv and
// emits nothing standalone), so it is gated behind detail (--devs-parallel-detail).
func DevsParallel(reader readers.Reader, output string, maxPeople int, allowSyntheticFallback, detail bool) error {
	opts := defaultOptions()
	opts.MaxPeople = maxPeople
	opts.DevsParallelFallback = allowSyntheticFallback
	opts.DevsParallelDetail = detail

	return DevsParallelWithOptions(reader, output, opts)
}

func DevsParallelWithOptions(reader readers.Reader, output string, opts Options) error {
	fmt.Println("Analyzing parallel development patterns...")

	parallelData, timeSeries, err := loadDevsParallelData(reader, opts.MaxPeople)
	if err != nil {
		if opts.DevsParallelFallback && errors.Is(err, readers.ErrAnalysisMissing) {
			fmt.Fprintf(os.Stderr, "Warning: could not load devs-parallel data: %v\n", err)
			return generateSyntheticParallelAnalysis(reader, output, opts.DevsParallelDetail, opts.Graphics)
		}

		return err
	}

	if len(parallelData) == 0 {
		return fmt.Errorf("%w: devs-parallel", readers.ErrAnalysisMissing)
	}

	// Primary output: the Python-parity parallel-coordinates "Developers" chart.
	if err := plotDevsParallelCoordinates(parallelData, output, opts.Graphics); err != nil {
		return fmt.Errorf("failed to create parallel coordinates plot: %w", err)
	}

	metrics := calculateParallelismMetricsFromParallelData(parallelData, timeSeries)

	// The concurrency timeline is a Go-only auxiliary chart, emitted as a
	// sibling only when detail is requested.
	if opts.DevsParallelDetail {
		timelineOutput := siblingOutputPath(output, "devs-parallel.png", "concurrency_timeline")

		err := plotParallelActivity(metrics, timelineOutput, opts.Graphics)
		if err != nil {
			return fmt.Errorf("failed to create parallel activity plot: %w", err)
		}
	}

	// Print summary statistics
	printParallelismSummary(metrics)

	fmt.Println("Parallel development analysis completed successfully.")

	return nil
}

// plotDevsParallelCoordinates renders the parallel-coordinates chart that Python
// labours' show_devs_parallel draws: each developer is a smooth curve flowing
// across the commits / lines / ownership / couples / commit-cooccurrence rank
// axes, normalized by the developer count.
func plotDevsParallelCoordinates(
	data []ParallelDeveloperData,
	output string,
	optionValues ...graphics.Options,
) error {
	visuals := graphics.DefaultOptions()
	if len(optionValues) > 0 {
		visuals = optionValues[0]
	}

	if output == "" {
		output = "devs-parallel.png"
	}

	n := len(data)
	if n == 0 {
		return errors.New("no developer data for parallel coordinates")
	}

	series := make([]graphics.MatplotlibParallelCoordinatesSeries, 0, n)
	for _, dev := range data {
		series = append(series, graphics.MatplotlibParallelCoordinatesSeries{
			Values: []float64{
				float64(dev.CommitsRank) / float64(n),
				float64(dev.LinesRank) / float64(n),
				float64(dev.OwnershipRank) / float64(n),
				float64(dev.CouplesIndex) / float64(n),
				float64(dev.CommitCooccIndex) / float64(n),
			},
		})
	}

	err := graphics.PlotParallelCoordinatesMatplotlib(series, graphics.MatplotlibParallelCoordinatesOptions{
		Title:        "Developers",
		Output:       output,
		WidthInches:  16,
		HeightInches: 12,
		Axes:         5,
		FontSize:     visuals.PlotFontSize(),
	})
	if err != nil {
		return err
	}

	fmt.Printf("Saved devs-parallel plot to %s\n", output)

	return nil
}

func loadDevsParallelData(reader readers.Reader, maxPeople int) ([]ParallelDeveloperData, *readers.DeveloperTimeSeriesData, error) {
	people, ownership, err := reader.GetOwnershipBurndown()
	if err != nil {
		return nil, nil, fmt.Errorf("devs-parallel ownership burndown: %w", err)
	}

	couplingPeople, couplingMatrix, err := reader.GetPeopleCooccurrence()
	if err != nil {
		return nil, nil, fmt.Errorf("devs-parallel people cooccurrence: %w", err)
	}

	timeSeries, err := reader.GetDeveloperTimeSeriesData()
	if err != nil {
		return nil, nil, fmt.Errorf("devs-parallel devs time series: %w", err)
	}

	return calculateParallelDeveloperData(people, ownership, couplingPeople, couplingMatrix, timeSeries, maxPeople), timeSeries, nil
}

func calculateParallelDeveloperData(
	people []string,
	ownership map[string][][]int,
	couplingPeople []string,
	couplingMatrix readers.SparseMatrix,
	timeSeries *readers.DeveloperTimeSeriesData,
	maxPeople int,
) []ParallelDeveloperData {
	if timeSeries == nil || len(timeSeries.People) == 0 || len(timeSeries.Days) == 0 {
		return nil
	}

	names := parallelDeveloperNames(people, timeSeries.People)
	commits, lines, activeDays := aggregateParallelDeveloperActivity(timeSeries.Days, names)

	chosen := topNamesByValue(commits, maxPeople)
	if len(chosen) == 0 {
		return nil
	}

	ownershipTotals := ownershipTotalsForNames(ownership, chosen)
	commitsRank := rankNamesByValue(commits, chosen)
	linesRank := rankNamesByValue(lines, chosen)
	ownershipRank := rankNamesByValue(ownershipTotals, chosen)
	couplesOrder := orderNamesByCoupling(chosen, couplingPeople, couplingMatrix)
	commitOrder := orderNamesByActiveDayOverlap(chosen, activeDays)
	averageOverlap := averageOverlapScores(chosen, activeDays)

	result := make([]ParallelDeveloperData, 0, len(chosen))
	for _, name := range chosen {
		result = append(result, ParallelDeveloperData{
			Name:                name,
			CommitsRank:         commitsRank[name],
			Commits:             commits[name],
			LinesRank:           linesRank[name],
			Lines:               lines[name],
			OwnershipRank:       ownershipRank[name],
			Ownership:           ownershipTotals[name],
			CouplesIndex:        couplesOrder[name],
			CouplesCluster:      0,
			CommitCooccIndex:    commitOrder[name],
			CommitCooccCluster:  0,
			AverageOverlapScore: averageOverlap[name],
		})
	}

	return result
}

func parallelDeveloperNames(ownershipPeople, timeSeriesPeople []string) []string {
	if len(ownershipPeople) > 0 {
		return ownershipPeople
	}

	return timeSeriesPeople
}

func aggregateParallelDeveloperActivity(
	days map[int]map[int]readers.DevDay,
	names []string,
) (map[string]int, map[string]int, map[string]map[int]bool) {
	commits := make(map[string]int)
	lines := make(map[string]int)

	activeDays := make(map[string]map[int]bool)
	for day, devs := range days {
		aggregateParallelDeveloperDay(day, devs, names, commits, lines, activeDays)
	}

	return commits, lines, activeDays
}

func aggregateParallelDeveloperDay(
	day int,
	devs map[int]readers.DevDay,
	names []string,
	commits, lines map[string]int,
	activeDays map[string]map[int]bool,
) {
	for devIndex, stats := range devs {
		if devIndex < 0 || devIndex >= len(names) {
			continue
		}

		name := names[devIndex]
		commits[name] += stats.Commits

		lines[name] += stats.LinesAdded + stats.LinesRemoved + stats.LinesModified
		if hasDeveloperActivity(stats) {
			if activeDays[name] == nil {
				activeDays[name] = make(map[int]bool)
			}

			activeDays[name][day] = true
		}
	}
}

func ownershipTotalsForNames(ownership map[string][][]int, names []string) map[string]int {
	totals := make(map[string]int, len(names))
	for _, name := range names {
		totals[name] = finalOwnershipTotal(ownership[name])
	}

	return totals
}

func filterPeopleBurndownByActivity(peopleBurndown []readers.PeopleBurndown, maxPeople int) []readers.PeopleBurndown {
	if maxPeople <= 0 || len(peopleBurndown) <= maxPeople {
		return peopleBurndown
	}

	filtered := append([]readers.PeopleBurndown(nil), peopleBurndown...)
	sort.SliceStable(filtered, func(i, j int) bool {
		left := peopleBurndownActivity(filtered[i])

		right := peopleBurndownActivity(filtered[j])
		if left == right {
			return filtered[i].Person < filtered[j].Person
		}

		return left > right
	})
	fmt.Fprintf(os.Stderr, "Warning: truncated people to the most active %d\n", maxPeople)

	return filtered[:maxPeople]
}

func peopleBurndownActivity(person readers.PeopleBurndown) int {
	total := 0

	for _, row := range person.Matrix {
		for _, value := range row {
			total += value
		}
	}

	return total
}

func topNamesByValue(values map[string]int, maxPeople int) []string {
	names := make([]string, 0, len(values))
	for name, value := range values {
		if value > 0 {
			names = append(names, name)
		}
	}

	sortNamesByValue(names, values)

	if maxPeople > 0 && len(names) > maxPeople {
		fmt.Fprintf(os.Stderr, "Warning: truncated people to the most active %d\n", maxPeople)
		names = names[:maxPeople]
	}

	return names
}

func rankNamesByValue(values map[string]int, chosen []string) map[string]int {
	names := append([]string(nil), chosen...)
	sortNamesByValue(names, values)

	ranks := make(map[string]int, len(names))
	for rank, name := range names {
		ranks[name] = rank
	}

	return ranks
}

func sortNamesByValue(names []string, values map[string]int) {
	sort.SliceStable(names, func(i, j int) bool {
		left := values[names[i]]

		right := values[names[j]]
		if left == right {
			return names[i] < names[j]
		}

		return left > right
	})
}

func finalOwnershipTotal(matrix [][]int) int {
	if len(matrix) == 0 {
		return 0
	}

	final := matrix[len(matrix)-1]

	total := 0
	for _, value := range final {
		total += value
	}

	return total
}

func orderNamesByCoupling(
	chosen, couplingPeople []string, couplingMatrix readers.SparseMatrix,
) map[string]int {
	indexByName := make(map[string]int, len(couplingPeople))
	for i, name := range couplingPeople {
		indexByName[name] = i
	}

	values := make(map[string]int, len(chosen))
	for _, name := range chosen {
		idx, ok := indexByName[name]
		if !ok || idx < 0 || idx >= couplingMatrix.Rows {
			continue
		}

		total := 0

		for _, other := range chosen {
			otherIdx, ok := indexByName[other]
			if ok && otherIdx >= 0 && otherIdx < couplingMatrix.Columns {
				total += couplingMatrix.At(idx, otherIdx)
			}
		}

		values[name] = total
	}

	return rankNamesByValue(values, chosen)
}

func orderNamesByActiveDayOverlap(chosen []string, activeDaysByName map[string]map[int]bool) map[string]int {
	values := make(map[string]int, len(chosen))
	for _, name := range chosen {
		total := 0

		for _, other := range chosen {
			if name == other {
				continue
			}

			total += activeDayIntersection(activeDaysByName[name], activeDaysByName[other])
		}

		values[name] = total
	}

	return rankNamesByValue(values, chosen)
}

func averageOverlapScores(chosen []string, activeDaysByName map[string]map[int]bool) map[string]float64 {
	scores := make(map[string]float64, len(chosen))
	for _, name := range chosen {
		total := 0.0
		count := 0

		for _, other := range chosen {
			if name == other {
				continue
			}

			total += activeDayJaccard(activeDaysByName[name], activeDaysByName[other])
			count++
		}

		if count > 0 {
			scores[name] = total / float64(count)
		}
	}

	return scores
}

func activeDayIntersection(left, right map[int]bool) int {
	if len(left) > len(right) {
		left, right = right, left
	}

	total := 0

	for day := range left {
		if right[day] {
			total++
		}
	}

	return total
}

func activeDayJaccard(left, right map[int]bool) float64 {
	intersection := activeDayIntersection(left, right)

	union := len(left) + len(right) - intersection
	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

func calculateParallelismMetricsFromParallelData(data []ParallelDeveloperData, timeSeries *readers.DeveloperTimeSeriesData) ParallelismMetrics {
	activeDaysByName := activeDaysForParallelDevelopers(data, timeSeries)

	allDays := sortedActiveDays(data, activeDaysByName)
	if len(allDays) == 0 {
		allDays = []int{0}
	}

	metrics := ParallelismMetrics{
		TotalPeriods:      len(allDays),
		DeveloperOverlaps: make(map[string]map[string]float64, len(data)),
		PeriodConcurrency: make([]int, len(allDays)),
		ActiveDevelopers:  make([]string, 0, len(data)),
	}

	populateConcurrencyMetrics(&metrics, data, allDays, activeDaysByName)
	populateDeveloperOverlaps(&metrics, data, activeDaysByName)

	return metrics
}

func populateConcurrencyMetrics(
	metrics *ParallelismMetrics,
	data []ParallelDeveloperData,
	allDays []int,
	activeDaysByName map[string]map[int]bool,
) {
	totalConcurrency := 0

	for i, day := range allDays {
		concurrent := 0

		for _, dev := range data {
			if activeDaysByName[dev.Name][day] {
				concurrent++
			}
		}

		metrics.PeriodConcurrency[i] = concurrent

		totalConcurrency += concurrent
		if concurrent > 1 {
			metrics.ParallelPeriods++
		}

		if concurrent > metrics.PeakConcurrency {
			metrics.PeakConcurrency = concurrent
		}
	}

	metrics.AverageConcurrency = float64(totalConcurrency) / float64(len(metrics.PeriodConcurrency))
	metrics.ParallelismIndex = float64(metrics.ParallelPeriods) / float64(len(metrics.PeriodConcurrency)) * 100
}

func populateDeveloperOverlaps(
	metrics *ParallelismMetrics,
	data []ParallelDeveloperData,
	activeDaysByName map[string]map[int]bool,
) {
	for _, dev := range data {
		metrics.ActiveDevelopers = append(metrics.ActiveDevelopers, dev.Name)

		metrics.DeveloperOverlaps[dev.Name] = make(map[string]float64, len(data))
		for _, other := range data {
			metrics.DeveloperOverlaps[dev.Name][other.Name] = developerOverlap(
				dev.Name, other.Name, activeDaysByName,
			)
		}
	}
}

func developerOverlap(left, right string, activeDaysByName map[string]map[int]bool) float64 {
	if left == right {
		return 1
	}

	return activeDayJaccard(activeDaysByName[left], activeDaysByName[right])
}

func activeDaysForParallelDevelopers(data []ParallelDeveloperData, timeSeries *readers.DeveloperTimeSeriesData) map[string]map[int]bool {
	active := make(map[string]map[int]bool, len(data))
	for _, dev := range data {
		active[dev.Name] = make(map[int]bool)
	}

	if timeSeries == nil {
		return active
	}

	for day, devs := range timeSeries.Days {
		for devIndex, stats := range devs {
			if devIndex < 0 || devIndex >= len(timeSeries.People) {
				continue
			}

			name := timeSeries.People[devIndex]

			days, ok := active[name]
			if !ok {
				continue
			}

			if hasDeveloperActivity(stats) {
				days[day] = true
			}
		}
	}

	return active
}

func hasDeveloperActivity(stats readers.DevDay) bool {
	return stats.Commits > 0 || stats.LinesAdded > 0 ||
		stats.LinesRemoved > 0 || stats.LinesModified > 0
}

func sortedActiveDays(data []ParallelDeveloperData, activeDaysByName map[string]map[int]bool) []int {
	seen := make(map[int]bool)

	for _, dev := range data {
		for day := range activeDaysByName[dev.Name] {
			seen[day] = true
		}
	}

	days := make([]int, 0, len(seen))
	for day := range seen {
		days = append(days, day)
	}

	sort.Ints(days)

	return days
}

// plotParallelActivity creates a timeline showing concurrent developer activity.
func plotParallelActivity(
	metrics ParallelismMetrics,
	output string,
	optionValues ...graphics.Options,
) error {
	visuals := graphics.DefaultOptions()
	if len(optionValues) > 0 {
		visuals = optionValues[0]
	}

	if len(metrics.PeriodConcurrency) == 0 {
		metrics.PeriodConcurrency = []int{0}
	}

	x := make([]float64, len(metrics.PeriodConcurrency))

	y := make([]float64, len(metrics.PeriodConcurrency))
	for i, concurrency := range metrics.PeriodConcurrency {
		x[i] = float64(i)
		y[i] = float64(concurrency)
	}

	series := []graphics.MatplotlibLineSeries{
		{
			Name:  "Concurrent developers",
			X:     x,
			Y:     y,
			Color: color.RGBA{R: 76, G: 120, B: 168, A: 255},
			Fill:  true,
		},
	}

	// Add horizontal line for average concurrency as a flat dashed series so it
	// appears in the legend.
	if metrics.AverageConcurrency > 0 {
		avgY := make([]float64, len(x))
		for i := range avgY {
			avgY[i] = metrics.AverageConcurrency
		}

		series = append(series, graphics.MatplotlibLineSeries{
			Name:   fmt.Sprintf("Average (%.1f)", metrics.AverageConcurrency),
			X:      x,
			Y:      avgY,
			Color:  color.RGBA{R: 245, G: 133, B: 24, A: 255},
			Dashes: []float64{5, 5},
		})
	}

	// Save to the user's requested output path (Python parity: single file).
	if output == "" {
		output = "devs-parallel.png"
	}

	err := graphics.PlotLineChartMatplotlib(series, graphics.MatplotlibLineOptions{
		Title:    "Parallel Development Activity Over Time",
		XLabel:   "Time Period",
		YLabel:   "Number of Concurrent Developers",
		Output:   output,
		ShowGrid: true,
		Legend:   true,
		FontSize: visuals.PlotFontSize(),
	})
	if err != nil {
		return fmt.Errorf("failed to save parallel activity plot: %w", err)
	}

	fmt.Printf("Saved parallel activity plot to %s\n", output)

	return nil
}

// generateSyntheticParallelAnalysis creates a fallback analysis when real data is not available.
func generateSyntheticParallelAnalysis(
	reader readers.Reader,
	output string,
	detail bool,
	optionValues ...graphics.Options,
) error {
	fmt.Println("Generating synthetic parallel development analysis...")

	// Try to get basic developer stats for fallback
	developerStats, err := reader.GetDeveloperStats()
	if err != nil {
		return fmt.Errorf("get developer stats for parallel fallback: %w", err)
	}

	if len(developerStats) == 0 {
		return fmt.Errorf("%w: developer stats", readers.ErrAnalysisMissing)
	}

	metrics := syntheticParallelismMetrics(developerStats)
	if detail {
		err := plotParallelActivity(metrics, output, optionValues...)
		if err != nil {
			return fmt.Errorf("failed to create parallel activity plot: %w", err)
		}
	}

	printParallelismSummary(metrics)

	if !detail {
		fmt.Println("Concurrency timeline chart skipped (pass --devs-parallel-detail to render it).")
	}

	fmt.Println("Synthetic parallel development analysis completed.")

	return nil
}

func syntheticParallelismMetrics(developerStats []readers.DeveloperStat) ParallelismMetrics {
	numPeriods := 52
	metrics := ParallelismMetrics{
		TotalPeriods:       numPeriods,
		ParallelPeriods:    int(float64(numPeriods) * 0.6), // Assume 60% parallel activity
		ParallelismIndex:   60.0,
		PeakConcurrency:    min(len(developerStats), 4),
		AverageConcurrency: math.Min(float64(len(developerStats))*0.7, 3.0),
		ActiveDevelopers:   make([]string, 0, len(developerStats)),
		PeriodConcurrency:  make([]int, numPeriods),
		DeveloperOverlaps:  make(map[string]map[string]float64),
	}

	for i, dev := range developerStats {
		metrics.ActiveDevelopers = append(metrics.ActiveDevelopers, dev.Name)

		metrics.DeveloperOverlaps[dev.Name] = make(map[string]float64)
		for j, otherDev := range developerStats {
			metrics.DeveloperOverlaps[dev.Name][otherDev.Name] = syntheticDeveloperOverlap(i, j, dev, otherDev)
		}
	}

	for i := range numPeriods {
		baseActivity := 1 + int(metrics.AverageConcurrency*math.Sin(float64(i)*0.3)+0.5)
		variation := int(math.Sin(float64(i)*0.1) * 2)
		concurrency := max(1, min(len(developerStats), baseActivity+variation))
		metrics.PeriodConcurrency[i] = concurrency
	}

	return metrics
}

func syntheticDeveloperOverlap(i, j int, dev, otherDev readers.DeveloperStat) float64 {
	if i == j {
		return 1
	}

	largerCommitCount := max(dev.Commits, otherDev.Commits)
	if largerCommitCount == 0 {
		return 0
	}

	ratio := float64(min(dev.Commits, otherDev.Commits)) / float64(largerCommitCount)
	overlap := ratio * (0.3 + 0.4*math.Sin(float64(i+j)*0.5))

	return math.Max(0, math.Min(1, overlap))
}

// printParallelismSummary displays key metrics about parallel development.
func printParallelismSummary(metrics ParallelismMetrics) {
	fmt.Println("\n=== Parallel Development Summary ===")
	fmt.Printf("Total Time Periods: %d\n", metrics.TotalPeriods)
	fmt.Printf("Periods with Parallel Activity: %d (%.1f%%)\n",
		metrics.ParallelPeriods, metrics.ParallelismIndex)
	fmt.Printf("Peak Concurrent Developers: %d\n", metrics.PeakConcurrency)
	fmt.Printf("Average Concurrent Developers: %.2f\n", metrics.AverageConcurrency)
	fmt.Printf("Active Developers: %d\n", len(metrics.ActiveDevelopers))

	if len(metrics.ActiveDevelopers) <= 1 {
		return
	}

	fmt.Println("\nTop Developer Collaborations:")

	overlaps := uniqueDeveloperOverlaps(metrics.DeveloperOverlaps)
	sort.Slice(overlaps, func(i, j int) bool {
		return overlaps[i].overlap > overlaps[j].overlap
	})

	for _, item := range overlaps[:min(5, len(overlaps))] {
		fmt.Printf("  %s: %.3f\n", item.pair, item.overlap)
	}
}

type developerOverlapSummary struct {
	pair    string
	overlap float64
}

func uniqueDeveloperOverlaps(overlaps map[string]map[string]float64) []developerOverlapSummary {
	result := make([]developerOverlapSummary, 0)
	processed := make(map[string]bool)

	for developer, others := range overlaps {
		for other, overlap := range others {
			pairKey, reverseKey := developer+"-"+other, other+"-"+developer
			if developer == other || processed[pairKey] || processed[reverseKey] {
				continue
			}

			result = append(result, developerOverlapSummary{
				pair:    fmt.Sprintf("%s ↔ %s", developer, other),
				overlap: overlap,
			})
			processed[pairKey] = true
			processed[reverseKey] = true
		}
	}

	return result
}
