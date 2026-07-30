package modes

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

const timeAxisLabel = "Time"

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

type languageEvolution struct {
	Languages []string
	Dates     []time.Time
	Matrix    [][]float64
	Total     float64
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
			Title:            fmt.Sprintf("Language Evolution Over Time\n(Total: %s lines)", formatFloatWithCommas(data.Total)),
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
			YMax:             math.Max(data.Total*1.05, 1),
			ShowGrid:         false,
			FontSize:         opts.PlotFontSize(),
		})
		if err != nil {
			return err
		}

		fmt.Printf("Language chart saved to %s\n", outputPath)
	}

	return nil
}

func buildLanguageEvolution(timeSeries *readers.DeveloperTimeSeriesData, startUnix, endUnix int64, resample string) (languageEvolution, error) {
	start, end, totalDays := timeSeriesCalendarRange(timeSeries, startUnix, endUnix, 0)
	if totalDays <= 0 {
		return languageEvolution{}, errors.New("no temporal data to plot")
	}

	totals := totalLanguageActivity(timeSeries.Days)
	if len(totals) == 0 {
		return languageEvolution{}, errors.New("no language data to plot")
	}

	selection := selectEvolutionLanguages(totals, 10)
	daily := dailyLanguageEvolution(timeSeries.Days, totalDays, selection)
	dates, matrix := resampleLanguageMatrix(daily, start, end, resample)

	return languageEvolution{
		Languages: selection.languages,
		Dates:     dates,
		Matrix:    matrix,
		Total:     maximumLanguageTotal(matrix),
	}, nil
}

func totalLanguageActivity(days map[int]map[int]readers.DevDay) map[string]int {
	totals := make(map[string]int)

	for _, developers := range days {
		for _, stats := range developers {
			for language, values := range stats.Languages {
				if language == "" {
					continue
				}

				for _, value := range values {
					totals[language] += value
				}
			}
		}
	}

	return totals
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
		selection.languages = append(selection.languages, "Other")
	}

	selection.index = make(map[string]int, len(selection.languages))
	for i, language := range selection.languages {
		selection.index[language] = i
	}

	return selection
}

func dailyLanguageEvolution(
	days map[int]map[int]readers.DevDay,
	totalDays int,
	selection languageSelection,
) [][]float64 {
	daily := make([][]float64, totalDays)
	for day := range daily {
		daily[day] = make([]float64, len(selection.languages))
	}

	cumulative := make(map[string]int)

	for _, day := range sortedDeveloperDays(days) {
		if day < 0 || day >= totalDays {
			continue
		}

		addDailyLanguageChanges(cumulative, days[day], selection)
		writeDailyLanguageTotals(daily[day], cumulative, selection.languages)
	}

	carryLanguageTotalsForward(daily)

	return daily
}

func sortedDeveloperDays(days map[int]map[int]readers.DevDay) []int {
	result := make([]int, 0, len(days))
	for day := range days {
		result = append(result, day)
	}

	sort.Ints(result)

	return result
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

	_, hasOther := selection.index["Other"]

	return "Other", hasOther
}

func writeDailyLanguageTotals(row []float64, cumulative map[string]int, languages []string) {
	for i, language := range languages {
		row[i] = math.Max(0, float64(cumulative[language]))
	}
}

func carryLanguageTotalsForward(daily [][]float64) {
	for day := 1; day < len(daily); day++ {
		for language := range daily[day] {
			if daily[day][language] == 0 && daily[day-1][language] > 0 {
				daily[day][language] = daily[day-1][language]
			}
		}
	}
}

func maximumLanguageTotal(matrix [][]float64) float64 {
	total := 0.0
	for _, row := range matrix {
		total = math.Max(total, sumFloat64(row))
	}

	return total
}

func resampleLanguageMatrix(daily [][]float64, start, end time.Time, resample string) ([]time.Time, [][]float64) {
	dates := languageSampleDates(daily, start, end, normalizeLanguageFrequency(resample))
	if len(dates) == 0 {
		dates = []time.Time{start}
	}

	return dates, sampleLanguageMatrix(daily, start, dates)
}

func normalizeLanguageFrequency(frequency string) string {
	switch frequency {
	case "", "year":
		return "YE"
	case "month":
		return "ME"
	case "day", "raw", "no":
		return "D"
	case "week":
		return "W"
	default:
		return frequency
	}
}

func languageSampleDates(daily [][]float64, start, end time.Time, frequency string) []time.Time {
	switch frequency {
	case "YE", "A":
		return languageYearEndDates(start, end)
	case "ME", "M":
		return languageMonthEndDates(start, end)
	case "W":
		return languageWeeklyDates(start, end)
	default:
		return consecutiveDates(start, len(daily))
	}
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

func formatFloatWithCommas(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	parts := strings.SplitN(s, ".", 2)
	intPart := parts[0]
	var out []rune

	for i, r := range reverseString(intPart) {
		if i > 0 && i%3 == 0 {
			out = append(out, ',')
		}

		out = append(out, r)
	}

	formatted := reverseString(string(out))
	if len(parts) == 2 {
		formatted += "." + parts[1]
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

// plotLanguages creates a bar chart showing language distribution by lines of code.
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
			Title:        "Programming Languages by Lines of Code",
			XLabel:       "Languages",
			YLabel:       "Lines of Code",
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

	// Print text summary
	fmt.Println("\nLanguage Statistics:")
	fmt.Println("====================")

	totalLines := 0
	for _, stat := range languageStats {
		totalLines += stat.Lines
	}

	for i, stat := range languageStats {
		percentage := float64(stat.Lines) / float64(totalLines) * 100
		fmt.Printf("%2d. %-15s %8d lines (%5.1f%%)\n", i+1, stat.Language, stat.Lines, percentage)
	}

	fmt.Printf("\nTotal: %d lines across %d languages\n", totalLines, len(languageStats))

	return nil
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
