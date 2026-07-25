package modes

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// Languages generates language statistics and visualization showing the distribution
// of programming languages used in the repository.
func Languages(reader readers.Reader, output string) error {
	timeSeries, timeSeriesErr := reader.GetDeveloperTimeSeriesData()

	// Step 1: Read language statistics
	languageStats, err := reader.GetLanguageStats()
	if err != nil {
		return fmt.Errorf("failed to get language stats: %w", err)
	}

	if len(languageStats) == 0 {
		return fmt.Errorf("no language statistics found in the data - the input file may not contain language analysis results")
	}

	// Step 2: Sort languages by line count (descending)
	sort.Slice(languageStats, func(i, j int) bool {
		return languageStats[i].Lines > languageStats[j].Lines
	})

	// Step 3: Generate visualization
	startUnix, endUnix := reader.GetHeader()
	if timeSeriesErr == nil && timeSeries != nil && len(timeSeries.Days) > 0 && startUnix > 0 && endUnix > startUnix {
		err = plotLanguageEvolution(timeSeries, startUnix, endUnix, viper.GetString("resample"), output)
	} else {
		err = plotLanguages(languageStats, output)
	}
	if err != nil {
		return fmt.Errorf("failed to generate language plot: %v", err)
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

func plotLanguageEvolution(timeSeries *readers.DeveloperTimeSeriesData, startUnix, endUnix int64, resample, output string) error {
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
		if err := graphics.PlotTimeAreasMatplotlib(data.Dates, series, graphics.MatplotlibTimeAreaOptions{
			Title:        fmt.Sprintf("Language Evolution Over Time\n(Total: %s lines)", formatFloatWithCommas(data.Total)),
			XLabel:       "Time",
			YLabel:       "Lines of Code",
			Output:       outputPath,
			WidthInches:  16,
			HeightInches: 12,
			Stacked:      true,
			Legend:       true,
			LegendLeft:   true,
			LegendTop:    true,
			YMin:         0,
			YMax:         math.Max(data.Total*1.05, 1),
			ShowGrid:     false,
		}); err != nil {
			return err
		}
		fmt.Printf("Language chart saved to %s\n", outputPath)
	}
	return nil
}

func buildLanguageEvolution(timeSeries *readers.DeveloperTimeSeriesData, startUnix, endUnix int64, resample string) (languageEvolution, error) {
	start, end, totalDays := timeSeriesCalendarRange(timeSeries, startUnix, endUnix, 0)
	if totalDays <= 0 {
		return languageEvolution{}, fmt.Errorf("no temporal data to plot")
	}

	totalLangs := make(map[string]int)
	for _, devs := range timeSeries.Days {
		for _, stats := range devs {
			for lang, vals := range stats.Languages {
				if lang == "" {
					continue
				}
				for _, v := range vals {
					totalLangs[lang] += v
				}
			}
		}
	}
	if len(totalLangs) == 0 {
		return languageEvolution{}, fmt.Errorf("no language data to plot")
	}

	type langTotal struct {
		Name  string
		Total int
	}
	sortedLangs := make([]langTotal, 0, len(totalLangs))
	for lang, total := range totalLangs {
		sortedLangs = append(sortedLangs, langTotal{Name: lang, Total: total})
	}
	sort.Slice(sortedLangs, func(i, j int) bool {
		if sortedLangs[i].Total == sortedLangs[j].Total {
			return sortedLangs[i].Name < sortedLangs[j].Name
		}
		return sortedLangs[i].Total > sortedLangs[j].Total
	})

	topCount := min(len(sortedLangs), 10)
	topLanguages := make(map[string]bool, topCount)
	for _, item := range sortedLangs[:topCount] {
		topLanguages[item.Name] = true
	}
	languages := make([]string, 0, topCount+1)
	for lang := range topLanguages {
		languages = append(languages, lang)
	}
	sort.Strings(languages)
	if len(sortedLangs) > topCount {
		languages = append(languages, "Other")
	}
	langIndex := make(map[string]int, len(languages))
	for i, lang := range languages {
		langIndex[lang] = i
	}

	daily := make([][]float64, totalDays)
	cumulative := make(map[string]int)
	for i := range daily {
		daily[i] = make([]float64, len(languages))
	}
	days := make([]int, 0, len(timeSeries.Days))
	for day := range timeSeries.Days {
		days = append(days, day)
	}
	sort.Ints(days)
	for _, day := range days {
		if day < 0 || day >= totalDays {
			continue
		}
		for _, stats := range timeSeries.Days[day] {
			for lang, vals := range stats.Languages {
				if lang == "" || len(vals) < 2 {
					continue
				}
				target := lang
				if !topLanguages[lang] {
					if _, ok := langIndex["Other"]; !ok {
						continue
					}
					target = "Other"
				}
				cumulative[target] += vals[0] - vals[1]
			}
		}
		for i, lang := range languages {
			daily[day][i] = math.Max(0, float64(cumulative[lang]))
		}
	}
	for day := 1; day < totalDays; day++ {
		for i := range languages {
			if daily[day][i] == 0 && daily[day-1][i] > 0 {
				daily[day][i] = daily[day-1][i]
			}
		}
	}

	dates, matrix := resampleLanguageMatrix(daily, start, end, resample)
	total := 0.0
	for _, row := range matrix {
		rowTotal := 0.0
		for _, value := range row {
			rowTotal += value
		}
		if rowTotal > total {
			total = rowTotal
		}
	}
	return languageEvolution{Languages: languages, Dates: dates, Matrix: matrix, Total: total}, nil
}

func resampleLanguageMatrix(daily [][]float64, start, end time.Time, resample string) ([]time.Time, [][]float64) {
	freq := resample
	switch freq {
	case "", "year":
		freq = "YE"
	case "month":
		freq = "ME"
	case "day", "raw", "no":
		freq = "D"
	case "week":
		freq = "W"
	}

	var dates []time.Time
	switch freq {
	case "YE", "A":
		for year := start.Year(); year <= end.Year(); year++ {
			dt := time.Date(year, time.December, 31, start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), start.Location())
			if !dt.Before(start) && !dt.After(end) {
				dates = append(dates, dt)
			}
		}
	case "ME", "M":
		for current := languageMonthEnd(start.Year(), start.Month(), start); !current.After(end); {
			if !current.Before(start) {
				dates = append(dates, current)
			}
			next := current.AddDate(0, 1, 0)
			current = languageMonthEnd(next.Year(), next.Month(), start)
		}
	case "W":
		for current := start; !current.After(end); current = current.AddDate(0, 0, 7) {
			dates = append(dates, current)
		}
	default:
		for i := range daily {
			dates = append(dates, start.AddDate(0, 0, i))
		}
	}
	if len(dates) == 0 {
		dates = []time.Time{start}
	}

	matrix := make([][]float64, len(dates))
	for i, dt := range dates {
		day := calendarDayIndex(start, dt)
		if day < 0 {
			day = 0
		}
		if day >= len(daily) {
			day = len(daily) - 1
		}
		matrix[i] = append([]float64(nil), daily[day]...)
	}
	return dates, matrix
}

func languageMonthEnd(year int, month time.Month, ref time.Time) time.Time {
	return time.Date(year, month+1, 1, ref.Hour(), ref.Minute(), ref.Second(), ref.Nanosecond(), ref.Location()).AddDate(0, 0, -1)
}

func formatFloatWithCommas(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	parts := strings.SplitN(s, ".", 2)
	intPart := parts[0]
	var out []byte
	for i, r := range reverseString(intPart) {
		if i > 0 && i%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(r))
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

// plotLanguages creates a bar chart showing language distribution by lines of code
func plotLanguages(languageStats []readers.LanguageStat, output string) error {
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
		if err := graphics.PlotBarChartMatplotlib(names, values, graphics.MatplotlibBarOptions{
			Title:        "Programming Languages by Lines of Code",
			XLabel:       "Languages",
			YLabel:       "Lines of Code",
			Output:       outputPath,
			WidthInches:  16,
			HeightInches: 12,
			RotateX:      len(languageStats) > 10,
		}); err != nil {
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
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, fmt.Errorf("failed to create output directory %s: %v", dir, err)
			}
		}
		return []string{output}, nil
	}

	if err := os.MkdirAll(output, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %v", output, err)
	}
	return []string{
		filepath.Join(output, "languages.png"),
		filepath.Join(output, "languages.svg"),
	}, nil
}
