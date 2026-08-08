package modes

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

const timeAxisLabel = "Time"

// otherLanguageLabel is the synthetic band holding everything outside the top N.
const otherLanguageLabel = "Other"

// User-facing --resample values accepted by normalizeLanguageFrequency.
const (
	resampleYear  = "year"
	resampleMonth = "month"
	resampleWeek  = "week"
	resampleDay   = "day"
)

// Chart file extensions this package writes and dispatches on.
const (
	extensionSVG = ".svg"
	extensionPNG = ".png"
)

// Languages generates language statistics and visualization showing the distribution
// of programming languages used in the repository.
func Languages(reader readers.Reader, output string) error {
	return LanguagesWithOptions(reader, output, defaultOptions())
}

func LanguagesWithOptions(reader readers.Reader, output string, opts Options) error {
	timeSeries, timeSeriesErr := reader.GetDeveloperTimeSeriesData()

	// Step 1: Read language statistics
	languageStats, err := reader.GetLanguageStats()
	if err != nil {
		return fmt.Errorf("failed to get language stats: %w", err)
	}

	if len(languageStats) == 0 {
		return errors.New("no language statistics found in the data - the input file may not contain language analysis results")
	}

	// Step 2: Sort languages by line count (descending)
	sort.Slice(languageStats, func(i, j int) bool {
		return languageStats[i].Lines > languageStats[j].Lines
	})

	// Step 3: Generate visualization
	startUnix, endUnix := reader.GetHeader()
	if timeSeriesErr == nil && timeSeries != nil && len(timeSeries.Days) > 0 && startUnix > 0 && endUnix > startUnix {
		err = plotLanguageEvolutionWithOptions(timeSeries, startUnix, endUnix, opts.Resample, output, opts.Graphics)
	} else {
		err = plotLanguages(languageStats, output, opts.Graphics)
	}

	if err != nil {
		return fmt.Errorf("failed to generate language plot: %w", err)
	}

	fmt.Printf("Language analysis completed. Found %d languages.\n", len(languageStats))

	return nil
}

// languageEvolution carries the plotted matrix plus the figures needed to
// describe it honestly. Total and Peak are deliberately separate: the headline
// must be what the corpus looks like *now*, while the y-axis has to accommodate
// the highest point the stack ever reached.
type languageEvolution struct {
	Languages []string
	Dates     []time.Time
	// Matrix is what gets drawn, so its per-language values are clamped at zero
	// — a stacked area cannot render a negative band.
	Matrix [][]float64
	// Total is the unclamped stack sum at the last sample: comparable to the
	// burndown's "lines still alive".
	Total float64
	// Peak is the largest clamped stack sum across all samples. Y-axis only.
	Peak float64
	// ClampedAway is how many net-negative lines Matrix hides at the last
	// sample. Non-zero means the drawn stack sits above Total.
	ClampedAway float64
	// UnclassifiedShare is the fraction of all changed lines whose language
	// could not be resolved. Those lines appear in no band at all.
	UnclassifiedShare float64
}

type languageTotal struct {
	Name  string
	Total int
}

type languageSelection struct {
	languages []string
	top       map[string]bool
	index     map[string]int
}

func plotLanguageEvolution(timeSeries *readers.DeveloperTimeSeriesData, startUnix, endUnix int64, resample, output string) error {
	return plotLanguageEvolutionWithOptions(timeSeries, startUnix, endUnix, resample, output, graphics.DefaultOptions())
}

func plotLanguageEvolutionWithOptions(
	timeSeries *readers.DeveloperTimeSeriesData,
	startUnix, endUnix int64,
	resample, output string,
	opts graphics.Options,
) error {
	data, err := buildLanguageEvolution(timeSeries, startUnix, endUnix, resample)
	if err != nil {
		return err
	}

	colors := graphics.PythonLaboursColorPalette(len(data.Languages))

	series := make([]graphics.MatplotlibTimeAreaSeries, len(data.Languages))
	for langIndex, lang := range data.Languages {
		values := make([]float64, len(data.Dates))
		for i := range data.Dates {
			values[i] = data.Matrix[i][langIndex]
		}

		series[langIndex] = graphics.MatplotlibTimeAreaSeries{
			Label:  lang,
			Values: values,
			Color:  colors[langIndex%len(colors)],
		}
	}

	outputs, err := languageOutputPaths(output)
	if err != nil {
		return err
	}

	for _, outputPath := range outputs {
		err := graphics.PlotTimeAreasMatplotlib(data.Dates, series, graphics.MatplotlibTimeAreaOptions{
			Title:            "Language Evolution Over Time",
			Subtitle:         languageEvolutionSubtitle(data),
			XLabel:           timeAxisLabel,
			YLabel:           "Lines of Code",
			Output:           outputPath,
			WidthInches:      16,
			HeightInches:     12,
			Stacked:          true,
			FullNumberYTicks: true,
			Legend:           true,
			LegendLeft:       true,
			LegendTop:        true,
			YMin:             0,
			// The axis has to clear the highest point the stack ever reached,
			// which is not the same as the headline figure any more.
			YMax:     math.Max(data.Peak*1.05, 1),
			ShowGrid: false,
			FontSize: opts.PlotFontSize(),
		})
		if err != nil {
			return err
		}

		fmt.Printf("Language chart saved to %s\n", outputPath)
	}

	return nil
}

// languageEvolutionSubtitle states what the stack does and does not account
// for. It goes in the subtitle rather than the title because the axes title is
// rendered as a single line — core.Axes.SetTitle measures with
// measureSingleLineTextLayout and never splits on "\n" — so the "\n(Total: …)"
// this used to carry was drawn as one run with a literal newline in it.
//
// drawSubtitle is likewise a single line, so keep the clauses short and only
// emit the ones that apply.
func languageEvolutionSubtitle(data languageEvolution) string {
	clauses := []string{"Total " + formatFloatWithCommas(data.Total) + " lines"}

	if len(data.Dates) > 0 {
		clauses[0] += " at " + data.Dates[len(data.Dates)-1].Format("2006-01-02")
	}

	// Only worth saying when the corpus has shrunk since its high point;
	// otherwise the peak is the total and repeating it is noise.
	if data.Peak > data.Total {
		clauses = append(clauses, "peak "+formatFloatWithCommas(data.Peak))
	}

	if count := len(data.Languages); count > 0 {
		if slices.Contains(data.Languages, otherLanguageLabel) {
			clauses = append(clauses, fmt.Sprintf("top %d languages + Other", count-1))
		} else {
			clauses = append(clauses, fmt.Sprintf("%d languages", count))
		}
	}

	if data.UnclassifiedShare >= 0.0005 {
		clauses = append(clauses, formatPercent(data.UnclassifiedShare)+" of changed lines unclassified")
	}

	// A negative cumulative means more removals were attributed to a language
	// than additions. The band is floored at zero, so the drawn stack sits
	// above the real figure by this much; say so rather than quietly inflating.
	if data.ClampedAway >= 1 {
		clauses = append(clauses, formatFloatWithCommas(data.ClampedAway)+" lines net-negative, not shown")
	}

	return strings.Join(clauses, " · ")
}

func formatPercent(share float64) string {
	if share < 0.095 {
		return fmt.Sprintf("%.1f%%", share*100)
	}

	return fmt.Sprintf("%.0f%%", share*100)
}

func buildLanguageEvolution(timeSeries *readers.DeveloperTimeSeriesData, startUnix, endUnix int64, resample string) (languageEvolution, error) {
	start, end, totalDays := timeSeriesCalendarRange(timeSeries, startUnix, endUnix, 0)
	if totalDays <= 0 {
		return languageEvolution{}, errors.New("no temporal data to plot")
	}

	totals, unclassifiedShare := netLanguageTotals(timeSeries.Days)
	if len(totals) == 0 {
		return languageEvolution{}, errors.New("no language data to plot")
	}

	selection := selectEvolutionLanguages(totals, 10)
	daily, dailyRaw := dailyLanguageEvolution(timeSeries.Days, totalDays, selection)

	dates := languageSampleDates(daily, start, end, normalizeLanguageFrequency(resample))
	if len(dates) == 0 {
		dates = []time.Time{start}
	}

	// Both matrices are sampled on the same dates so the last row of one lines
	// up with the last row of the other.
	matrix := sampleLanguageMatrix(daily, start, dates)
	rawMatrix := sampleLanguageMatrix(dailyRaw, start, dates)

	total := sumFloat64(rawMatrix[len(rawMatrix)-1])
	drawn := sumFloat64(matrix[len(matrix)-1])

	return languageEvolution{
		Languages:         selection.languages,
		Dates:             dates,
		Matrix:            matrix,
		Total:             total,
		Peak:              maximumLanguageTotal(matrix),
		ClampedAway:       drawn - total,
		UnclassifiedShare: unclassifiedShare,
	}, nil
}

// netLanguageTotals ranks languages by the same quantity the bands actually
// plot — added minus removed — and reports what share of all changed lines
// carries no resolved language at all.
//
// Ranking used to run on added+removed+changed. That let a language with heavy
// churn but negligible net growth take a top-10 band away from a steadier but
// substantially larger one, so the legend and the areas disagreed about what
// mattered.
//
// The unclassified share is measured on churn rather than net, because a line
// with no resolved language contributes to neither side of the net figure and
// would otherwise be invisible. Those lines appear in no band on the chart.
func netLanguageTotals(days map[int]map[int]readers.DevDay) (map[string]int, float64) {
	totals := make(map[string]int)

	var classifiedChurn, unclassifiedChurn float64

	for _, developers := range days {
		for _, stats := range developers {
			for language, values := range stats.Languages {
				if language == "" {
					unclassifiedChurn += churnLines(values)

					continue
				}

				classifiedChurn += churnLines(values)
				totals[language] += netLines(values)
			}
		}
	}

	share := 0.0
	if all := classifiedChurn + unclassifiedChurn; all > 0 {
		share = unclassifiedChurn / all
	}

	return totals, share
}

// netLines is added minus removed: how much of a language exists as a result of
// the change, which is what the stacked bands accumulate.
func netLines(values []int) int {
	if len(values) < 2 {
		return 0
	}

	return values[0] - values[1]
}

// churnLines is added plus removed plus changed: how much a language was worked
// on. Used only to weigh the unclassified share, never to size a band.
func churnLines(values []int) float64 {
	total := 0
	for _, value := range values {
		total += value
	}

	return float64(total)
}

func selectEvolutionLanguages(totals map[string]int, limit int) languageSelection {
	ranked := make([]languageTotal, 0, len(totals))
	for language, total := range totals {
		ranked = append(ranked, languageTotal{Name: language, Total: total})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Total == ranked[j].Total {
			return ranked[i].Name < ranked[j].Name
		}

		return ranked[i].Total > ranked[j].Total
	})

	topCount := min(len(ranked), limit)

	selection := languageSelection{
		languages: make([]string, 0, topCount+1),
		top:       make(map[string]bool, topCount),
	}
	for _, item := range ranked[:topCount] {
		selection.top[item.Name] = true
		selection.languages = append(selection.languages, item.Name)
	}

	sort.Strings(selection.languages)

	if len(ranked) > topCount {
		selection.languages = append(selection.languages, otherLanguageLabel)
	}

	selection.index = make(map[string]int, len(selection.languages))
	for i, language := range selection.languages {
		selection.index[language] = i
	}

	return selection
}

// dailyLanguageEvolution returns one row per calendar day, twice: `clamped` is
// what gets drawn (per-language values floored at zero, because a stacked area
// cannot render a negative band) and `raw` keeps the true cumulative so the
// headline figure does not inherit the clamp's upward bias.
//
// Every day gets a row, including days on which nothing happened. The previous
// version wrote rows only for days present in `days` and patched the gaps
// afterwards by copying the preceding row wherever a value was exactly zero.
// That could not distinguish "no activity today" from "this language has been
// deleted down to nothing", so a language that genuinely reached zero kept its
// last positive value on the chart forever.
func dailyLanguageEvolution(
	days map[int]map[int]readers.DevDay,
	totalDays int,
	selection languageSelection,
) (clamped, raw [][]float64) {
	clamped = make([][]float64, totalDays)
	raw = make([][]float64, totalDays)

	cumulative := make(map[string]int)

	for day := range totalDays {
		if developers, ok := days[day]; ok {
			addDailyLanguageChanges(cumulative, developers, selection)
		}

		clamped[day] = make([]float64, len(selection.languages))
		raw[day] = make([]float64, len(selection.languages))

		writeDailyLanguageTotals(clamped[day], raw[day], cumulative, selection.languages)
	}

	return clamped, raw
}

func addDailyLanguageChanges(
	cumulative map[string]int,
	developers map[int]readers.DevDay,
	selection languageSelection,
) {
	for _, stats := range developers {
		for language, values := range stats.Languages {
			if language == "" || len(values) < 2 {
				continue
			}

			target, ok := selectedLanguageTarget(language, selection)
			if ok {
				cumulative[target] += values[0] - values[1]
			}
		}
	}
}

func selectedLanguageTarget(language string, selection languageSelection) (string, bool) {
	if selection.top[language] {
		return language, true
	}

	_, hasOther := selection.index[otherLanguageLabel]

	return otherLanguageLabel, hasOther
}

func writeDailyLanguageTotals(clamped, raw []float64, cumulative map[string]int, languages []string) {
	for i, language := range languages {
		value := float64(cumulative[language])
		raw[i] = value
		clamped[i] = math.Max(0, value)
	}
}

func maximumLanguageTotal(matrix [][]float64) float64 {
	total := 0.0
	for _, row := range matrix {
		total = math.Max(total, sumFloat64(row))
	}

	return total
}

func normalizeLanguageFrequency(frequency string) string {
	switch frequency {
	case "", resampleYear:
		return "YE"
	case resampleMonth:
		return "ME"
	case resampleDay, "raw", "no":
		return "D"
	case resampleWeek:
		return "W"
	default:
		return frequency
	}
}

func languageSampleDates(daily [][]float64, start, end time.Time, frequency string) []time.Time {
	// The daily matrix can run past `end`: timeSeriesCalendarRange stretches it
	// to cover the highest tick index present in the data. Sample up to whatever
	// the data actually reaches, not just to the header's end date.
	last := end
	if len(daily) > 0 {
		if dataEnd := start.AddDate(0, 0, len(daily)-1); dataEnd.After(last) {
			last = dataEnd
		}
	}

	var dates []time.Time

	switch frequency {
	case "YE", "A":
		dates = languageYearEndDates(start, last)
	case "ME", "M":
		dates = languageMonthEndDates(start, last)
	case "W":
		dates = languageWeeklyDates(start, last)
	default:
		return consecutiveDates(start, len(daily))
	}

	// Periodic sampling lands only on whole period boundaries, so it stops at
	// the last completed year/month/week and silently drops everything after it
	// — both from the plotted area and from the headline total. On the default
	// yearly resample that meant a chart rendered in August 2026 ended at
	// 2025-12-31 and omitted the entire current year. Close the gap with a final
	// sample at the true end of the data.
	if len(dates) == 0 || dates[len(dates)-1].Before(last) {
		dates = append(dates, last)
	}

	return dates
}

func languageYearEndDates(start, end time.Time) []time.Time {
	dates := make([]time.Time, 0, end.Year()-start.Year()+1)
	for year := start.Year(); year <= end.Year(); year++ {
		date := time.Date(year, time.December, 31, start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), start.Location())
		if !date.Before(start) && !date.After(end) {
			dates = append(dates, date)
		}
	}

	return dates
}

func languageMonthEndDates(start, end time.Time) []time.Time {
	var dates []time.Time

	for current := languageMonthEnd(start.Year(), start.Month(), start); !current.After(end); {
		if !current.Before(start) {
			dates = append(dates, current)
		}

		next := current.AddDate(0, 1, 0)
		current = languageMonthEnd(next.Year(), next.Month(), start)
	}

	return dates
}

func languageWeeklyDates(start, end time.Time) []time.Time {
	var dates []time.Time
	for current := start; !current.After(end); current = current.AddDate(0, 0, 7) {
		dates = append(dates, current)
	}

	return dates
}

func sampleLanguageMatrix(daily [][]float64, start time.Time, dates []time.Time) [][]float64 {
	matrix := make([][]float64, len(dates))
	for i, date := range dates {
		day := min(max(calendarDayIndex(start, date), 0), len(daily)-1)
		matrix[i] = append([]float64(nil), daily[day]...)
	}

	return matrix
}

func languageMonthEnd(year int, month time.Month, ref time.Time) time.Time {
	return time.Date(year, month+1, 1, ref.Hour(), ref.Minute(), ref.Second(), ref.Nanosecond(), ref.Location()).AddDate(0, 0, -1)
}

// formatFloatWithCommas renders a line count with thousands separators. Line
// counts are whole numbers, so it rounds rather than emitting the "%.1f" tail
// that used to make the chart read "9,298,469.0 lines".
func formatFloatWithCommas(v float64) string {
	digits := fmt.Sprintf("%.0f", math.Abs(v))

	var out []rune

	for i, r := range reverseString(digits) {
		if i > 0 && i%3 == 0 {
			out = append(out, ',')
		}

		out = append(out, r)
	}

	formatted := reverseString(string(out))
	if math.Signbit(v) && strings.Trim(digits, "0") != "" {
		formatted = "-" + formatted
	}

	return formatted
}

func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}

	return string(runes)
}

// plotLanguages creates a bar chart showing language distribution by churn.
//
// The fallback branch, used only when the input carries no devs time series.
// Note that readers.LanguageStat.Lines is added+removed+changed (see
// readers/helpers.go languageTotals), i.e. how much a language was *worked on*
// — not how much of it exists. That is a different quantity from the stacked
// evolution chart above, which plots added-minus-removed, and the two differ by
// several times on a real corpus. The labels here say so rather than calling
// both "lines of code".
func plotLanguages(
	languageStats []readers.LanguageStat,
	output string,
	optionValues ...graphics.Options,
) error {
	visuals := graphics.DefaultOptions()
	if len(optionValues) > 0 {
		visuals = optionValues[0]
	}
	// Prepare data for the bar chart
	names := make([]string, len(languageStats))
	values := make([]float64, len(languageStats))

	for i, stat := range languageStats {
		names[i] = stat.Language
		values[i] = float64(stat.Lines)
	}

	outputs, err := languageOutputPaths(output)
	if err != nil {
		return err
	}

	for _, outputPath := range outputs {
		err := graphics.PlotBarChartMatplotlib(names, values, graphics.MatplotlibBarOptions{
			Title:        "Programming Languages by Churn (added + removed + changed lines)",
			XLabel:       "Languages",
			YLabel:       "Changed lines",
			Output:       outputPath,
			WidthInches:  16,
			HeightInches: 12,
			RotateX:      len(languageStats) > 10,
			FontSize:     visuals.PlotFontSize(),
		})
		if err != nil {
			return err
		}

		fmt.Printf("Language chart saved to %s\n", outputPath)
	}

	printLanguageChurnSummary(languageStats)

	return nil
}

// printLanguageChurnSummary writes the per-language breakdown to stdout.
func printLanguageChurnSummary(languageStats []readers.LanguageStat) {
	fmt.Println("\nLanguage Statistics (churn: added + removed + changed lines):")
	fmt.Println("============================================================")

	totalLines := 0
	for _, stat := range languageStats {
		totalLines += stat.Lines
	}

	for i, stat := range languageStats {
		percentage := float64(stat.Lines) / float64(totalLines) * 100
		fmt.Printf("%2d. %-15s %8d changed lines (%5.1f%%)\n", i+1, stat.Language, stat.Lines, percentage)
	}

	fmt.Printf("\nTotal churn: %d changed lines across %d languages\n", totalLines, len(languageStats))
}

func languageOutputPaths(output string) ([]string, error) {
	if output == "" {
		output = "."
	}

	ext := strings.ToLower(filepath.Ext(output))
	if ext != "" {
		if dir := filepath.Dir(output); dir != "." {
			err := os.MkdirAll(dir, 0o750)
			if err != nil {
				return nil, fmt.Errorf("failed to create output directory %s: %w", dir, err)
			}
		}

		return []string{output}, nil
	}

	err := os.MkdirAll(output, 0o750)
	if err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", output, err)
	}

	return []string{
		filepath.Join(output, "languages.png"),
		filepath.Join(output, "languages.svg"),
	}, nil
}
