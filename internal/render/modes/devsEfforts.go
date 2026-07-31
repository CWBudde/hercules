package modes

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"sort"
	"time"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// DevsEfforts generates the "Efforts through time" chart: a stackplot of
// cumulative changed lines of code per developer over time. When per-day time
// series are unavailable it falls back to a commits-vs-lines scatter. With
// detail set it also renders the Go-only productivity ranking chart as a
// sibling of the requested output path.
func DevsEfforts(reader readers.Reader, output string, maxPeople int, detail bool) error {
	opts := defaultOptions()
	opts.MaxPeople, opts.DevsEffortsDetail = maxPeople, detail

	return DevsEffortsWithOptions(reader, output, opts)
}

func DevsEffortsWithOptions(reader readers.Reader, output string, opts Options) error {
	quiet := opts.Quiet
	maxPeople, detail := opts.MaxPeople, opts.DevsEffortsDetail
	progEstimator := progress.NewProgressEstimator(!quiet)

	totalPhases := 4 // data extraction, time series, analysis, plotting

	progEstimator.StartMultiOperation(totalPhases, "Developer Efforts Analysis")
	defer progEstimator.FinishMultiOperation()

	// Phase 1: Extract developer statistics (drives the optional ranking chart
	// and the scatter fallback).
	progEstimator.NextOperation("Extracting developer statistics")

	developerStats, err := reader.GetDeveloperStats()
	if err != nil {
		return fmt.Errorf("failed to get developer stats: %w", err)
	}

	// Phase 2: Build the per-day efforts time series when available.
	progEstimator.NextOperation("Building effort time series")
	progEstimator.NextOperation("Analyzing developer efforts")

	effortsTimeSeries := loadDevEffortsTimeSeries(reader, maxPeople, quiet)

	// Phase 4: Generate the primary plot.
	progEstimator.NextOperation("Generating visualization")

	err = renderDevEffortsPrimary(effortsTimeSeries, developerStats, output, maxPeople, quiet, opts.Graphics)
	if err != nil {
		return err
	}

	if detail {
		rankingOutput := siblingOutputPath(output, "devs-efforts.png", "productivity_ranking")

		err := plotProductivityRanking(effortMetricsForRanking(developerStats, maxPeople), rankingOutput, opts.Graphics)
		if err != nil {
			return fmt.Errorf("failed to generate developer productivity ranking: %w", err)
		}
	}

	if !quiet {
		fmt.Println("Developer efforts analysis completed successfully.")
	}

	return nil
}

func loadDevEffortsTimeSeries(reader readers.Reader, maxPeople int, quiet bool) *devEffortsMatrix {
	timeSeries, err := reader.GetDeveloperTimeSeriesData()

	startUnix, endUnix := reader.GetHeader()
	if err != nil || timeSeries == nil || len(timeSeries.Days) == 0 || endUnix <= startUnix {
		return nil
	}

	built, err := buildDevEffortsMatrix(timeSeries, startUnix, endUnix, maxPeople, quiet)
	if err != nil {
		if !quiet {
			fmt.Printf("Falling back to commits-vs-lines scatter: %v\n", err)
		}

		return nil
	}

	return &built
}

func renderDevEffortsPrimary(
	timeSeries *devEffortsMatrix,
	stats []readers.DeveloperStat,
	output string,
	maxPeople int,
	quiet bool,
	visuals graphics.Options,
) error {
	if timeSeries != nil {
		err := plotDevEffortsTimeSeries(*timeSeries, output, visuals)
		if err != nil {
			return fmt.Errorf("failed to generate developer efforts plot: %w", err)
		}

		return nil
	}

	if maxPeople > 0 && len(stats) > maxPeople && !quiet {
		fmt.Printf("Picking top %d developers by commit count.\n", maxPeople)
	}

	err := plotDevEfforts(effortMetricsForRanking(stats, maxPeople), output, visuals)
	if err != nil {
		return fmt.Errorf("failed to generate developer efforts plots: %w", err)
	}

	return nil
}

// effortMetricsForRanking selects the top developers (by the combined
// productivity score) and returns their effort metrics.
func effortMetricsForRanking(stats []readers.DeveloperStat, maxPeople int) []EffortMetric {
	if maxPeople > 0 && len(stats) > maxPeople {
		stats = selectTopDevelopers(stats, maxPeople)
	}

	return analyzeDevEfforts(stats)
}

// EffortMetric represents effort analysis for a developer.
type EffortMetric struct {
	Name             string
	Commits          int
	LinesAdded       int
	LinesRemoved     int
	LinesModified    int
	FilesTouched     int
	ProductivityRank int
}

// analyzeDevEfforts performs effort analysis on developer statistics.
func analyzeDevEfforts(stats []readers.DeveloperStat) []EffortMetric {
	metrics := make([]EffortMetric, 0, len(stats))

	// Calculate metrics for each developer
	for _, stat := range stats {
		metric := EffortMetric{
			Name:          stat.Name,
			Commits:       stat.Commits,
			LinesAdded:    stat.LinesAdded,
			LinesRemoved:  stat.LinesRemoved,
			LinesModified: stat.LinesModified,
			FilesTouched:  stat.FilesTouched,
		}
		metrics = append(metrics, metric)
	}

	// Sort by combined productivity score (commits + lines changed)
	sort.Slice(metrics, func(i, j int) bool {
		scoreI := float64(metrics[i].Commits) + float64(metrics[i].LinesAdded+metrics[i].LinesRemoved+metrics[i].LinesModified)*0.01
		scoreJ := float64(metrics[j].Commits) + float64(metrics[j].LinesAdded+metrics[j].LinesRemoved+metrics[j].LinesModified)*0.01

		return scoreI > scoreJ
	})

	// Assign productivity ranks
	for i := range metrics {
		metrics[i].ProductivityRank = i + 1
	}

	return metrics
}

// plotDevEfforts renders the commits-vs-lines scatter used as a fallback when
// per-day developer time series are not present in the input (the Python-parity
// "Efforts through time" chart requires per-day data and a valid header range).
func plotDevEfforts(metrics []EffortMetric, output string, visuals graphics.Options) error {
	if output == "" {
		output = "devs-efforts.png"
	}

	return plotCommitsVsLines(metrics, output, visuals)
}

// plotCommitsVsLines creates scatter plot of commits vs total lines changed.
func plotCommitsVsLines(metrics []EffortMetric, output string, visuals graphics.Options) error {
	points := make([]graphics.MatplotlibScatterPoint, len(metrics))
	for i, metric := range metrics {
		point := graphics.MatplotlibScatterPoint{
			X: float64(metric.Commits),
			Y: float64(metric.LinesAdded + metric.LinesRemoved + metric.LinesModified),
		}
		if i < 10 { // Only label top 10 to avoid clutter
			point.Label = peopleChartLabel(metric.Name)
		}

		points[i] = point
	}

	palette := visuals.Palette()

	series := []graphics.MatplotlibScatterSeries{
		{Name: "Developers", Points: points, Color: palette[0], Size: 32},
	}

	err := graphics.PlotScatterMatplotlib(series, graphics.MatplotlibScatterOptions{
		Title:          "Developer Efforts: Commits vs Lines Changed",
		XLabel:         "Total Commits",
		YLabel:         "Total Lines Changed",
		Output:         output,
		WidthInches:    16,
		HeightInches:   8,
		ShowGrid:       true,
		AnnotateLabels: true,
		FontSize:       visuals.PlotFontSize(),
	})
	if err != nil {
		return fmt.Errorf("failed to save devs-efforts plot %s: %w", output, err)
	}

	fmt.Printf("Saved devs-efforts plot to %s\n", output)

	return nil
}

// plotProductivityRanking creates a bar chart of developer productivity ranking
// at the requested sibling output path (Go-only --devs-efforts-detail chart).
func plotProductivityRanking(metrics []EffortMetric, output string, visuals graphics.Options) error {
	if output == "" {
		output = "devs-efforts_productivity_ranking.png"
	}

	// Prepare data for top developers only
	maxDev := min(len(metrics),
		// Show top 20 developers
		20)

	labels := make([]string, maxDev)

	values := make([]float64, maxDev)
	for i := range maxDev {
		metric := metrics[i]
		// Ranks 1..20 told the reader nothing the bar heights did not already
		// say. The names are what makes the chart answerable.
		labels[i] = peopleChartLabel(metric.Name)
		values[i] = float64(metric.Commits) + float64(metric.LinesAdded+metric.LinesRemoved+metric.LinesModified)*0.01
	}

	barColor := color.RGBA{R: 245, G: 133, B: 24, A: 255}

	err := graphics.PlotBarChartMatplotlib(labels, values, graphics.MatplotlibBarOptions{
		Title:        "Developer Productivity Ranking",
		XLabel:       "Developer",
		YLabel:       "Productivity Score (Commits + Lines/100)",
		Output:       output,
		WidthInches:  15.36,
		HeightInches: 7.68,
		Color:        barColor,
		DisableGrid:  true,
		Opaque:       true,
		// Names need the 45-degree tick style, and DefaultStyle would throw the
		// whole option set away - including FontSize below, which was silently
		// ignored for as long as it has been set here.
		RotateX:    true,
		ManualXLim: true,
		XMin:       -0.64,
		XMax:       float64(maxDev) - 0.36,
		FontSize:   visuals.PlotFontSize(),
	})
	if err != nil {
		return fmt.Errorf("failed to save productivity ranking plot: %w", err)
	}

	fmt.Printf("Saved developer productivity ranking plot to %s\n", output)

	return nil
}

// devEffortsMatrix holds the smoothed cumulative effort layers used by the
// "Efforts through time" stackplot.
type devEffortsMatrix struct {
	Dates     []time.Time
	Names     []string    // chosen developers in effort order, then "others"
	CumLayers [][]float64 // smoothed cumulative efforts (stacked upward)
}

type rankedDevEffort struct {
	effort int
	dev    int
}

// buildDevEffortsMatrix builds per-day changed-lines per developer, selects the
// top contributors by total effort with an aggregated "others" row, computes
// cumulative sums, and applies Slepian/DPSS smoothing.
func buildDevEffortsMatrix(ts *readers.DeveloperTimeSeriesData, startUnix, endUnix int64, maxPeople int, quiet bool) (devEffortsMatrix, error) {
	start := dateOnly(time.Unix(startUnix, 0))

	numDays := calendarDayCount(start, dateOnly(time.Unix(endUnix, 0)))
	if numDays < 2 {
		return devEffortsMatrix{}, errors.New("not enough days for an effort time series")
	}

	ranked := rankDeveloperEfforts(ts.Days)
	if len(ranked) == 0 {
		return devEffortsMatrix{}, errors.New("no developer effort data")
	}

	chosen := chooseDeveloperEfforts(ranked, maxPeople, quiet)
	efforts := collectDailyDeveloperEfforts(ts.Days, chosen, numDays)
	cumulative := cumulativeEfforts(efforts)
	smoothRowsPreserveTail(cumulative, normalizedSlepianWindow(10, 0.5))

	return devEffortsMatrix{
		Dates:     consecutiveDates(start, numDays),
		Names:     developerEffortNames(chosen, ts.People),
		CumLayers: cumulative,
	}, nil
}

func rankDeveloperEfforts(days map[int]map[int]readers.DevDay) []rankedDevEffort {
	effortsByDev := make(map[int]int)

	for _, devs := range days {
		for dev, stats := range devs {
			effortsByDev[dev] += nonNegativeDevEffort(stats)
		}
	}

	ranked := make([]rankedDevEffort, 0, len(effortsByDev))
	for dev, effort := range effortsByDev {
		ranked = append(ranked, rankedDevEffort{effort: effort, dev: dev})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].effort == ranked[j].effort {
			return ranked[i].dev < ranked[j].dev
		}

		return ranked[i].effort > ranked[j].effort
	})

	return ranked
}

func chooseDeveloperEfforts(ranked []rankedDevEffort, maxPeople int, quiet bool) []rankedDevEffort {
	chosenCount := len(ranked)
	if maxPeople > 0 && chosenCount > maxPeople {
		chosenCount = maxPeople
		if !quiet {
			fmt.Fprintf(os.Stderr, "Warning: truncated people to the most active %d\n", maxPeople)
		}
	}

	return ranked[:chosenCount]
}

func collectDailyDeveloperEfforts(
	days map[int]map[int]readers.DevDay,
	chosen []rankedDevEffort,
	numDays int,
) [][]float64 {
	chosenCount := len(chosen)

	chosenOrder := make(map[int]int, chosenCount)
	for i, item := range chosen {
		chosenOrder[item.dev] = i
	}

	othersRow := chosenCount
	rows := chosenCount + 1

	efforts := make([][]float64, rows)
	for i := range efforts {
		efforts[i] = make([]float64, numDays)
	}

	for day, devs := range days {
		if day < 0 || day >= numDays {
			continue
		}

		for dev, stats := range devs {
			row, ok := chosenOrder[dev]
			if !ok {
				row = othersRow
			}

			efforts[row][day] += float64(nonNegativeDevEffort(stats))
		}
	}

	return efforts
}

func cumulativeEfforts(efforts [][]float64) [][]float64 {
	cumulative := make([][]float64, len(efforts))
	for i := range efforts {
		cumulative[i] = make([]float64, len(efforts[i]))

		running := 0.0
		for day, effort := range efforts[i] {
			running += effort
			cumulative[i][day] = running
		}
	}

	return cumulative
}

func normalizedSlepianWindow(m int, bw float64) []float64 {
	window := slepianWindow(m, bw)
	if sum := sumFloat64(window); sum != 0 {
		for i := range window {
			window[i] /= sum
		}
	}

	return window
}

func developerEffortNames(chosen []rankedDevEffort, people []string) []string {
	names := make([]string, 0, len(chosen)+1)
	for _, item := range chosen {
		names = append(names, developerEffortName(item.dev, people))
	}

	return append(names, "others")
}

func developerEffortName(dev int, people []string) string {
	name := ""
	if dev >= 0 && dev < len(people) {
		name = people[dev]
	} else if dev < 0 {
		// Hercules uses a negative sentinel index for commits whose author
		// could not be identified (Python labours labels it "Unidentified").
		name = "Unidentified"
	}

	if name == "" {
		name = fmt.Sprintf("developer %d", dev)
	}

	if len(name) > 40 {
		return name[:37] + "..."
	}

	return name
}

func consecutiveDates(start time.Time, numDays int) []time.Time {
	dates := make([]time.Time, numDays)
	for i := range numDays {
		dates[i] = start.AddDate(0, 0, i)
	}

	return dates
}

func nonNegativeDevEffort(stats readers.DevDay) int {
	effort := stats.LinesAdded + stats.LinesRemoved + stats.LinesModified
	if effort < 0 {
		return 0
	}

	return effort
}

// plotDevEffortsTimeSeries renders the cumulative stackplot at the requested path.
func plotDevEffortsTimeSeries(data devEffortsMatrix, output string, visuals graphics.Options) error {
	if output == "" {
		output = "devs-efforts.png"
	}

	err := graphics.PlotDevsEffortsMatplotlib(
		data.Dates,
		data.CumLayers,
		peopleChartLabels(data.Names),
		graphics.MatplotlibDevsEffortsOptions{
			Title:        "Efforts through time (changed lines of code)",
			Output:       output,
			WidthInches:  16,
			HeightInches: 10,
			FontSize:     visuals.PlotFontSize(),
		},
	)
	if err != nil {
		return err
	}

	fmt.Printf("Saved devs-efforts plot to %s\n", output)

	return nil
}

// slepianWindow returns the zeroth discrete prolate spheroidal sequence (the
// Slepian taper) of length m for the given time-bandwidth scaling, matching
// scipy.signal.windows.dpss(m, bw*m/4) as used by Python labours' old_slepian.
// It is found as the dominant eigenvector of the standard DPSS tridiagonal
// matrix via shifted power iteration (m is small, so this is exact enough).
func slepianWindow(m int, bw float64) []float64 {
	if m <= 0 {
		return nil
	}

	if m == 1 {
		return []float64{1}
	}

	diag, off := slepianTridiagonal(m, bw)
	v := dominantTridiagonalEigenvector(diag, off, positiveDefiniteShift(diag, off))

	// The zeroth DPSS is single-signed; orient it positive.
	if sumFloat64(v) < 0 {
		for i := range v {
			v[i] = -v[i]
		}
	}

	return v
}

func slepianTridiagonal(m int, bw float64) ([]float64, []float64) {
	cos2piW := math.Cos(2 * math.Pi * bw / 4)
	diag := make([]float64, m)

	off := make([]float64, m) // off[n] couples rows n-1 and n (n = 1..m-1)
	for n := range m {
		t := float64(m-1-2*n) / 2
		diag[n] = t * t * cos2piW
	}

	for n := 1; n < m; n++ {
		off[n] = float64(n) * float64(m-n) / 2
	}

	return diag, off
}

func positiveDefiniteShift(diag, off []float64) float64 {
	minBound := math.Inf(1)

	for n := range diag {
		radius := adjacentOffDiagonalSum(off, n)
		minBound = math.Min(minBound, diag[n]-radius)
	}

	if minBound < 0 {
		return -minBound + 1
	}

	return 0
}

func adjacentOffDiagonalSum(off []float64, n int) float64 {
	radius := 0.0
	if n >= 1 {
		radius += math.Abs(off[n])
	}

	if n+1 < len(off) {
		radius += math.Abs(off[n+1])
	}

	return radius
}

func dominantTridiagonalEigenvector(diag, off []float64, shift float64) []float64 {
	vector := make([]float64, len(diag))
	for i := range vector {
		vector[i] = 1
	}

	next := make([]float64, len(diag))
	for range 2000 {
		multiplyShiftedTridiagonal(next, vector, diag, off, shift)

		norm := vectorNorm(next)
		if norm == 0 || normalizeEigenvector(vector, next, norm) < 1e-12 {
			break
		}
	}

	return vector
}

func multiplyShiftedTridiagonal(dst, vector, diag, off []float64, shift float64) {
	for n := range diag {
		dst[n] = (diag[n] + shift) * vector[n]
		if n >= 1 {
			dst[n] += off[n] * vector[n-1]
		}

		if n+1 < len(diag) {
			dst[n] += off[n+1] * vector[n+1]
		}
	}
}

func vectorNorm(vector []float64) float64 {
	squared := 0.0
	for _, value := range vector {
		squared += value * value
	}

	return math.Sqrt(squared)
}

func normalizeEigenvector(vector, next []float64, norm float64) float64 {
	diff := 0.0

	for n := range next {
		scaled := next[n] / norm
		diff += math.Abs(scaled - vector[n])
		vector[n] = scaled
	}

	return diff
}

// smoothRowsPreserveTail convolves each row with window ("same" mode) while
// restoring the trailing values, mirroring Python's edge-preserving smoothing.
func smoothRowsPreserveTail(matrix [][]float64, window []float64) {
	if len(window) == 0 {
		return
	}

	tail := len(window) * 2

	for i := range matrix {
		row := matrix[i]
		n := len(row)

		endLen := min(tail, n)

		ending := append([]float64(nil), row[n-endLen:]...)
		smoothed := convolveSame(row, window)
		copy(smoothed[n-endLen:], ending)
		matrix[i] = smoothed
	}
}

func sumFloat64(values []float64) float64 {
	total := 0.0
	for _, v := range values {
		total += v
	}

	return total
}
