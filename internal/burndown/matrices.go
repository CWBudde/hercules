package burndown

import (
	"log"
	"math"
	"runtime"
	"time"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/plumbing"
)

// DenseHistory is a 2D matrix with dimensions [number of samples][number of bands] -> number of lines.
// Rows represent time samples (y-axis), columns represent age bands (x-axis).
type DenseHistory [][]int64

// AddBurndownMatrix explodes `matrix` so that it is daily sampled and has daily bands, shift by `offset` ticks
// and add to the accumulator. `daily` size is square and is guaranteed to fit `matrix` by
// the caller.
// Rows: *at least* len(matrix) * sampling + offset
// Columns: *at least* len(matrix[...]) * granularity + offset
// `matrix` can be sparse, so that the last columns which are equal to 0 are truncated.
func AddBurndownMatrix(matrix DenseHistory, granularity, sampling int, accPerTick [][]float32, offset int) {
	//	defer print("AddBurndownMatrix exit\n")
	//	print("AddBurndownMatrix enter\n")

	// Determine the maximum number of bands; the actual one may be larger but we do not care
	maxCols := maximumMatrixColumns(matrix)
	validatePerTickDimensions(matrix, sampling, accPerTick, offset, maxCols)

	var memoryStats runtime.MemStats
	runtime.ReadMemStats(&memoryStats)

	perTick := makePerTickAccumulator(accPerTick)
	interpolator := burndownInterpolator{
		matrix: matrix, granularity: granularity, sampling: sampling, perTick: perTick, offset: offset,
	}
	interpolator.interpolate(maxCols)

	for y := len(matrix) * sampling; y+offset < len(perTick); y++ {
		copy(perTick[y+offset], perTick[len(matrix)*sampling-1+offset])
	}
	// the original matrix has been resampled by tick
	// add it to the accumulator
	for y, row := range perTick {
		for x, val := range row {
			accPerTick[y][x] += val
		}
	}

	runtime.ReadMemStats(&memoryStats)

	for i := range perTick {
		perTick[i] = nil
	}

	runtime.GC()
	var a runtime.MemStats
	runtime.ReadMemStats(&a)

	//	print("AddBurndownMatrix Deallocated: ", (m.Alloc-a.Alloc)/1024/1024, "\n")
}

type burndownInterpolator struct {
	matrix      DenseHistory
	granularity int
	sampling    int
	perTick     [][]float32
	offset      int
}

func maximumMatrixColumns(matrix DenseHistory) int {
	maximum := 0
	for _, row := range matrix {
		maximum = max(maximum, len(row))
	}

	return maximum
}

func validatePerTickDimensions(
	matrix DenseHistory,
	sampling int,
	accPerTick [][]float32,
	offset, maximumColumns int,
) {
	neededRows := len(matrix)*sampling + offset
	if len(accPerTick) < neededRows {
		log.Panicf("merge bug: too few per-tick rows: required %d, have %d",
			neededRows, len(accPerTick))
	}

	if len(accPerTick[0]) < maximumColumns {
		log.Panicf("merge bug: too few per-tick cols: required %d, have %d",
			maximumColumns, len(accPerTick[0]))
	}
}

func makePerTickAccumulator(accPerTick [][]float32) [][]float32 {
	perTick := make([][]float32, len(accPerTick))
	for rowIndex, row := range accPerTick {
		perTick[rowIndex] = make([]float32, len(row))
	}

	return perTick
}

func (interpolator *burndownInterpolator) interpolate(maximumColumns int) {
	for columnIndex := range maximumColumns {
		for rowIndex := range interpolator.matrix {
			if columnIndex >= len(interpolator.matrix[rowIndex]) {
				// Sparse rows omit trailing zero-valued bands.
				continue
			}

			if columnIndex*interpolator.granularity > (rowIndex+1)*interpolator.sampling {
				// The future is zeros.
				continue
			}

			interpolator.interpolateCell(rowIndex, columnIndex)
		}
	}
}

func (interpolator *burndownInterpolator) interpolateCell(rowIndex, columnIndex int) {
	switch {
	case (columnIndex+1)*interpolator.granularity >= (rowIndex+1)*interpolator.sampling:
		interpolator.interpolateRisingCell(rowIndex, columnIndex)
	case (columnIndex+1)*interpolator.granularity >= rowIndex*interpolator.sampling:
		peak := interpolator.interpolationPeak(rowIndex, columnIndex)
		finishIndex := (columnIndex + 1) * interpolator.granularity
		interpolator.raise(rowIndex, columnIndex, finishIndex, peak)
		interpolator.decay(rowIndex, columnIndex, finishIndex, peak)
	default:
		startValue := interpolator.value(rowIndex-1, columnIndex)
		interpolator.decay(rowIndex, columnIndex, rowIndex*interpolator.sampling, startValue)
	}
}

func (interpolator *burndownInterpolator) interpolateRisingCell(rowIndex, columnIndex int) {
	finishIndex := (rowIndex + 1) * interpolator.sampling
	finishValue := interpolator.value(rowIndex, columnIndex)

	columnStart := columnIndex * interpolator.granularity
	if columnStart <= rowIndex*interpolator.sampling {
		interpolator.raise(rowIndex, columnIndex, finishIndex, finishValue)

		return
	}

	if finishIndex <= columnStart {
		return
	}

	interpolator.raise(rowIndex, columnIndex, finishIndex, finishValue)

	average := finishValue / float32(finishIndex-columnStart)
	for tickIndex := columnStart; tickIndex < finishIndex; tickIndex++ {
		for bandIndex := columnStart; bandIndex <= tickIndex; bandIndex++ {
			interpolator.perTick[tickIndex+interpolator.offset][bandIndex+interpolator.offset] = average
		}
	}
}

func (interpolator *burndownInterpolator) decay(
	rowIndex, columnIndex, startIndex int,
	startValue float32,
) {
	if startValue == 0 {
		return
	}

	decayRatio := interpolator.value(rowIndex, columnIndex) / startValue
	scale := float32((rowIndex+1)*interpolator.sampling - startIndex)

	columnStart := columnIndex * interpolator.granularity

	columnEnd := (columnIndex + 1) * interpolator.granularity
	for bandIndex := columnStart; bandIndex < columnEnd; bandIndex++ {
		initial := interpolator.perTick[startIndex-1+interpolator.offset][bandIndex+interpolator.offset]
		for tickIndex := startIndex; tickIndex < (rowIndex+1)*interpolator.sampling; tickIndex++ {
			interpolator.perTick[tickIndex+interpolator.offset][bandIndex+interpolator.offset] = initial *
				(1 + (decayRatio-1)*float32(tickIndex-startIndex+1)/scale)
		}
	}
}

func (interpolator *burndownInterpolator) raise(
	rowIndex, columnIndex, finishIndex int,
	finishValue float32,
) {
	var initial float32
	if rowIndex > 0 {
		initial = interpolator.value(rowIndex-1, columnIndex)
	}

	startIndex := max(rowIndex*interpolator.sampling, columnIndex*interpolator.granularity)
	if finishValue < initial {
		interpolator.lowerExistingBand(
			columnIndex, startIndex, finishIndex, initial, finishValue,
		)

		return
	}

	if startIndex == finishIndex {
		return
	}

	average := (finishValue - initial) / float32(finishIndex-startIndex)
	for tickIndex := rowIndex * interpolator.sampling; tickIndex < finishIndex; tickIndex++ {
		for bandIndex := startIndex; bandIndex <= tickIndex; bandIndex++ {
			interpolator.perTick[tickIndex+interpolator.offset][bandIndex+interpolator.offset] = average
		}
	}

	// Copy [columnIndex*granularity..rowIndex*sampling).
	for tickIndex := rowIndex * interpolator.sampling; tickIndex < finishIndex; tickIndex++ {
		bandStart := columnIndex * interpolator.granularity
		bandEnd := rowIndex * interpolator.sampling

		if bandStart >= bandEnd {
			continue
		}

		targetRow := interpolator.perTick[tickIndex+interpolator.offset]
		sourceRow := interpolator.perTick[tickIndex-1+interpolator.offset]

		for bandIndex := bandStart; bandIndex < bandEnd; bandIndex++ {
			targetRow[bandIndex+interpolator.offset] = sourceRow[bandIndex+interpolator.offset]
		}
	}
}

func (interpolator *burndownInterpolator) lowerExistingBand(
	columnIndex, startIndex, finishIndex int,
	initial, finish float32,
) {
	steps := finishIndex - startIndex
	if steps <= 0 {
		return
	}

	bandStart := columnIndex * interpolator.granularity
	bandEnd := (columnIndex + 1) * interpolator.granularity

	for tickIndex := startIndex; tickIndex < finishIndex; tickIndex++ {
		source := interpolator.perTick[tickIndex-1+interpolator.offset]
		target := interpolator.perTick[tickIndex+interpolator.offset]

		sourceTotal := float32(0)
		for bandIndex := bandStart; bandIndex < bandEnd; bandIndex++ {
			sourceTotal += source[bandIndex+interpolator.offset]
		}

		progress := float32(tickIndex-startIndex+1) / float32(steps)
		targetTotal := initial + (finish-initial)*progress

		if sourceTotal <= 0 {
			continue
		}

		scale := targetTotal / sourceTotal
		for bandIndex := bandStart; bandIndex < bandEnd; bandIndex++ {
			target[bandIndex+interpolator.offset] = source[bandIndex+interpolator.offset] * scale
		}
	}
}

func (interpolator *burndownInterpolator) interpolationPeak(rowIndex, columnIndex int) float32 {
	previousValue := interpolator.value(rowIndex-1, columnIndex)
	currentValue := interpolator.value(rowIndex, columnIndex)
	delta := float32((columnIndex+1)*interpolator.granularity - rowIndex*interpolator.sampling)
	previous, scale := interpolator.previousPeakValueAndScale(rowIndex, columnIndex)

	peak := previousValue + (previousValue-previous)/scale*delta
	if currentValue <= peak {
		return peak
	}

	if rowIndex == len(interpolator.matrix)-1 {
		// Not enough data to interpolate; this is at least not restricted.
		return currentValue
	}

	nextValue := interpolator.value(rowIndex+1, columnIndex)
	decayPerTick := (currentValue - nextValue) / float32(interpolator.sampling)

	return currentValue +
		decayPerTick*float32((rowIndex+1)*interpolator.sampling-(columnIndex+1)*interpolator.granularity)
}

func (interpolator *burndownInterpolator) previousPeakValueAndScale(rowIndex, columnIndex int) (float32, float32) {
	if rowIndex > 0 && (rowIndex-1)*interpolator.sampling >= columnIndex*interpolator.granularity {
		var previous float32
		if rowIndex > 1 {
			previous = interpolator.value(rowIndex-2, columnIndex)
		}

		return previous, float32(interpolator.sampling)
	}

	if rowIndex == 0 {
		return 0, float32(interpolator.sampling)
	}

	return 0, float32(rowIndex*interpolator.sampling - columnIndex*interpolator.granularity)
}

func (interpolator *burndownInterpolator) value(rowIndex, columnIndex int) float32 {
	if rowIndex < 0 || rowIndex >= len(interpolator.matrix) ||
		columnIndex < 0 || columnIndex >= len(interpolator.matrix[rowIndex]) {
		return 0
	}

	return float32(interpolator.matrix[rowIndex][columnIndex])
}

func roundTime(t time.Time, d time.Duration, dir bool) int {
	if !dir {
		t = plumbing.FloorTime(t, d)
	}

	ticks := float64(t.Unix()) / d.Seconds()
	if dir {
		return int(math.Ceil(ticks))
	}

	return int(math.Floor(ticks))
}

// MergeBurndownMatrices takes two [number of samples][number of bands] matrices,
// resamples them to ticks so that they become square, sums and resamples back to the
// least of (sampling1, sampling2) and (granularity1, granularity2).
func MergeBurndownMatrices(
	matrix1, matrix2 DenseHistory, granularity1, sampling1, granularity2, sampling2 int, tickSize time.Duration,
	common1, common2 *core.CommonAnalysisResult,
) DenseHistory {
	//	defer print("MergeBurndownMatrices exit\n\n\n")
	//	print("MergeBurndownMatrices enter\n\n\n")
	commonMerged := common1.Copy()
	commonMerged.Merge(common2)

	var granularity, sampling int
	sampling = min(sampling1, sampling2)

	granularity = min(granularity1, granularity2)

	size := roundTime(commonMerged.EndTimeAsTime(), tickSize, true) -
		roundTime(commonMerged.BeginTimeAsTime(), tickSize, false)

	perTick := make([][]float32, size+granularity)
	for i := range perTick {
		perTick[i] = make([]float32, size+sampling)
	}

	//	var m runtime.MemStats
	//	runtime.ReadMemStats(&m)

	if len(matrix1) > 0 {
		AddBurndownMatrix(matrix1, granularity1, sampling1, perTick,
			roundTime(common1.BeginTimeAsTime(), tickSize, false)-
				roundTime(commonMerged.BeginTimeAsTime(), tickSize, false))
	}

	if len(matrix2) > 0 {
		AddBurndownMatrix(matrix2, granularity2, sampling2, perTick,
			roundTime(common2.BeginTimeAsTime(), tickSize, false)-
				roundTime(commonMerged.BeginTimeAsTime(), tickSize, false))
	}

	// convert daily to [][]int64
	result := make(DenseHistory, (size+sampling-1)/sampling)
	for rowIndex := range result {
		result[rowIndex] = make([]int64, (size+granularity-1)/granularity)

		sampledIndex := (rowIndex+1)*sampling - 1
		for bandIndex := range len(result[rowIndex]) {
			accum := float32(0)
			for k := bandIndex * granularity; k < (bandIndex+1)*granularity; k++ {
				accum += perTick[sampledIndex][k]
			}

			result[rowIndex][bandIndex] = int64(accum)
		}
	}

	//	runtime.ReadMemStats(&m)

	for i := range perTick {
		perTick[i] = nil
	}

	runtime.GC()

	//	var a runtime.MemStats
	//	runtime.ReadMemStats(&a)

	//	print("MergeBurndownMatrices Deallocated: ", (m.Alloc-a.Alloc)/1024/1024, "\n")

	return result
}
