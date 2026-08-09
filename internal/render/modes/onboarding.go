package modes

import (
	"errors"
	"fmt"
	"sort"

	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/optional"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/ticker"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

const (
	onboardingDefaultOutput     = "onboarding.png"
	onboardingRampupOutput      = "onboarding-rampup.png"
	onboardingTimeToFirstOutput = "onboarding-time-to-first.png"
	onboardingAuthorsOutput     = "onboarding-authors.png"

	// onboardingTopAuthors caps the per-author ramp chart. One line per author
	// stops being readable well before this on a real repository, and the
	// cohort heatmap is the chart that degrades gracefully at org scale.
	onboardingTopAuthors = 15

	// onboardingHeatmapColormap matches the retired Python renderer's
	// docs/onboarding_cohorts.png reference image.
	onboardingHeatmapColormap = "YlGnBu"
)

var errOnboardingHeatmapAxes = errors.New("failed to create onboarding cohort axes")

// Onboarding renders the developer ramp-up charts for the Onboarding analysis.
//
// It writes three companion charts next to output: the cohort heatmap
// (_rampup), the time-to-first-meaningful-commit histogram (_time-to-first),
// and the per-author ramps (_authors). Nothing is written to output itself.
func Onboarding(reader readers.Reader, output string) error {
	onboardingReader, ok := reader.(readers.OnboardingReader)
	if !ok {
		return fmt.Errorf("%w: onboarding", readers.ErrAnalysisMissing)
	}

	data, err := onboardingReader.GetOnboarding()
	if err != nil {
		return err
	}

	if data == nil || len(data.Authors) == 0 {
		return fmt.Errorf("%w: onboarding", readers.ErrAnalysisMissing)
	}

	return plotOnboardingCharts(titleRepositoryName(reader.GetName()), data, output)
}

func plotOnboardingCharts(repoName string, data *readers.OnboardingData, output string) error {
	windows := onboardingWindows(data.WindowDays)

	// An empty cohort set is not an empty analysis: the per-author charts still
	// have everything they need, so only the heatmap is skipped.
	if len(data.Cohorts) > 0 && len(windows) > 0 {
		err := plotOnboardingCohortHeatmap(
			repoName, data, windows, siblingOutputPath(output, onboardingDefaultOutput, "rampup"),
		)
		if err != nil {
			return err
		}
	}

	err := plotOnboardingTimeToFirst(
		repoName, data, windows, siblingOutputPath(output, onboardingDefaultOutput, "time-to-first"),
	)
	if err != nil {
		return err
	}

	if len(windows) == 0 {
		return nil
	}

	return plotOnboardingAuthorRamps(
		repoName, data, windows, siblingOutputPath(output, onboardingDefaultOutput, "authors"),
	)
}

// onboardingWindows returns the analysis windows sorted ascending and
// deduplicated, so every chart shares one column order.
func onboardingWindows(windowDays []int) []int {
	seen := make(map[int]bool, len(windowDays))
	windows := make([]int, 0, len(windowDays))

	for _, days := range windowDays {
		if days <= 0 || seen[days] {
			continue
		}

		seen[days] = true

		windows = append(windows, days)
	}

	sort.Ints(windows)

	return windows
}

// plotOnboardingCohortHeatmap reproduces the retired Python renderer's cohort
// heatmap (docs/onboarding_cohorts.png): one row per join cohort, one column
// per window, cells coloured and annotated with the cohort's average
// meaningful lines.
//
// graphics.PlotHeatmapMatplotlib cannot carry the in-cell annotations, the axis
// labels or the colorbar label this chart needs, and it saves on an opaque
// white background where every report chart is transparent. Dropping one level
// down follows plotTemporalHeatmap's precedent.
func plotOnboardingCohortHeatmap(repoName string, data *readers.OnboardingData, windows []int, output string) error {
	output, err := resolveReportOutput(output, onboardingRampupOutput)
	if err != nil {
		return err
	}

	rowLabels, matrix := onboardingCohortMatrix(data, windows)

	colLabels := make([]string, len(windows))
	for i, days := range windows {
		colLabels[i] = fmt.Sprintf("%dd", days)
	}

	graphics.RegisterPythonLaboursHeatmapColormaps()

	width, height := reportPlotPixels(onboardingRampupOutput)
	fig := newReportFigure(width, height)

	ax, err := onboardingHeatmapAxes(fig)
	if err != nil {
		return err
	}

	maxValue := maxOnboardingMatrixValue(matrix)

	ax.SetTitle(repoName + " - Onboarding Cohorts")
	ax.SetXLabel("Days since first commit")
	ax.SetYLabel("Join cohort")

	image := ax.ImShow(matrix, core.ImShowOptions{
		Colormap: optional.Of(onboardingHeatmapColormap),
		VMin:     optional.Of(0.0),
		VMax:     optional.Of(maxValue),
		Aspect:   optional.Of(core.ImageAspect("auto")),
		Origin:   optional.Of(core.ImageOriginUpper),
	})
	if image == nil {
		return errOnboardingHeatmapAxes
	}

	configureOnboardingHeatmapTicks(ax, rowLabels, colLabels)
	annotateOnboardingHeatmap(ax, matrix, maxValue)
	addOnboardingColorbar(fig, maxValue)

	err = saveReportFigureWithoutTightLayout(fig, output, width, height)
	if err != nil {
		return err
	}

	fmt.Printf("Saved %s\n", output)

	return nil
}

// onboardingHeatmapAxes carves out the plot area, leaving room on the left and
// bottom for the cohort labels and axis titles and on the right for the
// colorbar. The figure is saved without a tight layout, so these fractions are
// the layout.
func onboardingHeatmapAxes(fig *core.Figure) (*core.Axes, error) {
	gs := fig.GridSpec(
		1, 1,
		core.WithGridSpecPadding(0.18, 0.80, 0.14, 0.93),
		core.WithGridSpecSpacing(0, 0),
	)

	ax := gs.Cell(0, 0).AddAxes()
	if ax == nil {
		return nil, errOnboardingHeatmapAxes
	}

	return ax, nil
}

func addOnboardingColorbar(fig *core.Figure, maxValue float64) {
	gs := fig.GridSpec(
		1, 1,
		core.WithGridSpecPadding(0.855, 0.885, 0.14, 0.93),
		core.WithGridSpecSpacing(0, 0),
	)

	ax := gs.Cell(0, 0).AddAxes()
	if ax == nil {
		return
	}

	ax.ShowFrame = false
	ax.SetXLim(0, 1)
	ax.SetYLim(0, maxValue)
	ax.SetYLabel("Meaningful lines")

	if ax.XAxis != nil {
		ax.XAxis.ShowSpine = false
		ax.XAxis.ShowTicks = false
		ax.XAxis.ShowLabels = false
	}

	if ax.YAxis != nil {
		ax.YAxis.ShowSpine = false
		ax.YAxis.ShowTicks = false
		ax.YAxis.MinorLocator = nil
	}

	if right := ax.RightAxis(); right != nil {
		right.MinorLocator = nil
	}

	_ = ax.SetYTickLabelPosition("right")
	_ = ax.SetYLabelPosition("right")
	ax.Add(&core.Colorbar{
		Colormap:    onboardingHeatmapColormap,
		Alpha:       1,
		BorderColor: render.Color{R: 0.2, G: 0.2, B: 0.2, A: 0.9},
		BorderWidth: 1,
	})
}

// onboardingCohortMatrix builds the heatmap rows in ascending cohort order,
// labelled "YYYY-MM (n=N)" as the Python renderer did.
func onboardingCohortMatrix(data *readers.OnboardingData, windows []int) ([]string, [][]float64) {
	cohorts := make([]string, 0, len(data.Cohorts))
	for cohort := range data.Cohorts {
		cohorts = append(cohorts, cohort)
	}

	sort.Strings(cohorts)

	labels := make([]string, len(cohorts))
	matrix := make([][]float64, len(cohorts))

	for i, name := range cohorts {
		cohort := data.Cohorts[name]

		label := name
		if label == "" {
			label = cohort.Cohort
		}

		labels[i] = fmt.Sprintf("%s (n=%d)", label, cohort.AuthorCount)
		matrix[i] = make([]float64, len(windows))

		for j, days := range windows {
			if snapshot, ok := cohort.AverageSnapshots[days]; ok {
				matrix[i][j] = snapshot.AvgMeaningfulLines
			}
		}
	}

	return labels, matrix
}

func maxOnboardingMatrixValue(matrix [][]float64) float64 {
	maxValue := 0.0

	for _, row := range matrix {
		for _, value := range row {
			if value > maxValue {
				maxValue = value
			}
		}
	}

	if maxValue <= 0 {
		return 1
	}

	return maxValue
}

func configureOnboardingHeatmapTicks(ax *core.Axes, rowLabels, colLabels []string) {
	xTicks := make([]float64, len(colLabels))
	for i := range colLabels {
		xTicks[i] = float64(i)
	}

	ax.XAxis.Locator = ticker.FixedLocator{TicksList: xTicks}
	ax.XAxis.Formatter = ticker.FixedFormatter{Labels: append([]string(nil), colLabels...)}

	yTicks := make([]float64, len(rowLabels))
	for i := range rowLabels {
		yTicks[i] = float64(i)
	}

	ax.YAxis.Locator = ticker.FixedLocator{TicksList: yTicks}
	ax.YAxis.Formatter = ticker.FixedFormatter{Labels: append([]string(nil), rowLabels...)}
}

// annotateOnboardingHeatmap writes each cell's value into the cell, flipping to
// white text once the cell is dark enough to swallow black text.
func annotateOnboardingHeatmap(ax *core.Axes, matrix [][]float64, maxValue float64) {
	dark := render.Color{R: 1, G: 1, B: 1, A: 1}
	light := render.Color{R: 0, G: 0, B: 0, A: 1}

	for row, values := range matrix {
		for column, value := range values {
			textColor := light
			if maxValue > 0 && value/maxValue > 0.6 {
				textColor = dark
			}

			ax.Text(float64(column), float64(row), fmt.Sprintf("%.0f", value), core.TextOptions{
				FontSize: graphics.PythonPlotFontSize() * 0.8,
				Color:    textColor,
				HAlign:   core.TextAlignCenter,
				VAlign:   core.TextVAlignMiddle,
			})
		}
	}
}

// plotOnboardingTimeToFirst renders the distribution of days from an author's
// first commit to their first meaningful one. The -1 sentinel is not a day
// zero: it means the retained trail holds no meaningful commit at all, so it
// gets its own labelled bucket at the end.
func plotOnboardingTimeToFirst(repoName string, data *readers.OnboardingData, windows []int, output string) error {
	labels, counts := onboardingTimeToFirstBuckets(data, windows)

	return plotIntBars(
		repoName+" - Time to First Meaningful Commit",
		"Days to first meaningful commit",
		"Authors",
		labels,
		counts,
		output,
		onboardingTimeToFirstOutput,
	)
}

func onboardingTimeToFirstBuckets(data *readers.OnboardingData, windows []int) ([]string, []int) {
	labels := onboardingBucketLabels(windows)
	counts := make([]int, len(labels))
	sentinel := len(labels) - 1

	for _, author := range data.Authors {
		counts[onboardingBucketIndex(author.DaysToFirstMeaningfulCommit, windows, sentinel)]++
	}

	return labels, counts
}

// onboardingBucketLabels derives the histogram bins from the analysis windows,
// so the bars line up with the windows the other two charts are drawn on. The
// last bin always holds the "no meaningful commit" sentinel.
func onboardingBucketLabels(windows []int) []string {
	if len(windows) == 0 {
		return []string{"0+d", "none"}
	}

	labels := make([]string, 0, len(windows)+1)
	lower := 0

	for _, days := range windows {
		labels = append(labels, fmt.Sprintf("%d-%dd", lower, days))
		lower = days + 1
	}

	return append(labels, fmt.Sprintf("none within %dd", windows[len(windows)-1]))
}

func onboardingBucketIndex(days int, windows []int, sentinel int) int {
	if days < 0 {
		return sentinel
	}

	// Without windows the labels degrade to a single open-ended "0+d" bin, which
	// every non-negative value belongs in. Falling through to the sentinel below
	// would leave that bin permanently empty and file real ramp-ups under "none".
	if len(windows) == 0 {
		return 0
	}

	for i, window := range windows {
		if days <= window {
			return i
		}
	}

	// Beyond the largest window the trail cannot hold the commit either, so it
	// belongs with the sentinel rather than in the last day bucket.
	return sentinel
}

// plotOnboardingAuthorRamps draws the per-author meaningful-line trajectory for
// the top contributors, ranked by their largest-window meaningful lines.
func plotOnboardingAuthorRamps(repoName string, data *readers.OnboardingData, windows []int, output string) error {
	series := onboardingAuthorSeries(data, windows)
	if len(series) == 0 {
		return nil
	}

	return plotLineSeries(
		fmt.Sprintf("%s - Author Ramp-up (top %d)", repoName, onboardingTopAuthors),
		"Days since first commit",
		"Meaningful lines",
		series,
		output,
		onboardingAuthorsOutput,
	)
}

func onboardingAuthorSeries(data *readers.OnboardingData, windows []int) []namedSeries {
	type rankedAuthor struct {
		id    int
		score int
	}

	largest := windows[len(windows)-1]
	ranked := make([]rankedAuthor, 0, len(data.Authors))

	for id, author := range data.Authors {
		ranked = append(ranked, rankedAuthor{id: id, score: author.Snapshots[largest].MeaningfulLines})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].id < ranked[j].id
		}

		return ranked[i].score > ranked[j].score
	})

	if len(ranked) > onboardingTopAuthors {
		ranked = ranked[:onboardingTopAuthors]
	}

	series := make([]namedSeries, 0, len(ranked))

	for _, entry := range ranked {
		author := data.Authors[entry.id]
		points := make(xySeries, 0, len(windows))

		for _, days := range windows {
			points = append(points, xyPoint{
				X: float64(days),
				Y: float64(author.Snapshots[days].MeaningfulLines),
			})
		}

		series = append(series, namedSeries{Name: onboardingAuthorLabel(data.People, entry.id), Points: points})
	}

	return series
}

// onboardingAuthorLabel resolves an author index against the identity
// dictionary. Negative indices (the wire form of core.AuthorMissing) and
// indices past the dictionary are labelled generically rather than dropped.
func onboardingAuthorLabel(people []string, id int) string {
	if id >= 0 && id < len(people) {
		return peopleChartLabel(people[id])
	}

	return unknownContributorLabel
}
