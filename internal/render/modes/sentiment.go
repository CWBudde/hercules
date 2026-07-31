package modes

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// experimentalSentimentSubtitle is shown beneath every sentiment chart title to
// flag that this analysis is experimental (it depends on a TensorFlow build and
// its scores may be inaccurate).
const experimentalSentimentSubtitle = "[EXPERIMENTAL] Sentiment analysis is experimental and may be inaccurate."

const heuristicSentimentNotice = "Collected sentiment data is missing; using heuristic fallback " +
	"because --sentiment-fallback is enabled."

// SentimentResult represents sentiment analysis for a developer or file.
type SentimentResult struct {
	Entity   string
	Type     string // "developer" or "language"
	Positive float64
	Neutral  float64
	Negative float64
	Score    float64 // Overall sentiment score (-1 to 1)
}

// Sentiment generates sentiment analysis charts from collected sentiment data.
// When allowHeuristicFallback is true, legacy developer/language heuristics are used
// for old fixtures that do not contain CommentSentimentResults.
func Sentiment(reader readers.Reader, output string, allowHeuristicFallback bool) error {
	opts := defaultOptions()
	opts.SentimentFallback = allowHeuristicFallback

	return SentimentWithOptions(reader, output, opts)
}

func SentimentWithOptions(reader readers.Reader, output string, opts Options) error {
	fmt.Println("Analyzing repository sentiment patterns...")

	sentimentResults, collectedTicks, err := loadSentimentData(reader, opts.SentimentFallback)
	if err != nil {
		return err
	}

	err = plotSentimentResults(reader, sentimentResults, collectedTicks, output, opts.Graphics)
	if err != nil {
		return err
	}

	printSentimentSummary(sentimentResults)
	_, _ = fmt.Fprintf(os.Stdout, "Sentiment analysis completed. Analyzed %d entities.\n", len(sentimentResults))

	return nil
}

func loadSentimentData(
	reader readers.Reader,
	allowHeuristicFallback bool,
) ([]SentimentResult, map[int]readers.SentimentTick, error) {
	results, ticks, err := collectedSentimentData(reader)
	if err != nil {
		return nil, nil, err
	}

	if len(results) > 0 {
		return results, ticks, nil
	}

	if !allowHeuristicFallback {
		return nil, nil, fmt.Errorf("%w: Sentiment", readers.ErrAnalysisMissing)
	}

	_, _ = fmt.Fprintln(os.Stdout, heuristicSentimentNotice)

	results, err = heuristicSentimentData(reader)
	if err != nil {
		return nil, nil, err
	}

	if len(results) == 0 {
		return nil, nil, fmt.Errorf("%w: sentiment fallback inputs", readers.ErrAnalysisMissing)
	}

	return results, nil, nil
}

func collectedSentimentData(
	reader readers.Reader,
) ([]SentimentResult, map[int]readers.SentimentTick, error) {
	sentimentReader, ok := reader.(readers.SentimentReader)
	if !ok {
		return nil, nil, nil
	}

	ticks, err := sentimentReader.GetSentimentByTick()
	if errors.Is(err, readers.ErrAnalysisMissing) {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, fmt.Errorf("could not read collected sentiment data: %w", err)
	}

	if len(ticks) == 0 {
		return nil, nil, nil
	}

	return sentimentResultsFromTicks(ticks), ticks, nil
}

func heuristicSentimentData(reader readers.Reader) ([]SentimentResult, error) {
	var results []SentimentResult
	var failures []error

	developers, err := analyzeDeveloperSentiment(reader)
	results, failures = addSentimentFallback(results, failures, "developer", developers, err)

	languages, err := analyzeLanguageSentiment(reader)
	results, failures = addSentimentFallback(results, failures, "language", languages, err)

	joinedErr := errors.Join(failures...)
	if joinedErr != nil {
		return nil, fmt.Errorf("sentiment fallback failed: %w", joinedErr)
	}

	return results, nil
}

func addSentimentFallback(
	results []SentimentResult,
	failures []error,
	name string,
	fallback []SentimentResult,
	err error,
) ([]SentimentResult, []error) {
	if err == nil {
		return append(results, fallback...), failures
	}

	if errors.Is(err, readers.ErrAnalysisMissing) {
		fmt.Fprintf(os.Stderr, "Warning: Could not analyze %s sentiment: %v\n", name, err)
		return results, failures
	}

	return results, append(failures, err)
}

func plotSentimentResults(
	reader readers.Reader,
	results []SentimentResult,
	ticks map[int]readers.SentimentTick,
	output string,
	visuals graphics.Options,
) error {
	if len(ticks) > 0 {
		start, _ := reader.GetHeader()

		err := plotCollectedSentimentTimelineWithOptions(titleRepositoryName(reader.GetName()), start, ticks, output, visuals)
		if err != nil {
			return fmt.Errorf("failed to generate collected sentiment timeline: %w", err)
		}

		return nil
	}

	err := plotSentimentOverviewWithOptions(results, output, visuals)
	if err != nil {
		return fmt.Errorf("failed to generate sentiment overview: %w", err)
	}

	err = plotSentimentByTypeWithOptions(results, output, visuals)
	if err != nil {
		return fmt.Errorf("failed to plot sentiment by type: %w", err)
	}

	return nil
}

func sentimentResultsFromTicks(ticks map[int]readers.SentimentTick) []SentimentResult {
	keys := make([]int, 0, len(ticks))
	for tick := range ticks {
		keys = append(keys, tick)
	}

	sort.Ints(keys)

	results := make([]SentimentResult, 0, len(keys))
	for _, tick := range keys {
		value := float64(ticks[tick].Value)
		if !isFinite(value) {
			continue
		}
		// Hercules stores the raw BiDiSentiment value in the historical Python
		// convention: values below 0.5 are positive, values above 0.5 are negative.
		score := clamp((0.5-value)*2, -1, 1)
		positive := 0.0
		negative := 0.0

		if score > 0 {
			positive = score
		} else if score < 0 {
			negative = -score
		}

		results = append(results, normalizeSentimentResult(SentimentResult{
			Entity:   fmt.Sprintf("tick %d", tick),
			Type:     "tick",
			Positive: positive,
			Neutral:  1 - math.Abs(score),
			Negative: negative,
			Score:    score,
		}))
	}

	return results
}

func plotCollectedSentimentTimeline(name string, startUnix int64, ticks map[int]readers.SentimentTick, output string) error {
	return plotCollectedSentimentTimelineWithOptions(name, startUnix, ticks, output, graphics.DefaultOptions())
}

func plotCollectedSentimentTimelineWithOptions(
	name string,
	startUnix int64,
	ticks map[int]readers.SentimentTick,
	output string,
	visuals graphics.Options,
) error {
	if len(ticks) == 0 {
		return errors.New("no collected sentiment ticks")
	}

	output, err := prepareSentimentOutput(output)
	if err != nil {
		return err
	}

	dates, mood := collectedSentimentTimeline(startUnix, ticks)
	smoothed := movingAverage(mood, max(1, len(mood)/32))
	positive, negative := splitSentimentMood(smoothed)
	series := collectedSentimentSeries(positive, negative)
	opts := collectedSentimentOptions(name, visuals)

	pngFile, svgFile, err := plotCollectedSentimentFiles(dates, series, opts, output)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "Sentiment overview charts saved to %s and %s\n", pngFile, svgFile)

	return nil
}

func prepareSentimentOutput(output string) (string, error) {
	if output == "" {
		output = "."
	}

	err := os.MkdirAll(output, 0o750)
	if err != nil {
		return "", fmt.Errorf("failed to create output directory %s: %w", output, err)
	}

	return output, nil
}

func collectedSentimentTimeline(
	startUnix int64,
	ticks map[int]readers.SentimentTick,
) ([]time.Time, []float64) {
	mood := make([]float64, lastSentimentTick(ticks)+1)
	for tick, value := range ticks {
		if tick >= 0 && tick < len(mood) {
			mood[tick] = clamp((0.5-float64(value.Value))*2, -1, 1)
		}
	}

	start := time.Unix(startUnix, 0)

	dates := make([]time.Time, len(mood))
	for day := range dates {
		dates[day] = start.AddDate(0, 0, day)
	}

	return dates, mood
}

func lastSentimentTick(ticks map[int]readers.SentimentTick) int {
	last := 0
	for tick := range ticks {
		if tick > last {
			last = tick
		}
	}

	return last
}

func splitSentimentMood(mood []float64) ([]float64, []float64) {
	positive := make([]float64, len(mood))

	negative := make([]float64, len(mood))
	for i, value := range mood {
		if value > 0 {
			positive[i] = value
		} else {
			negative[i] = value
		}
	}

	return positive, negative
}

func collectedSentimentSeries(
	positive, negative []float64,
) []graphics.MatplotlibTimeAreaSeries {
	return []graphics.MatplotlibTimeAreaSeries{
		{Label: "Positive", Values: positive, Color: sentimentColor(0x8d, 0xb8, 0x43)},
		{Label: "Negative", Values: negative, Color: sentimentColor(0xe1, 0x4c, 0x35)},
	}
}

func collectedSentimentOptions(name string, visuals graphics.Options) graphics.MatplotlibTimeAreaOptions {
	titleName := strings.TrimSpace(name)
	if titleName == "" {
		titleName = "Repository"
	}

	return graphics.MatplotlibTimeAreaOptions{
		Title:        titleName + " sentiment",
		Subtitle:     experimentalSentimentSubtitle,
		XLabel:       "Time",
		YLabel:       "Comment sentiment",
		WidthInches:  16,
		HeightInches: 12,
		Stacked:      false,
		ShowGrid:     true,
		Legend:       true,
		LegendTop:    true,
		Alpha:        1,
		YMin:         -1,
		YMax:         1,
		FontSize:     visuals.PlotFontSize(),
	}
}

func plotCollectedSentimentFiles(
	dates []time.Time,
	series []graphics.MatplotlibTimeAreaSeries,
	opts graphics.MatplotlibTimeAreaOptions,
	output string,
) (string, string, error) {
	pngFile := filepath.Join(output, "sentiment-overview.png")
	opts.Output = pngFile

	err := graphics.PlotTimeAreasMatplotlib(dates, series, opts)
	if err != nil {
		return "", "", fmt.Errorf("plot collected sentiment PNG: %w", err)
	}

	svgFile := filepath.Join(output, "sentiment-overview.svg")
	opts.Output = svgFile

	err = graphics.PlotTimeAreasMatplotlib(dates, series, opts)
	if err != nil {
		return "", "", fmt.Errorf("plot collected sentiment SVG: %w", err)
	}

	return pngFile, svgFile, nil
}

func movingAverage(values []float64, window int) []float64 {
	if window <= 1 || len(values) == 0 {
		return append([]float64(nil), values...)
	}

	result := make([]float64, len(values))

	half := window / 2
	for i := range values {
		start := max(i-half, 0)

		end := min(i+half+1, len(values))

		sum := 0.0
		for _, value := range values[start:end] {
			sum += value
		}

		result[i] = sum / float64(end-start)
	}

	return result
}

func sentimentColor(r, g, b uint8) color.Color {
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

// analyzeDeveloperSentiment analyzes sentiment based on developer activity patterns.
func analyzeDeveloperSentiment(reader readers.Reader) ([]SentimentResult, error) {
	devStats, err := reader.GetDeveloperStats()
	if err != nil {
		return nil, fmt.Errorf("get developer statistics: %w", err)
	}

	if len(devStats) == 0 {
		return nil, fmt.Errorf("%w: developer statistics", readers.ErrAnalysisMissing)
	}

	var results []SentimentResult

	// Calculate total metrics for normalization
	var totalCommits, totalAdded, totalRemoved int
	for _, dev := range devStats {
		totalCommits += dev.Commits
		totalAdded += dev.LinesAdded
		totalRemoved += dev.LinesRemoved
	}

	_ = totalAdded
	_ = totalRemoved

	if totalCommits <= 0 {
		totalCommits = 1
	}

	for _, dev := range devStats {
		// Calculate activity ratios
		commitRatio := float64(dev.Commits) / float64(totalCommits)
		changeRatio := float64(dev.LinesAdded) / float64(dev.LinesAdded+dev.LinesRemoved+1)

		// Sentiment heuristics based on activity patterns:
		// - High commit activity = positive engagement
		// - Balanced add/remove ratio = positive code quality focus
		// - Many languages = positive exploration/learning
		// - Many files touched = positive collaboration

		positive := commitRatio*0.3 + changeRatio*0.3 +
			float64(len(dev.Languages))/10.0*0.2 +
			float64(dev.FilesTouched)/100.0*0.2

		// Factors that might indicate negative sentiment:
		// - Very high removal ratio (frustration/cleanup)
		// - Very low commit activity (disengagement)
		negative := 0.0
		if dev.LinesRemoved > dev.LinesAdded*2 {
			negative += 0.3 // High removal ratio
		}

		if commitRatio < 0.01 && len(devStats) > 5 {
			negative += 0.2 // Very low activity
		}

		neutral := 1.0 - positive - negative
		if neutral < 0 {
			// Normalize if we went over 1.0
			total := positive + negative
			positive /= total
			negative /= total
			neutral = 0.0
		}

		score := positive - negative // Overall sentiment score

		results = append(results, normalizeSentimentResult(SentimentResult{
			Entity:   dev.Name,
			Type:     "developer",
			Positive: positive,
			Neutral:  neutral,
			Negative: negative,
			Score:    score,
		}))
	}

	return results, nil
}

// analyzeLanguageSentiment analyzes sentiment based on language usage patterns.
func analyzeLanguageSentiment(reader readers.Reader) ([]SentimentResult, error) {
	langStats, err := reader.GetLanguageStats()
	if err != nil {
		return nil, fmt.Errorf("get language statistics: %w", err)
	}

	if len(langStats) == 0 {
		return nil, fmt.Errorf("%w: language statistics", readers.ErrAnalysisMissing)
	}

	var results []SentimentResult

	// Calculate total lines for normalization
	totalLines := 0
	for _, lang := range langStats {
		totalLines += lang.Lines
	}

	if totalLines <= 0 {
		return nil, fmt.Errorf("%w: non-zero language statistics", readers.ErrAnalysisMissing)
	}

	// Language sentiment heuristics based on common perceptions and usage patterns
	languageSentimentMap := map[string]float64{
		"Go":         0.7,  // Modern, loved language
		"Python":     0.6,  // Popular, versatile
		"JavaScript": 0.2,  // Mixed feelings, necessary evil
		"TypeScript": 0.5,  // Improvement over JS
		"Rust":       0.8,  // Loved by developers
		"Java":       0.1,  // Corporate, verbose
		"C++":        0.0,  // Complex, powerful but hard
		"C":          0.3,  // Respected but challenging
		"Kotlin":     0.6,  // Modern improvement over Java
		"Swift":      0.5,  // Apple ecosystem
		"PHP":        -0.2, // Often criticized
		"Ruby":       0.4,  // Elegant but less popular
		"Shell":      0.1,  // Necessary but limited
		"HTML":       0.2,  // Markup, not programming
		"CSS":        0.1,  // Styling challenges
		"SQL":        0.3,  // Necessary data tool
		"Dockerfile": 0.4,  // Modern deployment
		"YAML":       0.2,  // Configuration
		"JSON":       0.3,  // Data format
		"Markdown":   0.5,  // Documentation friendly
	}

	for _, lang := range langStats {
		usage := float64(lang.Lines) / float64(totalLines)

		// Get base sentiment for this language (default neutral)
		baseSentiment := languageSentimentMap[lang.Language]
		if _, exists := languageSentimentMap[lang.Language]; !exists {
			baseSentiment = 0.0 // Neutral for unknown languages
		}

		// Weight sentiment by usage - more used languages have more impact
		weightedSentiment := baseSentiment * usage * 2.0 // Amplify for visibility

		var positive, negative, neutral float64
		if weightedSentiment > 0 {
			positive = weightedSentiment
			negative = 0.0
		} else if weightedSentiment < 0 {
			positive = 0.0
			negative = -weightedSentiment
		}

		neutral = 1.0 - positive - negative
		if neutral < 0 {
			neutral = 0.0
		}

		results = append(results, normalizeSentimentResult(SentimentResult{
			Entity:   lang.Language,
			Type:     "language",
			Positive: positive,
			Neutral:  neutral,
			Negative: negative,
			Score:    weightedSentiment,
		}))
	}

	return results, nil
}

func normalizeSentimentResult(result SentimentResult) SentimentResult {
	result.Positive = nonNegativeFinite(result.Positive)
	result.Neutral = nonNegativeFinite(result.Neutral)

	result.Negative = nonNegativeFinite(result.Negative)
	if !isFinite(result.Score) {
		result.Score = 0
	}

	total := result.Positive + result.Neutral + result.Negative
	switch {
	case total == 0:
		result.Neutral = 1
	case total > 1:
		result.Positive /= total
		result.Neutral /= total
		result.Negative /= total
	}

	return result
}

func nonNegativeFinite(value float64) float64 {
	if !isFinite(value) || value < 0 {
		return 0
	}

	return value
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func clamp(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}

	if value > maxValue {
		return maxValue
	}

	return value
}

// plotSentimentOverview creates a stacked bar chart showing overall sentiment distribution.
func plotSentimentOverview(results []SentimentResult, output string) error {
	return plotSentimentOverviewWithOptions(results, output, graphics.DefaultOptions())
}

func plotSentimentOverviewWithOptions(results []SentimentResult, output string, visuals graphics.Options) error {
	// Sort by sentiment score (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	entities := make([]string, len(results))
	positiveVals := make([]float64, len(results))
	neutralVals := make([]float64, len(results))
	negativeVals := make([]float64, len(results))

	for i, result := range results {
		result = normalizeSentimentResult(result)
		entities[i] = fmt.Sprintf("%s (%s)", result.Entity, result.Type)
		positiveVals[i] = result.Positive
		neutralVals[i] = result.Neutral
		negativeVals[i] = result.Negative
	}

	palette := visuals.Palette()
	series := []graphics.MatplotlibGroupedBarSeries{
		{Name: "Positive", Values: positiveVals, Color: palette[2%len(palette)]},
		{Name: "Neutral", Values: neutralVals, Color: palette[7%len(palette)]},
		{Name: "Negative", Values: negativeVals, Color: palette[3%len(palette)]},
	}
	opts := graphics.MatplotlibGroupedBarOptions{
		Title:        "Repository Sentiment Analysis Overview",
		Subtitle:     experimentalSentimentSubtitle,
		XLabel:       "Entities (Developers & Languages)",
		YLabel:       "Sentiment Distribution",
		WidthInches:  16,
		HeightInches: 12,
		RotateX:      len(results) > 8,
		FontSize:     visuals.PlotFontSize(),
	}

	outputFile := filepath.Join(output, "sentiment-overview.png")

	opts.Output = outputFile

	err := graphics.PlotStackedBarChartMatplotlib(entities, series, opts)
	if err != nil {
		return fmt.Errorf("failed to save sentiment overview: %w", err)
	}

	svgFile := filepath.Join(output, "sentiment-overview.svg")

	opts.Output = svgFile

	err = graphics.PlotStackedBarChartMatplotlib(entities, series, opts)
	if err != nil {
		return fmt.Errorf("failed to save sentiment overview SVG: %w", err)
	}

	fmt.Printf("Sentiment overview charts saved to %s and %s\n", outputFile, svgFile)

	return nil
}

// plotSentimentByType creates separate charts for developers and languages.
func plotSentimentByType(results []SentimentResult, output string) error {
	return plotSentimentByTypeWithOptions(results, output, graphics.DefaultOptions())
}

func plotSentimentByTypeWithOptions(results []SentimentResult, output string, visuals graphics.Options) error {
	// Separate by type
	var developers, languages []SentimentResult

	for _, result := range results {
		switch result.Type {
		case "developer":
			developers = append(developers, result)
		case "language":
			languages = append(languages, result)
		}
	}

	// Plot developer sentiment if available
	if len(developers) > 0 {
		err := plotSentimentForTypeWithOptions(developers, "Developer Sentiment Analysis", output, "sentiment-developers", visuals)
		if err != nil {
			return fmt.Errorf("failed to plot developer sentiment: %w", err)
		}
	}

	// Plot language sentiment if available
	if len(languages) > 0 {
		err := plotSentimentForTypeWithOptions(languages, "Language Sentiment Analysis", output, "sentiment-languages", visuals)
		if err != nil {
			return fmt.Errorf("failed to plot language sentiment: %w", err)
		}
	}

	return nil
}

// plotSentimentForType creates a sentiment chart for a specific type.
func plotSentimentForType(results []SentimentResult, title, output, filename string) error {
	return plotSentimentForTypeWithOptions(results, title, output, filename, graphics.DefaultOptions())
}

func plotSentimentForTypeWithOptions(
	results []SentimentResult,
	title, output, filename string,
	visuals graphics.Options,
) error {
	// Sort by sentiment score
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	points := make([]graphics.MatplotlibScatterPoint, len(results))

	names := make([]string, len(results))
	for i, result := range results {
		result = normalizeSentimentResult(result)
		points[i] = graphics.MatplotlibScatterPoint{X: float64(i), Y: result.Score}
		names[i] = result.Entity
	}

	palette := visuals.Palette()
	series := []graphics.MatplotlibScatterSeries{
		{Name: "Sentiment score", Points: points, Color: palette[0], Size: 32},
	}
	opts := graphics.MatplotlibScatterOptions{
		Title:        title,
		Subtitle:     experimentalSentimentSubtitle,
		XLabel:       "Entities",
		YLabel:       "Sentiment Score",
		WidthInches:  14,
		HeightInches: 8,
		ShowGrid:     true,
		ZeroLine:     true,
		XTickLabels:  names,
		RotateX:      len(results) > 6,
		FontSize:     visuals.PlotFontSize(),
	}

	outputFile := filepath.Join(output, filename+".png")

	opts.Output = outputFile

	err := graphics.PlotScatterMatplotlib(series, opts)
	if err != nil {
		return fmt.Errorf("failed to save %s: %w", filename, err)
	}

	svgFile := filepath.Join(output, filename+".svg")

	opts.Output = svgFile

	err = graphics.PlotScatterMatplotlib(series, opts)
	if err != nil {
		return fmt.Errorf("failed to save %s SVG: %w", filename, err)
	}

	fmt.Printf("%s charts saved to %s and %s\n", title, outputFile, svgFile)

	return nil
}

// printSentimentSummary prints a text summary of sentiment analysis.
func printSentimentSummary(results []SentimentResult) {
	fmt.Println("\n[EXPERIMENTAL] Sentiment Analysis Summary:")
	fmt.Println("==========================================")

	// Calculate overall statistics
	var totalPositive, totalNeutral, totalNegative float64
	var devCount, langCount int

	for _, result := range results {
		result = normalizeSentimentResult(result)
		totalPositive += result.Positive
		totalNeutral += result.Neutral
		totalNegative += result.Negative

		if result.Type == "developer" {
			devCount++
		} else {
			langCount++
		}
	}

	avgPositive := totalPositive / float64(len(results))
	avgNeutral := totalNeutral / float64(len(results))
	avgNegative := totalNegative / float64(len(results))

	fmt.Printf("Overall Sentiment Distribution:\n")
	fmt.Printf("  Positive: %.1f%%\n", avgPositive*100)
	fmt.Printf("  Neutral:  %.1f%%\n", avgNeutral*100)
	fmt.Printf("  Negative: %.1f%%\n", avgNegative*100)
	fmt.Printf("\nAnalyzed: %d developers, %d languages\n", devCount, langCount)

	// Show top positive and negative entities
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	fmt.Println("\nMost Positive Entities:")

	for i, result := range results {
		if i >= 5 || result.Score <= 0 {
			break
		}

		fmt.Printf("  %d. %s (%s) - Score: %.3f\n", i+1, result.Entity, result.Type, result.Score)
	}

	fmt.Println("\nMost Negative Entities:")
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score < results[j].Score
	})

	for i, result := range results {
		if i >= 5 || result.Score >= 0 {
			break
		}

		fmt.Printf("  %d. %s (%s) - Score: %.3f\n", i+1, result.Entity, result.Type, result.Score)
	}
}
