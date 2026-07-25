package modes

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"time"

	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// DevsEfforts generates the Python labours "Efforts through time" chart: a dual
// mirror stackplot of cumulative versus instantaneous changed lines of code per
// developer over time. When per-day time series are unavailable it falls back
// to a commits-vs-lines scatter. With detail set it also renders the Go-only
// productivity ranking chart as a sibling of the requested output path.
func DevsEfforts(reader readers.Reader, output string, maxPeople int, detail bool) error {
	quiet := viper.GetBool("quiet")
	progEstimator := progress.NewProgressEstimator(!quiet)

	totalPhases := 4 // data extraction, time series, analysis, plotting
	progEstimator.StartMultiOperation(totalPhases, "Developer Efforts Analysis")

	// Phase 1: Extract developer statistics (drives the optional ranking chart
	// and the scatter fallback).
	progEstimator.NextOperation("Extracting developer statistics")
	developerStats, err := reader.GetDeveloperStats()
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to get developer stats: %w", err)
	}

	// Phase 2: Build the per-day efforts time series when available.
	progEstimator.NextOperation("Building effort time series")
	timeSeries, tsErr := reader.GetDeveloperTimeSeriesData()
	startUnix, endUnix := reader.GetHeader()

	progEstimator.NextOperation("Analyzing developer efforts")
	var effortsTimeSeries *devEffortsMatrix
	if tsErr == nil && timeSeries != nil && len(timeSeries.Days) > 0 && endUnix > startUnix {
		if built, buildErr := buildDevEffortsMatrix(timeSeries, startUnix, endUnix, maxPeople, quiet); buildErr == nil {
			effortsTimeSeries = &built
		} else if !quiet {
			fmt.Printf("Falling back to commits-vs-lines scatter: %v\n", buildErr)
		}
	}

	// Phase 4: Generate the primary plot.
	progEstimator.NextOperation("Generating visualization")
	if effortsTimeSeries != nil {
		if err := plotDevEffortsTimeSeries(*effortsTimeSeries, output); err != nil {
			progEstimator.FinishMultiOperation()
			return fmt.Errorf("failed to generate developer efforts plot: %v", err)
		}
	} else {
		if len(developerStats) > maxPeople {
			if !quiet {
				fmt.Printf("Picking top %d developers by commit count.\n", maxPeople)
			}
		}
		if err := plotDevEfforts(effortMetricsForRanking(developerStats, maxPeople), output); err != nil {
			progEstimator.FinishMultiOperation()
			return fmt.Errorf("failed to generate developer efforts plots: %v", err)
		}
	}

	if detail {
		rankingOutput := siblingOutputPath(output, "devs-efforts.png", "productivity_ranking")
		if err := plotProductivityRanking(effortMetricsForRanking(developerStats, maxPeople), rankingOutput); err != nil {
			progEstimator.FinishMultiOperation()
			return fmt.Errorf("failed to generate developer productivity ranking: %v", err)
		}
	}

	progEstimator.FinishMultiOperation()
	if !quiet {
		fmt.Println("Developer efforts analysis completed successfully.")
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

// EffortMetric represents effort analysis for a developer
type EffortMetric struct {
	Name             string
	Commits          int
	LinesAdded       int
	LinesRemoved     int
	LinesModified    int
	FilesTouched     int
	ProductivityRank int
}

// analyzeDevEfforts performs effort analysis on developer statistics
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
func plotDevEfforts(metrics []EffortMetric, output string) error {
	if output == "" {
		output = "devs-efforts.png"
	}
	return plotCommitsVsLines(metrics, output)
}

// plotCommitsVsLines creates scatter plot of commits vs total lines changed
func plotCommitsVsLines(metrics []EffortMetric, output string) error {
	points := make([]graphics.MatplotlibScatterPoint, len(metrics))
	for i, metric := range metrics {
		point := graphics.MatplotlibScatterPoint{
			X: float64(metric.Commits),
			Y: float64(metric.LinesAdded + metric.LinesRemoved + metric.LinesModified),
		}
		if i < 10 { // Only label top 10 to avoid clutter
			point.Label = metric.Name
		}
		points[i] = point
	}

	series := []graphics.MatplotlibScatterSeries{
		{Name: "Developers", Points: points, Color: graphics.ColorPalette[0], Size: 32},
	}
	if err := graphics.PlotScatterMatplotlib(series, graphics.MatplotlibScatterOptions{
		Title:          "Developer Efforts: Commits vs Lines Changed",
		XLabel:         "Total Commits",
		YLabel:         "Total Lines Changed",
		Output:         output,
		WidthInches:    16,
		HeightInches:   8,
		ShowGrid:       true,
		AnnotateLabels: true,
	}); err != nil {
		return fmt.Errorf("failed to save devs-efforts plot %s: %v", output, err)
	}

	fmt.Printf("Saved devs-efforts plot to %s\n", output)
	return nil
}

// plotProductivityRanking creates a bar chart of developer productivity ranking
// at the requested sibling output path (Go-only --devs-efforts-detail chart).
func plotProductivityRanking(metrics []EffortMetric, output string) error {
	if output == "" {
		output = "devs-efforts_productivity_ranking.png"
	}

	// Prepare data for top developers only
	maxDev := len(metrics)
	if maxDev > 20 {
		maxDev = 20 // Show top 20 developers
	}

	labels := make([]string, maxDev)
	values := make([]float64, maxDev)
	for i := 0; i < maxDev; i++ {
		metric := metrics[i]
		labels[i] = fmt.Sprintf("%d", i+1)
		values[i] = float64(metric.Commits) + float64(metric.LinesAdded+metric.LinesRemoved+metric.LinesModified)*0.01
	}

	barColor := color.RGBA{R: 245, G: 133, B: 24, A: 255}
	if err := graphics.PlotBarChartMatplotlib(labels, values, graphics.MatplotlibBarOptions{
		Title:        "Developer Productivity Ranking",
		XLabel:       "Developer Rank",
		YLabel:       "Productivity Score (Commits + Lines/100)",
		Output:       output,
		WidthInches:  15.36,
		HeightInches: 7.68,
		Color:        barColor,
		DisableGrid:  true,
		Opaque:       true,
		DefaultStyle: true,
		ManualXLim:   true,
		XMin:         -0.64,
		XMax:         float64(maxDev) - 0.36,
	}); err != nil {
		return fmt.Errorf("failed to save productivity ranking plot: %v", err)
	}

	fmt.Printf("Saved developer productivity ranking plot to %s\n", output)
	return nil
}

// devEffortsMatrix holds the smoothed cumulative and mirrored instantaneous
// effort layers used by the Python-parity "Efforts through time" stackplot.
type devEffortsMatrix struct {
	Dates      []time.Time
	Names      []string    // chosen developers in effort order, then "others"
	CumLayers  [][]float64 // smoothed cumulative efforts (stacked upward)
	InstLayers [][]float64 // negated, scaled instantaneous efforts (stacked downward)
}

// buildDevEffortsMatrix mirrors Python labours' show_devs_efforts data pipeline:
// per-day changed-lines per developer, top-N selection by total effort with an
// aggregated "others" row, cumulative sums, and Slepian/DPSS smoothing.
func buildDevEffortsMatrix(ts *readers.DeveloperTimeSeriesData, startUnix, endUnix int64, maxPeople int, quiet bool) (devEffortsMatrix, error) {
	start := dateOnly(time.Unix(startUnix, 0))
	end := dateOnly(time.Unix(endUnix, 0))
	numDays := calendarDayCount(start, end)
	if numDays < 2 {
		return devEffortsMatrix{}, fmt.Errorf("not enough days for an effort time series")
	}

	effortsByDev := make(map[int]int)
	for _, devs := range ts.Days {
		for dev, stats := range devs {
			effortsByDev[dev] += stats.LinesAdded + stats.LinesRemoved + stats.LinesModified
		}
	}
	if len(effortsByDev) == 0 {
		return devEffortsMatrix{}, fmt.Errorf("no developer effort data")
	}

	type devEffort struct {
		effort int
		dev    int
	}
	ranked := make([]devEffort, 0, len(effortsByDev))
	for dev, effort := range effortsByDev {
		ranked = append(ranked, devEffort{effort: effort, dev: dev})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].effort == ranked[j].effort {
			return ranked[i].dev < ranked[j].dev
		}
		return ranked[i].effort > ranked[j].effort
	})

	chosenCount := len(ranked)
	if maxPeople > 0 && chosenCount > maxPeople {
		chosenCount = maxPeople
		if !quiet {
			fmt.Printf("Warning: truncated people to the most active %d\n", maxPeople)
		}
	}
	chosen := ranked[:chosenCount]
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
	for day, devs := range ts.Days {
		if day < 0 || day >= numDays {
			continue
		}
		for dev, stats := range devs {
			row, ok := chosenOrder[dev]
			if !ok {
				row = othersRow
			}
			efforts[row][day] += float64(stats.LinesAdded + stats.LinesRemoved + stats.LinesModified)
		}
	}

	cumulative := make([][]float64, rows)
	for i := range efforts {
		cumulative[i] = make([]float64, numDays)
		running := 0.0
		for d := 0; d < numDays; d++ {
			running += efforts[i][d]
			cumulative[i][d] = running
		}
	}

	window := slepianWindow(10, 0.5)
	if sum := sumFloat64(window); sum != 0 {
		for i := range window {
			window[i] /= sum
		}
	}
	smoothRowsPreserveTail(efforts, window)
	smoothRowsPreserveTail(cumulative, window)

	cumMax := matrixMaxFloat64(cumulative)
	effMax := matrixMaxFloat64(efforts)
	scale := 0.0
	if effMax != 0 {
		scale = cumMax / effMax
	}
	instantaneous := make([][]float64, rows)
	for i := range efforts {
		instantaneous[i] = make([]float64, numDays)
		for d := range efforts[i] {
			instantaneous[i][d] = -efforts[i][d] * scale
		}
	}

	names := make([]string, 0, rows)
	for _, item := range chosen {
		name := ""
		if item.dev >= 0 && item.dev < len(ts.People) {
			name = ts.People[item.dev]
		} else if item.dev < 0 {
			// Hercules uses a negative sentinel index for commits whose author
			// could not be identified (Python labours labels it "Unidentified").
			name = "Unidentified"
		}
		if name == "" {
			name = fmt.Sprintf("developer %d", item.dev)
		}
		if len(name) > 40 {
			name = name[:37] + "..."
		}
		names = append(names, name)
	}
	names = append(names, "others")

	dates := make([]time.Time, numDays)
	for i := 0; i < numDays; i++ {
		dates[i] = start.AddDate(0, 0, i)
	}

	return devEffortsMatrix{Dates: dates, Names: names, CumLayers: cumulative, InstLayers: instantaneous}, nil
}

// plotDevEffortsTimeSeries renders the dual mirror stackplot at the requested path.
func plotDevEffortsTimeSeries(data devEffortsMatrix, output string) error {
	if output == "" {
		output = "devs-efforts.png"
	}
	if err := graphics.PlotDevsEffortsMatplotlib(data.Dates, data.CumLayers, data.InstLayers, data.Names, graphics.MatplotlibDevsEffortsOptions{
		Title:        "Efforts through time (changed lines of code)",
		Output:       output,
		WidthInches:  16,
		HeightInches: 10,
	}); err != nil {
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

	nw := bw * float64(m) / 4
	w := nw / float64(m)
	cos2piW := math.Cos(2 * math.Pi * w)

	diag := make([]float64, m)
	for n := 0; n < m; n++ {
		t := float64(m-1-2*n) / 2
		diag[n] = t * t * cos2piW
	}
	off := make([]float64, m) // off[n] couples rows n-1 and n (n = 1..m-1)
	for n := 1; n < m; n++ {
		off[n] = float64(n) * float64(m-n) / 2
	}

	// Shift by a Gershgorin lower bound so the matrix is positive definite and
	// the dominant eigenvalue is also the largest in magnitude.
	minBound := math.Inf(1)
	for n := 0; n < m; n++ {
		radius := 0.0
		if n >= 1 {
			radius += math.Abs(off[n])
		}
		if n+1 < m {
			radius += math.Abs(off[n+1])
		}
		if diag[n]-radius < minBound {
			minBound = diag[n] - radius
		}
	}
	shift := 0.0
	if minBound < 0 {
		shift = -minBound + 1
	}

	v := make([]float64, m)
	for i := range v {
		v[i] = 1
	}
	next := make([]float64, m)
	for iter := 0; iter < 2000; iter++ {
		for n := 0; n < m; n++ {
			sum := (diag[n] + shift) * v[n]
			if n >= 1 {
				sum += off[n] * v[n-1]
			}
			if n+1 < m {
				sum += off[n+1] * v[n+1]
			}
			next[n] = sum
		}
		norm := 0.0
		for _, x := range next {
			norm += x * x
		}
		norm = math.Sqrt(norm)
		if norm == 0 {
			break
		}
		diff := 0.0
		for n := range next {
			scaled := next[n] / norm
			diff += math.Abs(scaled - v[n])
			v[n] = scaled
		}
		if diff < 1e-12 {
			break
		}
	}

	// The zeroth DPSS is single-signed; orient it positive.
	if sumFloat64(v) < 0 {
		for i := range v {
			v[i] = -v[i]
		}
	}
	return v
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
		endLen := tail
		if endLen > n {
			endLen = n
		}
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

func matrixMaxFloat64(matrix [][]float64) float64 {
	maxValue := math.Inf(-1)
	for _, row := range matrix {
		for _, v := range row {
			if v > maxValue {
				maxValue = v
			}
		}
	}
	if math.IsInf(maxValue, -1) {
		return 0
	}
	return maxValue
}
