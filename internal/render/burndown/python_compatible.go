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

	// Restore the original complex Python interpolation algorithm that creates smooth curves
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			// Skip if the future is zeros: y * granularity > (x + 1) * sampling
			if y*granularity > (x+1)*sampling {
				continue
			}

			// Define nested decay function (creates smooth exponential decay curves)
			decay := func(startIndex int, startVal float64) {
				if startVal == 0 {
					return
				}
				k := float64(matrix[y][x]) / startVal // k <= 1, creates decay rate
				scale := float64((x+1)*sampling - startIndex)

				for i := y * granularity; i < (y+1)*granularity; i++ {
					var initial float64
					if startIndex > 0 {
						initial = daily[i][startIndex-1]
					}
					// Create smooth exponential-like curves between points
					for j := startIndex; j < (x+1)*sampling; j++ {
						progress := float64(j-startIndex+1) / scale
						daily[i][j] = initial * (1 + (k-1)*progress)
					}
				}
			}

			// Define nested grow function (creates smooth growth curves)
			grow := func(finishIndex int, finishVal float64) {
				var initial float64
				if x > 0 {
					initial = float64(matrix[y][x-1])
				}
				startIndex := x * sampling
				if startIndex < y*granularity {
					startIndex = y * granularity
				}
				if finishIndex == startIndex {
					return
				}
				// Average slope creates smooth linear growth
				avg := (finishVal - initial) / float64(finishIndex-startIndex)

				// Fill triangular region with smooth interpolation
				for j := x * sampling; j < finishIndex; j++ {
					for i := startIndex; i <= j; i++ {
						daily[i][j] = avg
					}
				}
				// Copy values to create smooth persistence
				for j := x * sampling; j < finishIndex; j++ {
					for i := y * granularity; i < x*sampling; i++ {
						if j > 0 {
							daily[i][j] = daily[i][j-1]
						}
					}
				}
			}

			// Main interpolation logic with complex conditional structure for smooth curves
			switch {
			case (y+1)*granularity >= (x+1)*sampling:
				// Case: Current age band extends beyond current time sampling
				if y*granularity <= x*sampling {
					grow((x+1)*sampling, float64(matrix[y][x]))
				} else if (x+1)*sampling > y*granularity {
					grow((x+1)*sampling, float64(matrix[y][x]))
					// Smooth fill for overlapping region
					avg := float64(matrix[y][x]) / float64((x+1)*sampling-y*granularity)
					for j := y * granularity; j < (x+1)*sampling; j++ {
						for i := y * granularity; i <= j; i++ {
							daily[i][j] = avg
						}
					}
				}
			case (y+1)*granularity >= x*sampling:
				// Complex peak calculation case for smooth curves
				var v1, v2 float64
				if x > 0 {
					v1 = float64(matrix[y][x-1])
				}
				v2 = float64(matrix[y][x])
				delta := float64((y+1)*granularity - x*sampling)

				var previous float64
				var scale float64
				if x > 0 && (x-1)*sampling >= y*granularity {
					if x > 1 {
						previous = float64(matrix[y][x-2])
					}
					scale = float64(sampling)
				} else {
					if x == 0 {
						scale = float64(sampling)
					} else {
						scale = float64(x*sampling - y*granularity)
					}
				}

				// Calculate peak with smooth interpolation
				peak := v1 + (v1-previous)/scale*delta
				if v2 > peak {
					if x < cols-1 {
						k := (v2 - float64(matrix[y][x+1])) / float64(sampling)
						peak = float64(matrix[y][x]) + k*float64((x+1)*sampling-(y+1)*granularity)
					} else {
						peak = v2
					}
				}
				grow((y+1)*granularity, peak)
				decay((y+1)*granularity, peak)
			case x > 0:
				// Case: Age band is completely in the past
				decay(x*sampling, float64(matrix[y][x-1]))
			}
		}
	}

	return daily, nil
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
	if header.Sampling <= 0 || header.Granularity <= 0 {
		return nil, fmt.Errorf("invalid sampling (%d) or granularity (%d)", header.Sampling, header.Granularity)
	}
	if header.TickSize <= 0 || math.IsNaN(header.TickSize) || math.IsInf(header.TickSize, 0) {
		return nil, fmt.Errorf("invalid tick size %v", header.TickSize)
	}
	if len(matrix) == 0 || len(matrix[0]) == 0 {
		return nil, fmt.Errorf("empty matrix")
	}

	var survival []SurvivalPoint
	if reportSurvival {
		var err error
		survival, err = FitKaplanMeier(matrix)
		if err != nil {
			return nil, fmt.Errorf("calculate survival: %w", err)
		}
	} else if _, err := validateSurvivalMatrix(matrix); err != nil {
		return nil, fmt.Errorf("validate burndown: %w", err)
	}

	start := FloorDateTime(time.Unix(header.Start, 0), header.TickSize)
	last := time.Unix(header.Last, 0)

	finish := start.Add(time.Duration(len(matrix[0])*header.Sampling) * time.Duration(header.TickSize) * time.Second)

	var finalMatrix [][]float64
	var dateRange []time.Time
	var labels []string

	if resample != "no" && resample != "raw" {
		fmt.Printf("resampling to %s, please wait...\n", resample)

		// Interpolate the day x day matrix
		daily, err := InterpolateBurndownMatrix(matrix, header.Granularity, header.Sampling, interpolationProgress)
		if err != nil {
			return nil, fmt.Errorf("interpolation failed: %v", err)
		}

		// Zero out rows after 'last' like Python's daily[(last - start).days :] = 0.
		lastDays := int(last.Sub(start) / (24 * time.Hour))
		for i := lastDays; i < len(daily); i++ {
			for j := range daily[i] {
				daily[i][j] = 0
			}
		}

		// Resample the bands - convert Python's pandas logic to Go
		dateRange, finalMatrix, labels, err = resampleBurndownData(daily, start, finish, resample)
		if err != nil {
			// Try fallback resampling like Python does: step to a finer frequency.
			if _, base, perr := parseResampleFreq(resample); perr == nil {
				switch base {
				case "YE":
					fmt.Println("too loose resampling - by year, trying by month")
					fallback, fallbackErr := LoadBurndown(header, name, matrix, "month", false, interpolationProgress)
					if fallback != nil {
						fallback.Survival = survival
					}
					return fallback, fallbackErr
				case "ME":
					fmt.Println("too loose resampling - by month, trying by day")
					fallback, fallbackErr := LoadBurndown(header, name, matrix, "day", false, interpolationProgress)
					if fallback != nil {
						fallback.Survival = survival
					}
					return fallback, fallbackErr
				case "W":
					fmt.Println("too loose resampling - by week, trying by day")
					fallback, fallbackErr := LoadBurndown(header, name, matrix, "day", false, interpolationProgress)
					if fallback != nil {
						fallback.Survival = survival
					}
					return fallback, fallbackErr
				}
			}
			return nil, fmt.Errorf("too loose resampling: %s. Try finer", resample)
		}
	} else {
		// Raw mode - show age band labels
		finalMatrix = make([][]float64, len(matrix))
		for i := range matrix {
			finalMatrix[i] = make([]float64, len(matrix[i]))
			for j := range matrix[i] {
				finalMatrix[i][j] = float64(matrix[i][j])
			}
		}

		// Generate age band labels like Python does
		labels = make([]string, len(matrix))
		for i := range matrix {
			startTime := start.Add(time.Duration(i*header.Granularity) * time.Duration(header.TickSize) * time.Second)
			endTime := start.Add(time.Duration((i+1)*header.Granularity) * time.Duration(header.TickSize) * time.Second)
			labels[i] = fmt.Sprintf("%s - %s", startTime.Format("2006-01-02"), endTime.Format("2006-01-02"))
		}

		// Create date range for raw data
		dateRange = make([]time.Time, len(matrix[0]))
		for i := range dateRange {
			dateRange[i] = start.Add(time.Duration(i*header.Sampling) * time.Duration(header.TickSize) * time.Second)
		}

		resample = "M" // fake resampling type as Python does
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

	// Fill the new resampled matrix
	resampledMatrix := make([][]float64, len(dateGranularitySampling))
	for i := range resampledMatrix {
		resampledMatrix[i] = make([]float64, len(dateRangeSampling))
	}

	for i, gdt := range dateGranularitySampling {
		var istart, ifinish int

		if i > 0 {
			istart = int(dateGranularitySampling[i-1].Sub(start) / (24 * time.Hour))
		}
		ifinish = int(gdt.Sub(start) / (24 * time.Hour))

		var j int
		for idx, sdt := range dateRangeSampling {
			if int(sdt.Sub(start)/(24*time.Hour)) >= istart {
				j = idx
				break
			}
		}

		for k := j; k < len(dateRangeSampling); k++ {
			sdtDays := int(dateRangeSampling[k].Sub(start) / (24 * time.Hour))
			if sdtDays < 0 {
				continue
			}

			var sum float64
			for dailyRow := istart; dailyRow < ifinish && dailyRow < len(daily); dailyRow++ {
				if sdtDays < len(daily[dailyRow]) {
					sum += daily[dailyRow][sdtDays]
				}
			}
			resampledMatrix[i][k] = sum
		}
	}

	// Generate labels based on the base resampling unit (matches Python exactly)
	var labels []string
	switch base {
	case "YE": // Year
		for _, dt := range dateGranularitySampling {
			labels = append(labels, fmt.Sprintf("%d", dt.Year()))
		}
	case "ME": // Month (also multi-month, e.g. 3M / quarterly)
		for _, dt := range dateGranularitySampling {
			labels = append(labels, dt.Format("2006 January"))
		}
	default: // Day, week, or other
		for _, dt := range dateGranularitySampling {
			labels = append(labels, dt.Format("2006-01-02"))
		}
	}

	return dateRangeSampling, resampledMatrix, labels, nil
}

// parseResampleFreq parses a pandas-style offset alias into a multiplier and a
// canonical base frequency in {D, W, ME, YE}. It accepts word aliases
// ("year"/"month"/"week"/"day"), single-letter aliases ("A", "M", "W", "D"),
// the pandas *E variants ("YE", "ME"), quarter aliases ("Q"/"QE", mapped to 3
// months) and an optional leading integer multiplier (e.g. "3M", "2W").
func parseResampleFreq(resample string) (int, string, error) {
	s := strings.TrimSpace(resample)
	switch strings.ToLower(s) {
	case "year":
		return 1, "YE", nil
	case "month":
		return 1, "ME", nil
	case "week":
		return 1, "W", nil
	case "day":
		return 1, "D", nil
	}

	// Split an optional leading integer multiplier from the unit token.
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	mult := 1
	if i > 0 {
		n, err := strconv.Atoi(s[:i])
		if err != nil || n <= 0 {
			return 0, "", fmt.Errorf("unsupported resample mode: %s", resample)
		}
		mult = n
	}

	unit := strings.ToUpper(s[i:])
	// Strip a pandas anchor suffix such as "W-SUN" or "Q-DEC".
	if dash := strings.IndexByte(unit, '-'); dash >= 0 {
		unit = unit[:dash]
	}
	switch unit {
	case "A", "Y", "YE", "YS", "AS":
		return mult, "YE", nil
	case "Q", "QE", "QS":
		// Treat a quarter as three months (month-end anchored).
		return mult * 3, "ME", nil
	case "M", "ME", "MS":
		return mult, "ME", nil
	case "W":
		return mult, "W", nil
	case "D":
		return mult, "D", nil
	default:
		return 0, "", fmt.Errorf("unsupported resample mode: %s", resample)
	}
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

	result := make([]time.Time, periods)
	switch base {
	case "YE":
		year := start.Year()
		if yearEnd(year, start).Before(start) {
			year++
		}
		for i := range result {
			result[i] = yearEnd(year, start)
			year += mult
		}
	case "ME":
		// Work on the first-of-month so adding months never overflows.
		year, month := start.Year(), start.Month()
		if monthEnd(year, month, start).Before(start) {
			nm := time.Date(year, month, 1, 0, 0, 0, 0, start.Location()).AddDate(0, 1, 0)
			year, month = nm.Year(), nm.Month()
		}
		for i := range result {
			result[i] = monthEnd(year, month, start)
			nm := time.Date(year, month, 1, 0, 0, 0, 0, start.Location()).AddDate(0, mult, 0)
			year, month = nm.Year(), nm.Month()
		}
	case "W":
		// pandas "W" anchors on Sunday (W-SUN); the bin is labelled by that Sunday.
		current := nextOrSameWeekday(start, time.Sunday)
		for i := range result {
			result[i] = current
			current = current.AddDate(0, 0, 7*mult)
		}
	case "D":
		for i := range result {
			result[i] = start.AddDate(0, 0, i*mult)
		}
	default:
		return nil, fmt.Errorf("unsupported frequency %q", base)
	}
	return result, nil
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
