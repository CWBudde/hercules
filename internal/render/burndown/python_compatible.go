package burndown

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// BurndownParameters matches Python's burndown parameters structure
type BurndownParameters struct {
	Sampling    int     // Sampling interval
	Granularity int     // Granularity parameter
	TickSize    float64 // Tick size in seconds
}

// BurndownHeader matches Python's header structure: (start, last, sampling, granularity, tick)
type BurndownHeader struct {
	Start       int64   // Start timestamp
	Last        int64   // End timestamp
	Sampling    int     // Sampling interval
	Granularity int     // Granularity parameter
	TickSize    float64 // Tick size in seconds
}

// ProcessedBurndown represents the final processed burndown data ready for plotting
type ProcessedBurndown struct {
	Name         string      // Repository/entity name
	Matrix       [][]float64 // Final resampled matrix
	DateRange    []time.Time // Time series for x-axis
	Labels       []string    // Semantic labels for each band/layer
	Granularity  int         // Original granularity
	Sampling     int         // Original sampling
	ResampleMode string      // Resampling mode used
	Survival     []SurvivalPoint
}

// InterpolateBurndownMatrix converts sparse age-band data into a daily matrix with proper code persistence
// This implements burndown semantics: code persists until explicitly modified/deleted
func InterpolateBurndownMatrix(matrix [][]int, granularity, sampling int, progress bool) ([][]float64, error) {
	if len(matrix) == 0 || len(matrix[0]) == 0 {
		return [][]float64{}, fmt.Errorf("empty matrix")
	}

	rows := len(matrix)
	cols := len(matrix[0])

	// Create daily matrix: (matrix.shape[0] * granularity, matrix.shape[1] * sampling)
	dailyRows := rows * granularity
	dailyCols := cols * sampling
	daily := make([][]float64, dailyRows)
	for i := range daily {
		daily[i] = make([]float64, dailyCols)
	}

	interpolator := burndownInterpolator{
		matrix: matrix, daily: daily, granularity: granularity, sampling: sampling,
	}
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			interpolator.interpolateCell(y, x)
		}
	}
	return daily, nil
}

type burndownInterpolator struct {
	matrix      [][]int
	daily       [][]float64
	granularity int
	sampling    int
}

func (interpolator burndownInterpolator) interpolateCell(y, x int) {
	if y*interpolator.granularity > (x+1)*interpolator.sampling {
		return
	}
	bandEnd := (y + 1) * interpolator.granularity
	sampleStart := x * interpolator.sampling
	sampleEnd := (x + 1) * interpolator.sampling
	switch {
	case bandEnd >= sampleEnd:
		interpolator.interpolateGrowingBand(y, x, sampleEnd)
	case bandEnd >= sampleStart:
		interpolator.interpolateBandPeak(y, x, bandEnd)
	case x > 0:
		interpolator.decay(y, x, sampleStart, float64(interpolator.matrix[y][x-1]))
	}
}

func (interpolator burndownInterpolator) interpolateGrowingBand(y, x, sampleEnd int) {
	bandStart := y * interpolator.granularity
	sampleStart := x * interpolator.sampling
	if bandStart <= sampleStart {
		interpolator.grow(y, x, sampleEnd, float64(interpolator.matrix[y][x]))
		return
	}
	if sampleEnd <= bandStart {
		return
	}
	interpolator.grow(y, x, sampleEnd, float64(interpolator.matrix[y][x]))
	average := float64(interpolator.matrix[y][x]) / float64(sampleEnd-bandStart)
	for j := bandStart; j < sampleEnd; j++ {
		for i := bandStart; i <= j; i++ {
			interpolator.daily[i][j] = average
		}
	}
}

func (interpolator burndownInterpolator) interpolateBandPeak(y, x, bandEnd int) {
	v1 := interpolator.previousMatrixValue(y, x)
	v2 := float64(interpolator.matrix[y][x])
	previous, scale := interpolator.peakBaseline(y, x)
	delta := float64(bandEnd - x*interpolator.sampling)
	peak := v1 + (v1-previous)/scale*delta
	if v2 > peak {
		peak = interpolator.adjustedPeak(y, x, bandEnd, v2)
	}
	interpolator.grow(y, x, bandEnd, peak)
	interpolator.decay(y, x, bandEnd, peak)
}

func (interpolator burndownInterpolator) previousMatrixValue(y, x int) float64 {
	if x == 0 {
		return 0
	}
	return float64(interpolator.matrix[y][x-1])
}

func (interpolator burndownInterpolator) peakBaseline(y, x int) (float64, float64) {
	if x > 0 && (x-1)*interpolator.sampling >= y*interpolator.granularity {
		previous := 0.0
		if x > 1 {
			previous = float64(interpolator.matrix[y][x-2])
		}
		return previous, float64(interpolator.sampling)
	}
	if x == 0 {
		return 0, float64(interpolator.sampling)
	}
	return 0, float64(x*interpolator.sampling - y*interpolator.granularity)
}

func (interpolator burndownInterpolator) adjustedPeak(y, x, bandEnd int, value float64) float64 {
	if x >= len(interpolator.matrix[y])-1 {
		return value
	}
	slope := (value - float64(interpolator.matrix[y][x+1])) / float64(interpolator.sampling)
	return value + slope*float64((x+1)*interpolator.sampling-bandEnd)
}

func (interpolator burndownInterpolator) grow(y, x, finishIndex int, finishValue float64) {
	initial := interpolator.previousMatrixValue(y, x)
	startIndex := max(x*interpolator.sampling, y*interpolator.granularity)
	if finishIndex == startIndex {
		return
	}
	average := (finishValue - initial) / float64(finishIndex-startIndex)
	for j := x * interpolator.sampling; j < finishIndex; j++ {
		for i := startIndex; i <= j; i++ {
			interpolator.daily[i][j] = average
		}
	}
	for j := x * interpolator.sampling; j < finishIndex; j++ {
		for i := y * interpolator.granularity; i < x*interpolator.sampling; i++ {
			if j > 0 {
				interpolator.daily[i][j] = interpolator.daily[i][j-1]
			}
		}
	}
}

func (interpolator burndownInterpolator) decay(y, x, startIndex int, startValue float64) {
	if startValue == 0 {
		return
	}
	ratio := float64(interpolator.matrix[y][x]) / startValue
	scale := float64((x+1)*interpolator.sampling - startIndex)
	for i := y * interpolator.granularity; i < (y+1)*interpolator.granularity; i++ {
		initial := 0.0
		if startIndex > 0 {
			initial = interpolator.daily[i][startIndex-1]
		}
		for j := startIndex; j < (x+1)*interpolator.sampling; j++ {
			progress := float64(j-startIndex+1) / scale
			interpolator.daily[i][j] = initial * (1 + (ratio-1)*progress)
		}
	}
}

// FloorDateTime mimics Python's floor_datetime function
func FloorDateTime(dt time.Time, tickSize float64) time.Time {
	if tickSize <= 0 || math.IsNaN(tickSize) || math.IsInf(tickSize, 0) {
		return dt
	}
	seconds := float64(dt.Unix()) + float64(dt.Nanosecond())/float64(time.Second)
	floored := math.Floor(seconds/tickSize) * tickSize
	wholeSeconds := math.Floor(floored)
	nanoseconds := math.Round((floored - wholeSeconds) * float64(time.Second))
	return time.Unix(int64(wholeSeconds), int64(nanoseconds)).In(dt.Location())
}

// LoadBurndown is the main function that replicates Python's load_burndown
func LoadBurndown(header BurndownHeader, name string, matrix [][]int, resample string, reportSurvival, interpolationProgress bool) (*ProcessedBurndown, error) {
	if err := validateBurndownInput(header, matrix); err != nil {
		return nil, err
	}
	survival, err := loadBurndownSurvival(matrix, reportSurvival)
	if err != nil {
		return nil, err
	}
	start := FloorDateTime(time.Unix(header.Start, 0), header.TickSize)
	finish := start.Add(time.Duration(len(matrix[0])*header.Sampling) * time.Duration(header.TickSize) * time.Second)
	var finalMatrix [][]float64
	var dateRange []time.Time
	var labels []string
	if resample == "no" || resample == "raw" {
		finalMatrix, dateRange, labels = rawBurndownData(header, matrix, start)
		resample = "M"
	} else {
		finalMatrix, dateRange, labels, err = loadResampledBurndownData(
			header, matrix, start, finish, resample, interpolationProgress,
		)
		if err != nil {
			return loadBurndownFallback(header, name, matrix, resample, survival, interpolationProgress)
		}
	}
	return &ProcessedBurndown{
		Name:         name,
		Matrix:       finalMatrix,
		DateRange:    dateRange,
		Labels:       labels,
		Granularity:  header.Granularity,
		Sampling:     header.Sampling,
		ResampleMode: resample,
		Survival:     survival,
	}, nil
}

func validateBurndownInput(header BurndownHeader, matrix [][]int) error {
	if header.Sampling <= 0 || header.Granularity <= 0 {
		return fmt.Errorf("invalid sampling (%d) or granularity (%d)", header.Sampling, header.Granularity)
	}
	if header.TickSize <= 0 || math.IsNaN(header.TickSize) || math.IsInf(header.TickSize, 0) {
		return fmt.Errorf("invalid tick size %v", header.TickSize)
	}
	if len(matrix) == 0 || len(matrix[0]) == 0 {
		return fmt.Errorf("empty matrix")
	}
	return nil
}

func loadBurndownSurvival(matrix [][]int, report bool) ([]SurvivalPoint, error) {
	if report {
		survival, err := FitKaplanMeier(matrix)
		if err != nil {
			return nil, fmt.Errorf("calculate survival: %w", err)
		}
		return survival, nil
	}
	if _, err := validateSurvivalMatrix(matrix); err != nil {
		return nil, fmt.Errorf("validate burndown: %w", err)
	}
	return nil, nil
}

func rawBurndownData(
	header BurndownHeader,
	matrix [][]int,
	start time.Time,
) ([][]float64, []time.Time, []string) {
	finalMatrix := make([][]float64, len(matrix))
	labels := make([]string, len(matrix))
	for i := range matrix {
		finalMatrix[i] = make([]float64, len(matrix[i]))
		for j := range matrix[i] {
			finalMatrix[i][j] = float64(matrix[i][j])
		}
		bandStart := start.Add(time.Duration(i*header.Granularity) * time.Duration(header.TickSize) * time.Second)
		bandEnd := start.Add(time.Duration((i+1)*header.Granularity) * time.Duration(header.TickSize) * time.Second)
		labels[i] = fmt.Sprintf("%s - %s", bandStart.Format("2006-01-02"), bandEnd.Format("2006-01-02"))
	}
	dateRange := make([]time.Time, len(matrix[0]))
	for i := range dateRange {
		dateRange[i] = start.Add(time.Duration(i*header.Sampling) * time.Duration(header.TickSize) * time.Second)
	}
	return finalMatrix, dateRange, labels
}

func loadResampledBurndownData(
	header BurndownHeader,
	matrix [][]int,
	start, finish time.Time,
	resample string,
	interpolationProgress bool,
) ([][]float64, []time.Time, []string, error) {
	fmt.Printf("resampling to %s, please wait...\n", resample)
	daily, err := InterpolateBurndownMatrix(matrix, header.Granularity, header.Sampling, interpolationProgress)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("interpolation failed: %w", err)
	}
	lastDays := int(time.Unix(header.Last, 0).Sub(start) / (24 * time.Hour))
	for i := lastDays; i < len(daily); i++ {
		for j := range daily[i] {
			daily[i][j] = 0
		}
	}
	dateRange, finalMatrix, labels, err := resampleBurndownData(daily, start, finish, resample)
	return finalMatrix, dateRange, labels, err
}

func loadBurndownFallback(
	header BurndownHeader,
	name string,
	matrix [][]int,
	resample string,
	survival []SurvivalPoint,
	interpolationProgress bool,
) (*ProcessedBurndown, error) {
	_, base, err := parseResampleFreq(resample)
	fallbackModes := map[string]string{"YE": "month", "ME": "day", "W": "day"}
	fallbackMode, ok := fallbackModes[base]
	if err != nil || !ok {
		return nil, fmt.Errorf("too loose resampling: %s. Try finer", resample)
	}
	fmt.Printf("too loose resampling - by %s, trying by %s\n", resample, fallbackMode)
	fallback, fallbackErr := LoadBurndown(
		header, name, matrix, fallbackMode, false, interpolationProgress,
	)
	if fallback != nil {
		fallback.Survival = survival
	}
	return fallback, fallbackErr
}

// resampleBurndownData implements pandas-like resampling logic
func resampleBurndownData(daily [][]float64, start, finish time.Time, resample string) ([]time.Time, [][]float64, []string, error) {
	// Parse the resample frequency into a multiplier and a canonical base
	// frequency (D, W, ME, YE). This mirrors pandas offset aliases such as
	// "3M" (every third month-end), "W" (weekly) and "year"/"month"/"day".
	mult, base, err := parseResampleFreq(resample)
	if err != nil {
		return nil, nil, nil, err
	}

	dateGranularitySampling, err := pythonDateRangeUntil(start, finish, mult, base)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("unsupported resample mode: %s", resample)
	}
	if len(dateGranularitySampling) == 0 {
		return nil, nil, nil, fmt.Errorf("no valid resampling periods generated")
	}

	if dateGranularitySampling[0].After(finish) {
		return nil, nil, nil, fmt.Errorf("resampling period too loose")
	}

	samplingDays := int(finish.Sub(dateGranularitySampling[0]) / (24 * time.Hour))
	if samplingDays <= 0 {
		return nil, nil, nil, fmt.Errorf("no valid sampling range generated")
	}
	dateRangeSampling := make([]time.Time, samplingDays)
	for i := range dateRangeSampling {
		dateRangeSampling[i] = dateGranularitySampling[0].Add(time.Duration(i) * 24 * time.Hour)
	}

	resampledMatrix := fillResampledBurndownMatrix(
		daily, start, dateGranularitySampling, dateRangeSampling,
	)
	labels := burndownResampleLabels(dateGranularitySampling, base)
	return dateRangeSampling, resampledMatrix, labels, nil
}

func fillResampledBurndownMatrix(
	daily [][]float64,
	start time.Time,
	granularityDates, samplingDates []time.Time,
) [][]float64 {
	resampledMatrix := make([][]float64, len(granularityDates))
	for i := range resampledMatrix {
		resampledMatrix[i] = make([]float64, len(samplingDates))
	}
	for i, granularityDate := range granularityDates {
		var istart, ifinish int
		if i > 0 {
			istart = int(granularityDates[i-1].Sub(start) / (24 * time.Hour))
		}
		ifinish = int(granularityDate.Sub(start) / (24 * time.Hour))
		samplingStart := firstSamplingDateIndex(samplingDates, start, istart)
		for k := samplingStart; k < len(samplingDates); k++ {
			sdtDays := int(samplingDates[k].Sub(start) / (24 * time.Hour))
			if sdtDays < 0 {
				continue
			}
			resampledMatrix[i][k] = resampledBurndownValue(daily, istart, ifinish, sdtDays)
		}
	}
	return resampledMatrix
}

func resampledBurndownValue(daily [][]float64, startRow, finishRow, day int) float64 {
	var sum float64
	for row := startRow; row < finishRow && row < len(daily); row++ {
		if day < len(daily[row]) {
			sum += daily[row][day]
		}
	}
	return sum
}

func firstSamplingDateIndex(dates []time.Time, start time.Time, firstDay int) int {
	for index, date := range dates {
		if int(date.Sub(start)/(24*time.Hour)) >= firstDay {
			return index
		}
	}
	return 0
}

func burndownResampleLabels(dates []time.Time, base string) []string {
	labels := make([]string, len(dates))
	formats := map[string]string{"ME": "2006 January"}
	for i, date := range dates {
		if base == "YE" {
			labels[i] = fmt.Sprintf("%d", date.Year())
		} else if format, ok := formats[base]; ok {
			labels[i] = date.Format(format)
		} else {
			labels[i] = date.Format("2006-01-02")
		}
	}
	return labels
}

// parseResampleFreq parses a pandas-style offset alias into a multiplier and a
// canonical base frequency in {D, W, ME, YE}. It accepts word aliases
// ("year"/"month"/"week"/"day"), single-letter aliases ("A", "M", "W", "D"),
// the pandas *E variants ("YE", "ME"), quarter aliases ("Q"/"QE", mapped to 3
// months) and an optional leading integer multiplier (e.g. "3M", "2W").
func parseResampleFreq(resample string) (int, string, error) {
	s := strings.TrimSpace(resample)
	wordAliases := map[string]string{"year": "YE", "month": "ME", "week": "W", "day": "D"}
	if base, ok := wordAliases[strings.ToLower(s)]; ok {
		return 1, base, nil
	}
	mult, unitStart, err := parseResampleMultiplier(s)
	if err != nil {
		return 0, "", fmt.Errorf("unsupported resample mode: %s", resample)
	}
	unit := strings.ToUpper(s[unitStart:])
	if dash := strings.IndexByte(unit, '-'); dash >= 0 {
		unit = unit[:dash]
	}
	baseByUnit := map[string]string{
		"A": "YE", "Y": "YE", "YE": "YE", "YS": "YE", "AS": "YE",
		"M": "ME", "ME": "ME", "MS": "ME", "W": "W", "D": "D",
	}
	if unit == "Q" || unit == "QE" || unit == "QS" {
		return mult * 3, "ME", nil
	}
	base, ok := baseByUnit[unit]
	if !ok {
		return 0, "", fmt.Errorf("unsupported resample mode: %s", resample)
	}
	return mult, base, nil
}

func parseResampleMultiplier(value string) (int, int, error) {
	index := 0
	for index < len(value) && value[index] >= '0' && value[index] <= '9' {
		index++
	}
	if index == 0 {
		return 1, 0, nil
	}
	multiplier, err := strconv.Atoi(value[:index])
	if err != nil || multiplier <= 0 {
		return 0, 0, fmt.Errorf("invalid multiplier")
	}
	return multiplier, index, nil
}

func pythonDateRangeUntil(start, finish time.Time, mult int, base string) ([]time.Time, error) {
	periods := 0
	dateRange := []time.Time{start}
	for dateRange[len(dateRange)-1].Before(finish) {
		periods++
		var err error
		dateRange, err = pythonDateRange(start, periods, mult, base)
		if err != nil {
			return nil, err
		}
		if len(dateRange) == 0 {
			return nil, nil
		}
	}
	return dateRange, nil
}

func pythonDateRange(start time.Time, periods, mult int, base string) ([]time.Time, error) {
	if periods <= 0 {
		return nil, nil
	}
	if mult <= 0 {
		mult = 1
	}

	generators := map[string]func(time.Time, int, int) []time.Time{
		"YE": pythonYearEndRange,
		"ME": pythonMonthEndRange,
		"W":  pythonWeekRange,
		"D":  pythonDayRange,
	}
	generate, ok := generators[base]
	if !ok {
		return nil, fmt.Errorf("unsupported frequency %q", base)
	}
	return generate(start, periods, mult), nil
}

func pythonYearEndRange(start time.Time, periods, mult int) []time.Time {
	result := make([]time.Time, periods)
	year := start.Year()
	if yearEnd(year, start).Before(start) {
		year++
	}
	for i := range result {
		result[i] = yearEnd(year, start)
		year += mult
	}
	return result
}

func pythonMonthEndRange(start time.Time, periods, mult int) []time.Time {
	result := make([]time.Time, periods)
	year, month := start.Year(), start.Month()
	if monthEnd(year, month, start).Before(start) {
		nextMonth := time.Date(year, month, 1, 0, 0, 0, 0, start.Location()).AddDate(0, 1, 0)
		year, month = nextMonth.Year(), nextMonth.Month()
	}
	for i := range result {
		result[i] = monthEnd(year, month, start)
		nextMonth := time.Date(year, month, 1, 0, 0, 0, 0, start.Location()).AddDate(0, mult, 0)
		year, month = nextMonth.Year(), nextMonth.Month()
	}
	return result
}

func pythonWeekRange(start time.Time, periods, mult int) []time.Time {
	result := make([]time.Time, periods)
	current := nextOrSameWeekday(start, time.Sunday)
	for i := range result {
		result[i] = current
		current = current.AddDate(0, 0, 7*mult)
	}
	return result
}

func pythonDayRange(start time.Time, periods, mult int) []time.Time {
	result := make([]time.Time, periods)
	for i := range result {
		result[i] = start.AddDate(0, 0, i*mult)
	}
	return result
}

// nextOrSameWeekday returns the first time at or after t whose weekday is wd,
// preserving the clock time of t.
func nextOrSameWeekday(t time.Time, wd time.Weekday) time.Time {
	delta := (int(wd) - int(t.Weekday()) + 7) % 7
	return t.AddDate(0, 0, delta)
}

func yearEnd(year int, ref time.Time) time.Time {
	return time.Date(year, time.December, 31, ref.Hour(), ref.Minute(), ref.Second(), ref.Nanosecond(), ref.Location())
}

func monthEnd(year int, month time.Month, ref time.Time) time.Time {
	return time.Date(year, month+1, 1, ref.Hour(), ref.Minute(), ref.Second(), ref.Nanosecond(), ref.Location()).Add(-24 * time.Hour)
}
