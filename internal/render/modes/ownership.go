package modes

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
	"github.com/cwbudde/matplotlib-go/backends"
	_ "github.com/cwbudde/matplotlib-go/backends/agg"
	_ "github.com/cwbudde/matplotlib-go/backends/svg"
	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/style"
	"github.com/spf13/viper"
)

func OwnershipBurndown(reader readers.Reader, output string) error {
	// Initialize progress tracking
	quiet := viper.GetBool("quiet")
	progEstimator := progress.NewProgressEstimator(!quiet)

	// Start multi-phase operation for ownership analysis
	totalPhases := 4 // validation, data extraction, processing, visualization
	progEstimator.StartMultiOperation(totalPhases, "Ownership Burndown Analysis")

	// Phase 1: Validate output path
	progEstimator.NextOperation("Validating output path")
	if output == "" {
		output = "ownership.png"
		if !quiet {
			fmt.Printf("Output not provided, using default: %s\n", output)
		}
	}

	outputDir := filepath.Dir(output)
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to create output directory %s: %v", outputDir, err)
	}

	// Phase 2: Extract data from the reader
	progEstimator.NextOperation("Extracting ownership data")
	peopleSequence, ownershipData, err := reader.GetOwnershipBurndown()
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to get ownership burndown data: %v", err)
	}
	if len(peopleSequence) == 0 {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("no ownership burndown data found")
	}

	params, err := reader.GetBurndownParameters()
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to get burndown parameters: %v", err)
	}
	startUnix, lastUnix := reader.GetHeader()
	tickSize := params.TickSize
	if tickSize <= 0 {
		tickSize = 24 * 60 * 60
	}
	sampling := params.Sampling
	if sampling <= 0 {
		sampling = 1
	}
	startTime := floorTimeBySeconds(time.Unix(startUnix, 0), tickSize).Add(secondsDuration(float64(sampling) * tickSize))
	lastTime := time.Unix(lastUnix, 0)
	if lastUnix == 0 {
		lastTime = startTime
	}

	// Phase 3: Process the data
	progEstimator.NextOperation("Processing ownership data")
	maxPeople := viper.GetInt("max-people")
	if maxPeople <= 0 {
		maxPeople = 20
	}
	orderByTime := viper.GetBool("order-ownership-by-time")
	names, peopleMatrix, dateRange := processOwnershipBurndownWithProgress(
		startTime, lastTime, sampling, tickSize, peopleSequence, ownershipData, maxPeople, orderByTime, progEstimator,
	)

	// Phase 4: Generate output
	progEstimator.NextOperation("Generating visualization")

	// Check if JSON output is required
	if filepath.Ext(output) == ".json" {
		progEstimator.FinishMultiOperation()
		return saveOwnershipBurndownAsJSON(output, names, peopleMatrix, dateRange, lastTime)
	}

	// Visualize the data
	if err := plotOwnershipBurndown(reader.GetName(), names, peopleMatrix, dateRange, lastTime, output); err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to plot ownership burndown: %v", err)
	}

	progEstimator.FinishMultiOperation()
	if !quiet {
		fmt.Println("Ownership burndown chart generated successfully.")
	}
	return nil
}

func processOwnershipBurndown(
	start, _ time.Time, sampling int, tickSize float64,
	sequence []string, data map[string][][]int,
	maxPeople int, orderByTime bool,
) ([]string, [][]float64, []time.Time) {
	// Aggregate the ownership data
	people := make([][]float64, len(sequence))
	for i, name := range sequence {
		rows := data[name]
		if len(rows) == 0 {
			continue
		}
		total := make([]float64, len(rows))
		for rowIndex, row := range rows {
			for _, val := range row {
				total[rowIndex] += float64(val)
			}
		}
		people[i] = total
	}
	pointCount := ownershipPointCount(people)
	if pointCount == 0 {
		return sequence, people, nil
	}

	// Create a date range based on sampling
	dateRange := make([]time.Time, pointCount)
	step := ownershipSamplingDuration(sampling, tickSize)
	for i := 0; i < len(dateRange); i++ {
		dateRange[i] = start.Add(time.Duration(i) * step)
	}

	// Truncate to maxPeople
	if len(people) > maxPeople {
		sums := make([]float64, len(people))
		for i, row := range people {
			for _, val := range row {
				sums[i] += val
			}
		}

		indices := argsortDescending(sums)
		chosen := indices[:maxPeople]
		others := indices[maxPeople:]

		// Aggregate "others"
		othersTotal := make([]float64, len(people[0]))
		for _, idx := range others {
			for j, val := range people[idx] {
				othersTotal[j] += val
			}
		}

		// Update people and sequence
		truncatedPeople := make([][]float64, maxPeople+1)
		truncatedNames := make([]string, maxPeople+1)
		for i, idx := range chosen {
			truncatedPeople[i] = people[idx]
			truncatedNames[i] = sequence[idx]
		}
		truncatedPeople[maxPeople] = othersTotal
		truncatedNames[maxPeople] = "others"

		people = truncatedPeople
		sequence = truncatedNames
	}

	// Sort by first appearance or total ownership
	if orderByTime {
		appearances := make([]int, len(people))
		for i, row := range people {
			appearances[i] = findFirstNonZero(row)
		}
		indices := argsortAscending(appearances)
		people = reorder(people, indices)
		sequence = reorderStrings(sequence, indices)
	} else {
		totalOwnership := make([]float64, len(people))
		for i, row := range people {
			for _, val := range row {
				totalOwnership[i] += val
			}
		}
		indices := argsortDescending(totalOwnership)
		people = reorder(people, indices)
		sequence = reorderStrings(sequence, indices)
	}

	return sequence, people, dateRange
}

// processOwnershipBurndownWithProgress processes ownership data with progress tracking
func processOwnershipBurndownWithProgress(
	start, _ time.Time, sampling int, tickSize float64,
	sequence []string, data map[string][][]int,
	maxPeople int, orderByTime bool,
	progEstimator *progress.ProgressEstimator,
) ([]string, [][]float64, []time.Time) {
	// Start detailed progress for data processing
	totalSteps := len(sequence) + 2 // aggregation steps + sorting + date range creation
	progEstimator.StartOperation("Aggregating ownership data", totalSteps)

	// Aggregate the ownership data
	people := make([][]float64, len(sequence))
	for i, name := range sequence {
		progEstimator.UpdateProgress(1)
		rows := data[name]
		if len(rows) == 0 {
			continue
		}
		total := make([]float64, len(rows))
		for rowIndex, row := range rows {
			for _, val := range row {
				total[rowIndex] += float64(val)
			}
		}
		people[i] = total
	}
	pointCount := ownershipPointCount(people)
	if pointCount == 0 {
		progEstimator.FinishOperation()
		return sequence, people, nil
	}

	// Create a date range based on sampling
	progEstimator.UpdateProgress(1)
	dateRange := make([]time.Time, pointCount)
	step := ownershipSamplingDuration(sampling, tickSize)
	for i := 0; i < len(dateRange); i++ {
		dateRange[i] = start.Add(time.Duration(i) * step)
	}

	// Truncate to maxPeople
	if len(people) > maxPeople {
		sums := make([]float64, len(people))
		for i, row := range people {
			for _, val := range row {
				sums[i] += val
			}
		}

		indices := argsortDescending(sums)
		chosen := indices[:maxPeople]
		others := indices[maxPeople:]

		// Aggregate "others"
		othersTotal := make([]float64, len(people[0]))
		for _, idx := range others {
			for j, val := range people[idx] {
				othersTotal[j] += val
			}
		}

		// Update people and sequence
		truncatedPeople := make([][]float64, maxPeople+1)
		truncatedNames := make([]string, maxPeople+1)
		for i, idx := range chosen {
			truncatedPeople[i] = people[idx]
			truncatedNames[i] = sequence[idx]
		}
		truncatedPeople[maxPeople] = othersTotal
		truncatedNames[maxPeople] = "others"

		people = truncatedPeople
		sequence = truncatedNames
	}

	// Sort by first appearance or total ownership
	progEstimator.UpdateProgress(1)
	if orderByTime {
		appearances := make([]int, len(people))
		for i, row := range people {
			appearances[i] = findFirstNonZero(row)
		}
		indices := argsortAscending(appearances)
		people = reorder(people, indices)
		sequence = reorderStrings(sequence, indices)
	} else {
		totalOwnership := make([]float64, len(people))
		for i, row := range people {
			for _, val := range row {
				totalOwnership[i] += val
			}
		}
		indices := argsortDescending(totalOwnership)
		people = reorder(people, indices)
		sequence = reorderStrings(sequence, indices)
	}

	progEstimator.FinishOperation()
	return sequence, people, dateRange
}

func plotOwnershipBurndown(repoName string, names []string, people [][]float64, dateRange []time.Time, lastTime time.Time, output string) error {
	if len(people) == 0 || len(dateRange) == 0 {
		return fmt.Errorf("no ownership burndown data to plot")
	}
	if lastTime.Before(dateRange[len(dateRange)-1]) {
		lastTime = dateRange[len(dateRange)-1]
	}
	x := make([]float64, len(dateRange))
	for i, date := range dateRange {
		x[i] = float64(date.Unix())
	}
	matrix := make([][]float64, 0, len(people))
	labels := make([]string, 0, len(people))
	for i, row := range people {
		if len(row) == 0 {
			continue
		}
		values := make([]float64, len(dateRange))
		for j := range values {
			if j < len(row) {
				values[j] = row[j]
			}
		}
		label := fmt.Sprintf("Developer %d", i+1)
		if i < len(names) {
			label = names[i]
		}
		label = truncateOwnershipLabel(label)
		matrix = append(matrix, values)
		labels = append(labels, label)
	}
	if len(matrix) == 0 {
		return fmt.Errorf("no ownership burndown data to plot")
	}
	if viper.GetBool("relative") {
		normalizeOwnershipColumns(matrix)
	}

	width, height := ownershipPlotPixelSize(graphics.PythonPlotDefaultWidthInches, graphics.PythonPlotDefaultHeightInches)
	fontSize := graphics.PythonPlotFontSize()
	background, foreground := graphics.LaboursPlotColors(viper.GetString("background"))
	transparentBackground := background
	transparentBackground.A = 0
	legendBackground := background
	legendBackground.A = 0.8
	fig := core.NewFigure(
		width,
		height,
		style.WithTheme(style.ThemeGGPlot),
		style.WithFont(graphics.PythonPlotFontFamily, fontSize),
		style.WithBackground(background.R, background.G, background.B, 0),
		style.WithAxesBackground(transparentBackground),
		style.WithAxesEdgeColor(foreground),
		style.WithTextColor(foreground.R, foreground.G, foreground.B, foreground.A),
		style.WithLegendColors(
			legendBackground,
			legendBackground,
			foreground,
		),
	)
	grid := fig.Subplots(1, 1, core.WithSubplotPadding(0.032, 0.991, 0.033, 0.968))
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return fmt.Errorf("failed to create ownership axes")
	}
	ax := grid[0][0]
	ax.SetTitle(fmt.Sprintf("%s code ownership through time", repoName))
	colors := graphics.PythonLaboursColorPalette(len(matrix))
	renderColors := make([]render.Color, len(colors))
	for i, color := range colors {
		renderColors[i] = ownershipRenderColor(color)
	}
	ax.StackPlot(x, matrix, core.StackPlotOptions{
		Colors: renderColors,
		Labels: labels,
	})
	xMin := float64(dateRange[0].Unix())
	xMax := float64(lastTime.Unix())
	if xMin == xMax {
		xMin = float64(dateRange[0].AddDate(-2, 0, 0).Unix())
		xMax = float64(dateRange[0].AddDate(2, 0, 0).Unix())
	}
	ax.SetXLim(xMin, xMax)
	if viper.GetBool("relative") {
		ax.SetYLim(0, 1)
	} else {
		ax.SetYLim(0, math.Max(maxOwnershipStackY(matrix)*1.05, 1))
	}
	configureOwnershipTimeAxis(ax, dateRange)
	legend := ax.AddLegend()
	legend.Location = core.LegendUpperLeft
	legend.BackgroundColor = legendBackground
	legend.BorderColor = legendBackground
	legend.TextColor = foreground
	if viper.GetBool("relative") {
		legend.Location = core.LegendLowerLeft
	}

	return saveOwnershipMatplotlibFigure(fig, output, width, height, transparentBackground)
}

func ownershipSamplingDuration(sampling int, tickSize float64) time.Duration {
	if sampling <= 0 {
		sampling = 1
	}
	if tickSize <= 0 {
		tickSize = 86400
	}
	return secondsDuration(float64(sampling) * tickSize)
}

func secondsDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

func ownershipPointCount(people [][]float64) int {
	for _, row := range people {
		if len(row) > 0 {
			return len(row)
		}
	}
	return 0
}

func truncateOwnershipLabel(label string) string {
	const maxLabelLength = 40
	if len(label) <= maxLabelLength {
		return label
	}
	return label[:maxLabelLength-3] + "..."
}

func normalizeOwnershipColumns(matrix [][]float64) {
	if len(matrix) == 0 {
		return
	}
	points := len(matrix[0])
	for col := 0; col < points; col++ {
		total := 0.0
		for _, row := range matrix {
			if col < len(row) {
				total += row[col]
			}
		}
		if total == 0 {
			continue
		}
		for _, row := range matrix {
			if col < len(row) {
				row[col] /= total
			}
		}
	}
}

func floorTimeBySeconds(t time.Time, seconds float64) time.Time {
	if seconds <= 0 {
		return t
	}
	step := int64(seconds)
	if step <= 0 {
		return t
	}
	return time.Unix((t.Unix()/step)*step, 0)
}

func configureOwnershipTimeAxis(ax *core.Axes, dates []time.Time) {
	if len(dates) == 0 {
		return
	}
	limit := 8
	step := int(math.Ceil(float64(len(dates)) / float64(limit)))
	if step < 1 {
		step = 1
	}
	ticks := make([]float64, 0, limit+1)
	labels := make([]string, 0, limit+1)
	for i := 0; i < len(dates); i += step {
		ticks = append(ticks, float64(dates[i].Unix()))
		labels = append(labels, dates[i].Format("2006-01-02"))
	}
	lastTick := float64(dates[len(dates)-1].Unix())
	if len(ticks) == 0 || ticks[len(ticks)-1] != lastTick {
		ticks = append(ticks, lastTick)
		labels = append(labels, dates[len(dates)-1].Format("2006-01-02"))
	}
	ax.XAxis.Locator = core.FixedLocator{TicksList: ticks}
	ax.XAxis.Formatter = core.FixedFormatter{Labels: labels}
	if len(labels) > 6 {
		ax.XAxis.MajorLabelStyle = core.TickLabelStyle{Rotation: 30, AutoAlign: true}
	}
}

func maxOwnershipStackY(matrix [][]float64) float64 {
	if len(matrix) == 0 {
		return 0
	}
	points := len(matrix[0])
	maxY := 0.0
	for i := 0; i < points; i++ {
		total := 0.0
		for _, row := range matrix {
			if i < len(row) {
				total += row[i]
			}
		}
		if total > maxY {
			maxY = total
		}
	}
	return maxY
}

func ownershipRenderColor(c color.Color) render.Color {
	r, g, b, a := c.RGBA()
	return render.Color{
		R: float64(r) / 0xffff,
		G: float64(g) / 0xffff,
		B: float64(b) / 0xffff,
		A: float64(a) / 0xffff,
	}
}

func ownershipPlotPixelSize(defaultWidth, defaultHeight float64) (int, int) {
	width := defaultWidth
	height := defaultHeight
	if sizeStr := viper.GetString("size"); sizeStr != "" {
		if parsedWidth, parsedHeight, err := parseOwnershipPlotSize(sizeStr); err == nil {
			width, height = parsedWidth, parsedHeight
		} else {
			fmt.Printf("Warning: %v, using default size\n", err)
		}
	}
	return graphics.InchesToPixels(width), graphics.InchesToPixels(height)
}

func parseOwnershipPlotSize(sizeStr string) (float64, float64, error) {
	parts := strings.Split(sizeStr, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid size %q: expected width,height", sizeStr)
	}
	width, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil || width <= 0 {
		return 0, 0, fmt.Errorf("invalid plot width %q", parts[0])
	}
	height, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil || height <= 0 {
		return 0, 0, fmt.Errorf("invalid plot height %q", parts[1])
	}
	return width, height, nil
}

func saveOwnershipMatplotlibFigure(fig *core.Figure, output string, width, height int, background render.Color) error {
	if output == "" {
		output = "ownership.png"
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil {
		return fmt.Errorf("failed to create output directory for %s: %v", output, err)
	}

	config := backends.Config{Width: width, Height: height, Background: background, DPI: 100, Transparent: background.A == 0}
	switch strings.ToLower(filepath.Ext(output)) {
	case ".svg":
		renderer, _, err := backends.NewRenderer("svg", config, nil)
		if err != nil {
			return fmt.Errorf("failed to create SVG renderer: %v", err)
		}
		return core.SaveSVG(fig, renderer, output)
	default:
		renderer, _, err := backends.NewRenderer("agg", config, backends.TextCapabilities)
		if err != nil {
			return fmt.Errorf("failed to create AGG renderer: %v", err)
		}
		if err := core.SavePNG(fig, renderer, output); err != nil {
			return err
		}
		if background.A == 0 && background.R == 1 && background.G == 1 && background.B == 1 {
			return normalizeOwnershipPNGMatte(output)
		}
		return nil
	}
}

func normalizeOwnershipPNGMatte(path string) error {
	file, err := os.Open(path) // #nosec G304 - path is generated by ownership plotting.
	if err != nil {
		return fmt.Errorf("failed to open ownership PNG for matte normalization: %v", err)
	}
	img, _, err := image.Decode(file)
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("failed to decode ownership PNG for matte normalization: %v", err)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close ownership PNG for matte normalization: %v", closeErr)
	}

	bounds := img.Bounds()
	out := image.NewNRGBA(bounds)
	changed := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if pixel.A == 0 {
				if pixel.R != 255 || pixel.G != 255 || pixel.B != 255 {
					pixel.R = 255
					pixel.G = 255
					pixel.B = 255
					changed = true
				}
			} else if pixel.A == 204 && similarRGB(pixel.R, pixel.G, pixel.B) && pixel.R < 128 {
				pixel.R = 255
				pixel.G = 255
				pixel.B = 255
				changed = true
			}
			out.SetNRGBA(x, y, pixel)
		}
	}
	if !changed {
		return nil
	}

	file, err = os.Create(path) // #nosec G304 - path is generated by ownership plotting.
	if err != nil {
		return fmt.Errorf("failed to rewrite ownership PNG matte: %v", err)
	}
	if err := png.Encode(file, out); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close ownership PNG matte: %v", err)
	}
	return nil
}

func similarRGB(r, g, b uint8) bool {
	return absDiffUint8(r, g) <= 1 && absDiffUint8(g, b) <= 1
}

func absDiffUint8(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

func saveOwnershipBurndownAsJSON(output string, names []string, people [][]float64, dateRange []time.Time, lastTime time.Time) error {
	data := struct {
		Type      string      `json:"type"`
		Names     []string    `json:"names"`
		People    [][]float64 `json:"people"`
		DateRange []time.Time `json:"date_range"`
		Last      time.Time   `json:"last"`
	}{
		Type:      "ownership",
		Names:     names,
		People:    people,
		DateRange: dateRange,
		Last:      lastTime,
	}

	file, err := os.Create(output) // #nosec G304 - output path is explicitly requested by caller.
	if err != nil {
		return fmt.Errorf("failed to create JSON output file: %v", err)
	}
	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("failed to write JSON data: %v", err)
	}

	fmt.Printf("JSON data saved to %s\n", output)
	return nil
}

func argsortDescending(data []float64) []int {
	indices := make([]int, len(data))
	for i := range indices {
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool {
		return data[indices[i]] > data[indices[j]]
	})
	return indices
}

func argsortAscending(data []int) []int {
	indices := make([]int, len(data))
	for i := range indices {
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool {
		return data[indices[i]] < data[indices[j]]
	})
	return indices
}

func findFirstNonZero(row []float64) int {
	for i, val := range row {
		if val > 0 {
			return i
		}
	}
	return math.MaxInt
}

func reorder(data [][]float64, indices []int) [][]float64 {
	reordered := make([][]float64, len(indices))
	for i, idx := range indices {
		reordered[i] = data[idx]
	}
	return reordered
}

func reorderStrings(data []string, indices []int) []string {
	reordered := make([]string, len(indices))
	for i, idx := range indices {
		reordered[i] = data[idx]
	}
	return reordered
}
