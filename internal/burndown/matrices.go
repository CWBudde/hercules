package burndown

import (
	"log"
	"math"
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
	maxCols := maximumMatrixColumns(matrix)
	validatePerTickDimensions(matrix, granularity, sampling, accPerTick, offset, maxCols)

	stream := newBurndownMatrixStream(matrix, granularity, sampling)
	for tick := range accPerTick {
		stream.advanceTo(tick - offset)

		for cohort := range maxCols * granularity {
			accPerTick[tick][cohort+offset] += stream.valueAt(cohort)
		}
	}
}

type burndownMatrixStream struct {
	bands       []burndownBandStream
	granularity int
	sampling    int
	rows        int
	preparedRow int
	position    int
	active      bool
}

type burndownBandStream struct {
	matrix      DenseHistory
	column      int
	granularity int
	sampling    int
	row         int
	previous    []float32
	chunk       []float32
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
	granularity,
	sampling int,
	accPerTick [][]float32,
	offset, maximumColumns int,
) {
	if granularity <= 0 || sampling <= 0 || offset < 0 {
		log.Panicf(
			"merge bug: invalid interpolation dimensions: granularity=%d sampling=%d offset=%d",
			granularity, sampling, offset,
		)
	}

	neededRows := len(matrix)*sampling + offset
	if len(accPerTick) < neededRows {
		log.Panicf("merge bug: too few per-tick rows: required %d, have %d",
			neededRows, len(accPerTick))
	}

	neededColumns := maximumColumns*granularity + offset
	for rowIndex, row := range accPerTick {
		if len(row) < neededColumns {
			log.Panicf("merge bug: too few per-tick cols in row %d: required %d, have %d",
				rowIndex, neededColumns, len(row))
		}
	}
}

func newBurndownMatrixStream(
	matrix DenseHistory,
	granularity, sampling int,
) *burndownMatrixStream {
	maximumColumns := maximumMatrixColumns(matrix)
	stream := &burndownMatrixStream{
		bands:       make([]burndownBandStream, maximumColumns),
		granularity: granularity,
		sampling:    sampling,
		rows:        len(matrix),
		preparedRow: -1,
	}

	for column := range stream.bands {
		stream.bands[column] = burndownBandStream{
			matrix:      matrix,
			column:      column,
			granularity: granularity,
			sampling:    sampling,
			row:         -1,
			previous:    make([]float32, granularity),
			chunk:       make([]float32, sampling*granularity),
		}
	}

	return stream
}

func (stream *burndownMatrixStream) advanceTo(tick int) {
	stream.active = tick >= 0 && stream.rows > 0
	if !stream.active {
		return
	}

	lastTick := stream.rows*stream.sampling - 1
	tick = min(tick, lastTick)

	targetRow := tick / stream.sampling
	for stream.preparedRow < targetRow {
		stream.preparedRow++
		for bandIndex := range stream.bands {
			stream.bands[bandIndex].prepare(stream.preparedRow)
		}
	}

	stream.position = tick % stream.sampling
}

func (stream *burndownMatrixStream) valueAt(cohort int) float32 {
	if !stream.active || cohort < 0 {
		return 0
	}

	column := cohort / stream.granularity
	if column >= len(stream.bands) {
		return 0
	}

	bandOffset := cohort % stream.granularity

	return stream.bands[column].chunk[stream.position*stream.granularity+bandOffset]
}

func (stream *burndownBandStream) prepare(rowIndex int) {
	if stream.row >= 0 {
		copy(stream.previous, stream.rowAt(stream.rowEnd()-1))
	}

	clear(stream.chunk)
	stream.row = rowIndex

	if stream.column >= len(stream.matrix[rowIndex]) ||
		stream.column*stream.granularity > (rowIndex+1)*stream.sampling {
		// Sparse rows omit trailing zero-valued bands, and future bands are zero.
		return
	}

	stream.interpolateCell()
}

func (stream *burndownBandStream) interpolateCell() {
	switch {
	case (stream.column+1)*stream.granularity >= stream.rowEnd():
		stream.interpolateRisingCell()
	case (stream.column+1)*stream.granularity >= stream.rowStart():
		peak := stream.interpolationPeak()
		finishIndex := (stream.column + 1) * stream.granularity
		stream.raise(finishIndex, peak)
		stream.decay(finishIndex, peak)
	default:
		startValue := stream.value(stream.row - 1)
		stream.decay(stream.rowStart(), startValue)
	}
}

func (stream *burndownBandStream) interpolateRisingCell() {
	finishIndex := stream.rowEnd()
	finishValue := stream.value(stream.row)

	columnStart := stream.column * stream.granularity
	if columnStart <= stream.rowStart() {
		stream.raise(finishIndex, finishValue)

		return
	}

	if finishIndex <= columnStart {
		return
	}

	stream.raise(finishIndex, finishValue)

	average := finishValue / float32(finishIndex-columnStart)
	for tickIndex := columnStart; tickIndex < finishIndex; tickIndex++ {
		for bandIndex := columnStart; bandIndex <= tickIndex; bandIndex++ {
			stream.set(tickIndex, bandIndex, average)
		}
	}
}

func (stream *burndownBandStream) decay(startIndex int, startValue float32) {
	if startValue == 0 {
		return
	}

	decayRatio := stream.value(stream.row) / startValue
	scale := float32(stream.rowEnd() - startIndex)

	columnStart := stream.column * stream.granularity
	columnEnd := (stream.column + 1) * stream.granularity

	for bandIndex := columnStart; bandIndex < columnEnd; bandIndex++ {
		initial := stream.at(startIndex-1, bandIndex)
		for tickIndex := startIndex; tickIndex < stream.rowEnd(); tickIndex++ {
			stream.set(
				tickIndex, bandIndex, initial*
					(1+(decayRatio-1)*float32(tickIndex-startIndex+1)/scale),
			)
		}
	}
}

func (stream *burndownBandStream) raise(finishIndex int, finishValue float32) {
	var initial float32
	if stream.row > 0 {
		initial = stream.value(stream.row - 1)
	}

	startIndex := max(stream.rowStart(), stream.column*stream.granularity)
	if finishValue < initial {
		stream.lowerExistingBand(startIndex, finishIndex, initial, finishValue)

		return
	}

	if startIndex == finishIndex {
		return
	}

	average := (finishValue - initial) / float32(finishIndex-startIndex)
	for tickIndex := stream.rowStart(); tickIndex < finishIndex; tickIndex++ {
		for bandIndex := startIndex; bandIndex <= tickIndex; bandIndex++ {
			stream.set(tickIndex, bandIndex, average)
		}
	}

	// Copy [column*granularity..row*sampling).
	for tickIndex := stream.rowStart(); tickIndex < finishIndex; tickIndex++ {
		bandStart := stream.column * stream.granularity
		bandEnd := stream.rowStart()

		if bandStart >= bandEnd {
			continue
		}

		for bandIndex := bandStart; bandIndex < bandEnd; bandIndex++ {
			stream.set(tickIndex, bandIndex, stream.at(tickIndex-1, bandIndex))
		}
	}
}

func (stream *burndownBandStream) lowerExistingBand(
	startIndex, finishIndex int,
	initial, finish float32,
) {
	steps := finishIndex - startIndex
	if steps <= 0 {
		return
	}

	bandStart := stream.column * stream.granularity
	bandEnd := (stream.column + 1) * stream.granularity

	for tickIndex := startIndex; tickIndex < finishIndex; tickIndex++ {
		sourceTotal := float32(0)
		for bandIndex := bandStart; bandIndex < bandEnd; bandIndex++ {
			sourceTotal += stream.at(tickIndex-1, bandIndex)
		}

		progress := float32(tickIndex-startIndex+1) / float32(steps)
		targetTotal := initial + (finish-initial)*progress

		if sourceTotal <= 0 {
			continue
		}

		scale := targetTotal / sourceTotal
		for bandIndex := bandStart; bandIndex < bandEnd; bandIndex++ {
			stream.set(
				tickIndex,
				bandIndex,
				stream.at(tickIndex-1, bandIndex)*scale,
			)
		}
	}
}

func (stream *burndownBandStream) interpolationPeak() float32 {
	previousValue := stream.value(stream.row - 1)
	currentValue := stream.value(stream.row)
	delta := float32((stream.column+1)*stream.granularity - stream.rowStart())
	previous, scale := stream.previousPeakValueAndScale()

	peak := previousValue + (previousValue-previous)/scale*delta
	if currentValue <= peak {
		return peak
	}

	if stream.row == len(stream.matrix)-1 {
		// Not enough data to interpolate; this is at least not restricted.
		return currentValue
	}

	nextValue := stream.value(stream.row + 1)
	decayPerTick := (currentValue - nextValue) / float32(stream.sampling)

	return currentValue +
		decayPerTick*float32(stream.rowEnd()-(stream.column+1)*stream.granularity)
}

func (stream *burndownBandStream) previousPeakValueAndScale() (float32, float32) {
	if stream.row > 0 &&
		(stream.row-1)*stream.sampling >= stream.column*stream.granularity {
		var previous float32
		if stream.row > 1 {
			previous = stream.value(stream.row - 2)
		}

		return previous, float32(stream.sampling)
	}

	if stream.row == 0 {
		return 0, float32(stream.sampling)
	}

	return 0, float32(stream.rowStart() - stream.column*stream.granularity)
}

func (stream *burndownBandStream) value(rowIndex int) float32 {
	if rowIndex < 0 || rowIndex >= len(stream.matrix) ||
		stream.column >= len(stream.matrix[rowIndex]) {
		return 0
	}

	return float32(stream.matrix[rowIndex][stream.column])
}

func (stream *burndownBandStream) rowStart() int {
	return stream.row * stream.sampling
}

func (stream *burndownBandStream) rowEnd() int {
	return (stream.row + 1) * stream.sampling
}

func (stream *burndownBandStream) rowAt(tick int) []float32 {
	if tick == stream.rowStart()-1 {
		return stream.previous
	}

	offset := (tick - stream.rowStart()) * stream.granularity

	return stream.chunk[offset : offset+stream.granularity]
}

func (stream *burndownBandStream) at(tick, cohort int) float32 {
	return stream.rowAt(tick)[cohort-stream.column*stream.granularity]
}

func (stream *burndownBandStream) set(tick, cohort int, value float32) {
	stream.rowAt(tick)[cohort-stream.column*stream.granularity] = value
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
	commonMerged := common1.Copy()
	commonMerged.Merge(common2)

	sampling := min(sampling1, sampling2)
	granularity := min(granularity1, granularity2)

	size := roundTime(commonMerged.EndTimeAsTime(), tickSize, true) -
		roundTime(commonMerged.BeginTimeAsTime(), tickSize, false)
	result := make(DenseHistory, (size+sampling-1)/sampling)
	resultColumns := (size + granularity - 1) / granularity

	first := newBurndownMatrixStream(matrix1, granularity1, sampling1)
	second := newBurndownMatrixStream(matrix2, granularity2, sampling2)
	mergedBegin := roundTime(commonMerged.BeginTimeAsTime(), tickSize, false)
	firstOffset := roundTime(common1.BeginTimeAsTime(), tickSize, false) - mergedBegin
	secondOffset := roundTime(common2.BeginTimeAsTime(), tickSize, false) - mergedBegin

	// Only the currently sampled output row is accumulated. The interpolation
	// streams retain one source-sampling chunk per age band instead of a full
	// duration-by-duration per-tick matrix.
	for rowIndex := range result {
		sampledIndex := (rowIndex+1)*sampling - 1
		first.advanceTo(sampledIndex - firstOffset)
		second.advanceTo(sampledIndex - secondOffset)

		result[rowIndex] = make([]int64, resultColumns)
		for bandIndex := range resultColumns {
			accum := float32(0)
			for k := bandIndex * granularity; k < (bandIndex+1)*granularity; k++ {
				accum += first.valueAt(k-firstOffset) + second.valueAt(k-secondOffset)
			}

			result[rowIndex][bandIndex] = int64(accum)
		}
	}

	return result
}
