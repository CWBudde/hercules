package modes

import (
	"fmt"
	"image/color"
	"math"
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
	fmt.Println("Analyzing parallel development patterns...")

	parallelData, timeSeries, err := loadDevsParallelData(reader, maxPeople)
	if err != nil {
		if allowSyntheticFallback {
			fmt.Printf("Warning: could not load devs-parallel data: %v\n", err)
			return generateSyntheticParallelAnalysis(reader, output, detail)
		}
		return err
	}

	if len(parallelData) == 0 {
		return fmt.Errorf("%w: devs-parallel", readers.ErrAnalysisMissing)
	}

	// Primary output: the Python-parity parallel-coordinates "Developers" chart.
	if err := plotDevsParallelCoordinates(parallelData, output); err != nil {
		return fmt.Errorf("failed to create parallel coordinates plot: %v", err)
	}

	metrics := calculateParallelismMetricsFromParallelData(parallelData, timeSeries)

	// The concurrency timeline is a Go-only auxiliary chart, emitted as a
	// sibling only when detail is requested.
	if detail {
		timelineOutput := siblingOutputPath(output, "devs-parallel.png", "concurrency_timeline")
		if err := plotParallelActivity(metrics, timelineOutput); err != nil {
			return fmt.Errorf("failed to create parallel activity plot: %v", err)
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
func plotDevsParallelCoordinates(data []ParallelDeveloperData, output string) error {
	if output == "" {
		output = "devs-parallel.png"
	}
	n := len(data)
	if n == 0 {
		return fmt.Errorf("no developer data for parallel coordinates")
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

	if err := graphics.PlotParallelCoordinatesMatplotlib(series, graphics.MatplotlibParallelCoordinatesOptions{
		Title:        "Developers",
		Output:       output,
		WidthInches:  16,
		HeightInches: 12,
		Axes:         5,
	}); err != nil {
		return err
	}

	fmt.Printf("Saved devs-parallel plot to %s\n", output)
	return nil
}

func loadDevsParallelData(reader readers.Reader, maxPeople int) ([]ParallelDeveloperData, *readers.DeveloperTimeSeriesData, error) {
	people, ownership, err := reader.GetOwnershipBurndown()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: devs-parallel ownership burndown: %v", readers.ErrAnalysisMissing, err)
	}

	couplingPeople, couplingMatrix, err := reader.GetPeopleCooccurrence()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: devs-parallel people cooccurrence: %v", readers.ErrAnalysisMissing, err)
	}

	timeSeries, err := reader.GetDeveloperTimeSeriesData()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: devs-parallel devs time series: %v", readers.ErrAnalysisMissing, err)
	}

	return calculateParallelDeveloperData(people, ownership, couplingPeople, couplingMatrix, timeSeries, maxPeople), timeSeries, nil
}

func calculateParallelDeveloperData(
	people []string,
	ownership map[string][][]int,
	couplingPeople []string,
	couplingMatrix [][]int,
	timeSeries *readers.DeveloperTimeSeriesData,
	maxPeople int,
) []ParallelDeveloperData {
	if timeSeries == nil || len(timeSeries.People) == 0 || len(timeSeries.Days) == 0 {
		return nil
	}

	nameByDevIndex := timeSeries.People
	if len(people) > 0 {
		nameByDevIndex = people
	}

	commitsByName := make(map[string]int)
	linesByName := make(map[string]int)
	activeDaysByName := make(map[string]map[int]bool)
	for day, devs := range timeSeries.Days {
		for devIndex, stats := range devs {
			if devIndex < 0 || devIndex >= len(nameByDevIndex) {
				continue
			}
			name := nameByDevIndex[devIndex]
			commitsByName[name] += stats.Commits
			linesByName[name] += stats.LinesAdded + stats.LinesRemoved + stats.LinesModified
			if stats.Commits > 0 || stats.LinesAdded > 0 || stats.LinesRemoved > 0 || stats.LinesModified > 0 {
				if activeDaysByName[name] == nil {
					activeDaysByName[name] = make(map[int]bool)
				}
				activeDaysByName[name][day] = true
			}
		}
	}

	chosen := topNamesByValue(commitsByName, maxPeople)
	if len(chosen) == 0 {
		return nil
	}

	ownershipByName := make(map[string]int)
	for _, name := range chosen {
		ownershipByName[name] = finalOwnershipTotal(ownership[name])
	}
	commitsRank := rankNamesByValue(commitsByName, chosen)
	linesRank := rankNamesByValue(linesByName, chosen)
	ownershipRank := rankNamesByValue(ownershipByName, chosen)
	couplesOrder := orderNamesByCoupling(chosen, couplingPeople, couplingMatrix)
	commitOrder := orderNamesByActiveDayOverlap(chosen, activeDaysByName)
	averageOverlap := averageOverlapScores(chosen, activeDaysByName)

	result := make([]ParallelDeveloperData, 0, len(chosen))
	for _, name := range chosen {
		result = append(result, ParallelDeveloperData{
			Name:                name,
			CommitsRank:         commitsRank[name],
			Commits:             commitsByName[name],
			LinesRank:           linesRank[name],
			Lines:               linesByName[name],
			OwnershipRank:       ownershipRank[name],
			Ownership:           ownershipByName[name],
			CouplesIndex:        couplesOrder[name],
			CouplesCluster:      0,
			CommitCooccIndex:    commitOrder[name],
			CommitCooccCluster:  0,
			AverageOverlapScore: averageOverlap[name],
		})
	}
	return result
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
	fmt.Printf("Warning: truncated people to the most active %d\n", maxPeople)
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
		fmt.Printf("Warning: truncated people to the most active %d\n", maxPeople)
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

func orderNamesByCoupling(chosen, couplingPeople []string, couplingMatrix [][]int) map[string]int {
	indexByName := make(map[string]int, len(couplingPeople))
	for i, name := range couplingPeople {
		indexByName[name] = i
	}
	values := make(map[string]int, len(chosen))
	for _, name := range chosen {
		idx, ok := indexByName[name]
		if !ok || idx < 0 || idx >= len(couplingMatrix) {
			continue
		}
		total := 0
		for _, other := range chosen {
			otherIdx, ok := indexByName[other]
			if ok && otherIdx >= 0 && otherIdx < len(couplingMatrix[idx]) {
				total += couplingMatrix[idx][otherIdx]
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
	if len(metrics.PeriodConcurrency) > 0 {
		metrics.AverageConcurrency = float64(totalConcurrency) / float64(len(metrics.PeriodConcurrency))
		metrics.ParallelismIndex = float64(metrics.ParallelPeriods) / float64(len(metrics.PeriodConcurrency)) * 100
	}

	for _, dev := range data {
		metrics.ActiveDevelopers = append(metrics.ActiveDevelopers, dev.Name)
		metrics.DeveloperOverlaps[dev.Name] = make(map[string]float64, len(data))
		for _, other := range data {
			if dev.Name == other.Name {
				metrics.DeveloperOverlaps[dev.Name][other.Name] = 1
			} else {
				metrics.DeveloperOverlaps[dev.Name][other.Name] = activeDayJaccard(activeDaysByName[dev.Name], activeDaysByName[other.Name])
			}
		}
	}
	return metrics
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
			if stats.Commits > 0 || stats.LinesAdded > 0 || stats.LinesRemoved > 0 || stats.LinesModified > 0 {
				days[day] = true
			}
		}
	}
	return active
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

// calculateParallelismMetrics analyzes the temporal activity data to find parallel development patterns
func calculateParallelismMetrics(peopleBurndown []readers.PeopleBurndown) ParallelismMetrics {
	if len(peopleBurndown) == 0 {
		return ParallelismMetrics{}
	}

	// Find the maximum time period across all developers
	maxPeriods := 0
	for _, person := range peopleBurndown {
		if len(person.Matrix) > maxPeriods {
			maxPeriods = len(person.Matrix)
		}
	}

	if maxPeriods == 0 {
		return ParallelismMetrics{}
	}

	// Create a matrix of developer activity over time
	developers := make([]string, len(peopleBurndown))
	activityMatrix := make([][]bool, len(peopleBurndown))

	for i, person := range peopleBurndown {
		developers[i] = person.Person
		activityMatrix[i] = make([]bool, maxPeriods)

		// Mark periods where developer was active (had any commits/changes)
		for j, period := range person.Matrix {
			if j >= maxPeriods {
				break
			}
			// Consider active if there's any value in the period (sum > 0)
			active := false
			for _, val := range period {
				if val > 0 {
					active = true
					break
				}
			}
			activityMatrix[i][j] = active
		}
	}

	// Calculate concurrency for each time period
	periodConcurrency := make([]int, maxPeriods)
	parallelPeriods := 0
	totalConcurrency := 0
	peakConcurrency := 0

	for period := 0; period < maxPeriods; period++ {
		concurrent := 0
		for dev := 0; dev < len(developers); dev++ {
			if activityMatrix[dev][period] {
				concurrent++
			}
		}
		periodConcurrency[period] = concurrent
		totalConcurrency += concurrent

		if concurrent > 1 {
			parallelPeriods++
		}
		if concurrent > peakConcurrency {
			peakConcurrency = concurrent
		}
	}

	// Calculate developer overlap coefficients
	developerOverlaps := make(map[string]map[string]float64)
	for i, dev1 := range developers {
		developerOverlaps[dev1] = make(map[string]float64)
		for j, dev2 := range developers {
			if i == j {
				developerOverlaps[dev1][dev2] = 1.0
				continue
			}

			// Count periods where both developers were active
			overlap := 0
			dev1Active := 0
			dev2Active := 0

			for period := 0; period < maxPeriods; period++ {
				if activityMatrix[i][period] {
					dev1Active++
				}
				if activityMatrix[j][period] {
					dev2Active++
				}
				if activityMatrix[i][period] && activityMatrix[j][period] {
					overlap++
				}
			}

			// Calculate Jaccard similarity coefficient
			union := dev1Active + dev2Active - overlap
			if union > 0 {
				developerOverlaps[dev1][dev2] = float64(overlap) / float64(union)
			} else {
				developerOverlaps[dev1][dev2] = 0.0
			}
		}
	}

	parallelismIndex := 0.0
	if maxPeriods > 0 {
		parallelismIndex = float64(parallelPeriods) / float64(maxPeriods) * 100
	}

	averageConcurrency := 0.0
	if maxPeriods > 0 {
		averageConcurrency = float64(totalConcurrency) / float64(maxPeriods)
	}

	return ParallelismMetrics{
		TotalPeriods:       maxPeriods,
		ParallelPeriods:    parallelPeriods,
		ParallelismIndex:   parallelismIndex,
		PeakConcurrency:    peakConcurrency,
		AverageConcurrency: averageConcurrency,
		DeveloperOverlaps:  developerOverlaps,
		PeriodConcurrency:  periodConcurrency,
		ActiveDevelopers:   developers,
	}
}

// plotParallelActivity creates a timeline showing concurrent developer activity
func plotParallelActivity(metrics ParallelismMetrics, output string) error {
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
	if err := graphics.PlotLineChartMatplotlib(series, graphics.MatplotlibLineOptions{
		Title:    "Parallel Development Activity Over Time",
		XLabel:   "Time Period",
		YLabel:   "Number of Concurrent Developers",
		Output:   output,
		ShowGrid: true,
		Legend:   true,
	}); err != nil {
		return fmt.Errorf("failed to save parallel activity plot: %v", err)
	}

	fmt.Printf("Saved parallel activity plot to %s\n", output)
	return nil
}

// generateSyntheticParallelAnalysis creates a fallback analysis when real data is not available
func generateSyntheticParallelAnalysis(reader readers.Reader, output string, detail bool) error {
	fmt.Println("Generating synthetic parallel development analysis...")

	// Try to get basic developer stats for fallback
	developerStats, err := reader.GetDeveloperStats()
	if err != nil {
		fmt.Printf("Warning: could not get developer stats: %v\n", err)
		return fmt.Errorf("no data available for parallel analysis")
	}

	if len(developerStats) == 0 {
		return fmt.Errorf("no developer data available")
	}

	// Create synthetic parallel activity data
	numPeriods := 52 // 52 weeks
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

	// Generate synthetic data based on developer stats
	for i, dev := range developerStats {
		metrics.ActiveDevelopers = append(metrics.ActiveDevelopers, dev.Name)

		// Initialize overlaps
		metrics.DeveloperOverlaps[dev.Name] = make(map[string]float64)
		for j, otherDev := range developerStats {
			if i == j {
				metrics.DeveloperOverlaps[dev.Name][otherDev.Name] = 1.0
			} else {
				// Synthetic overlap based on relative activity
				ratio := 0.0
				if max(dev.Commits, otherDev.Commits) > 0 {
					ratio = float64(min(dev.Commits, otherDev.Commits)) / float64(max(dev.Commits, otherDev.Commits))
				}
				overlap := ratio * (0.3 + 0.4*math.Sin(float64(i+j)*0.5)) // Add some variation
				metrics.DeveloperOverlaps[dev.Name][otherDev.Name] = math.Max(0, math.Min(1, overlap))
			}
		}
	}

	// Generate synthetic period concurrency
	for i := 0; i < numPeriods; i++ {
		// Simulate realistic parallel activity patterns
		baseActivity := 1 + int(metrics.AverageConcurrency*math.Sin(float64(i)*0.3)+0.5)
		variation := int(math.Sin(float64(i)*0.1) * 2)
		concurrency := max(1, min(len(developerStats), baseActivity+variation))
		metrics.PeriodConcurrency[i] = concurrency
	}

	if detail {
		if err := plotParallelActivity(metrics, output); err != nil {
			return fmt.Errorf("failed to create parallel activity plot: %v", err)
		}
	}

	printParallelismSummary(metrics)
	if !detail {
		fmt.Println("Concurrency timeline chart skipped (pass --devs-parallel-detail to render it).")
	}

	fmt.Println("Synthetic parallel development analysis completed.")
	return nil
}

// printParallelismSummary displays key metrics about parallel development
func printParallelismSummary(metrics ParallelismMetrics) {
	fmt.Println("\n=== Parallel Development Summary ===")
	fmt.Printf("Total Time Periods: %d\n", metrics.TotalPeriods)
	fmt.Printf("Periods with Parallel Activity: %d (%.1f%%)\n",
		metrics.ParallelPeriods, metrics.ParallelismIndex)
	fmt.Printf("Peak Concurrent Developers: %d\n", metrics.PeakConcurrency)
	fmt.Printf("Average Concurrent Developers: %.2f\n", metrics.AverageConcurrency)
	fmt.Printf("Active Developers: %d\n", len(metrics.ActiveDevelopers))

	if len(metrics.ActiveDevelopers) > 1 {
		fmt.Println("\nTop Developer Collaborations:")

		type overlap struct {
			pair    string
			overlap float64
		}

		var overlaps []overlap
		processed := make(map[string]bool)

		for dev1, others := range metrics.DeveloperOverlaps {
			for dev2, ovr := range others {
				if dev1 != dev2 {
					pairKey := dev1 + "-" + dev2
					reversePairKey := dev2 + "-" + dev1

					if !processed[pairKey] && !processed[reversePairKey] {
						overlaps = append(overlaps, overlap{
							pair:    fmt.Sprintf("%s ↔ %s", dev1, dev2),
							overlap: ovr,
						})
						processed[pairKey] = true
						processed[reversePairKey] = true
					}
				}
			}
		}

		sort.Slice(overlaps, func(i, j int) bool {
			return overlaps[i].overlap > overlaps[j].overlap
		})

		maxDisplay := min(5, len(overlaps))
		for i := 0; i < maxDisplay; i++ {
			fmt.Printf("  %s: %.3f\n", overlaps[i].pair, overlaps[i].overlap)
		}
	}
}

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
