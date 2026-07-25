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
//
//nolint:funlen // The interpolation cases share loop coordinates and boundary invariants.
func AddBurndownMatrix(matrix DenseHistory, granularity, sampling int, accPerTick [][]float32, offset int) {
	//	defer print("AddBurndownMatrix exit\n")
	//	print("AddBurndownMatrix enter\n")

	// Determine the maximum number of bands; the actual one may be larger but we do not care
	maxCols := 0
	for _, row := range matrix {
		if maxCols < len(row) {
			maxCols = len(row)
		}
	}

	neededRows := len(matrix)*sampling + offset
	if len(accPerTick) < neededRows {
		log.Panicf("merge bug: too few per-tick rows: required %d, have %d",
			neededRows, len(accPerTick))
	}

	if len(accPerTick[0]) < maxCols {
		log.Panicf("merge bug: too few per-tick cols: required %d, have %d",
			maxCols, len(accPerTick[0]))
	}

	var memoryStats runtime.MemStats
	runtime.ReadMemStats(&memoryStats)

	perTick := make([][]float32, len(accPerTick))
	for i, row := range accPerTick {
		perTick[i] = make([]float32, len(row))
	}

	for columnIndex := range maxCols {
		for rowIndex := range matrix {
			if columnIndex*granularity > (rowIndex+1)*sampling {
				// the future is zeros
				continue
			}

			decay := func(startIndex int, startVal float32) {
				if startVal == 0 {
					return
				}

				decayRatio := float32(matrix[rowIndex][columnIndex]) / startVal // <= 1

				scale := float32((rowIndex+1)*sampling - startIndex)
				for i := columnIndex * granularity; i < (columnIndex+1)*granularity; i++ {
					initial := perTick[startIndex-1+offset][i+offset]
					for j := startIndex; j < (rowIndex+1)*sampling; j++ {
						perTick[j+offset][i+offset] = initial *
							(1 + (decayRatio-1)*float32(j-startIndex+1)/scale)
					}
				}
			}
			raise := func(finishIndex int, finishVal float32) {
				var initial float32
				if rowIndex > 0 {
					initial = float32(matrix[rowIndex-1][columnIndex])
				}

				startIndex := max(rowIndex*sampling, columnIndex*granularity)

				if startIndex == finishIndex {
					return
				}

				avg := (finishVal - initial) / float32(finishIndex-startIndex)
				for j := rowIndex * sampling; j < finishIndex; j++ {
					for i := startIndex; i <= j; i++ {
						perTick[j+offset][i+offset] = avg
					}
				}
				// copy [x*g..y*s)
				for j := rowIndex * sampling; j < finishIndex; j++ {
					for i := columnIndex * granularity; i < rowIndex*sampling; i++ {
						perTick[j+offset][i+offset] = perTick[j-1+offset][i+offset]
					}
				}
			}

			switch {
			case (columnIndex+1)*granularity >= (rowIndex+1)*sampling:
				// x*granularity <= (y+1)*sampling
				// 1. x*granularity <= y*sampling
				//    y*sampling..(y+1)sampling
				//
				//       x+1
				//        /
				//       /
				//      / y+1  -|
				//     /        |
				//    / y      -|
				//   /
				//  / x
				//
				// 2. x*granularity > y*sampling
				//    x*granularity..(y+1)sampling
				//
				//       x+1
				//        /
				//       /
				//      / y+1  -|
				//     /        |
				//    / x      -|
				//   /
				//  / y
				if columnIndex*granularity <= rowIndex*sampling {
					raise((rowIndex+1)*sampling, float32(matrix[rowIndex][columnIndex]))
				} else if (rowIndex+1)*sampling > columnIndex*granularity {
					raise((rowIndex+1)*sampling, float32(matrix[rowIndex][columnIndex]))

					avg := float32(matrix[rowIndex][columnIndex]) /
						float32((rowIndex+1)*sampling-columnIndex*granularity)
					for j := columnIndex * granularity; j < (rowIndex+1)*sampling; j++ {
						for i := columnIndex * granularity; i <= j; i++ {
							perTick[j+offset][i+offset] = avg
						}
					}
				}
			case (columnIndex+1)*granularity >= rowIndex*sampling:
				// y*sampling <= (x+1)*granularity < (y+1)sampling
				// y*sampling..(x+1)*granularity
				// (x+1)*granularity..(y+1)sampling
				//        x+1
				//         /\
				//        /  \
				//       /    \
				//      /    y+1
				//     /
				//    y
				previousValue := float32(matrix[rowIndex-1][columnIndex])
				currentValue := float32(matrix[rowIndex][columnIndex])
				var peak float32
				delta := float32((columnIndex+1)*granularity - rowIndex*sampling)
				var scale float32
				var previous float32

				if rowIndex > 0 && (rowIndex-1)*sampling >= columnIndex*granularity {
					// x*g <= (y-1)*s <= y*s <= (x+1)*g <= (y+1)*s
					//           |________|.......^
					if rowIndex > 1 {
						previous = float32(matrix[rowIndex-2][columnIndex])
					}

					scale = float32(sampling)
				} else {
					// (y-1)*s < x*g <= y*s <= (x+1)*g <= (y+1)*s
					//            |______|.......^
					if rowIndex == 0 {
						scale = float32(sampling)
					} else {
						scale = float32(rowIndex*sampling - columnIndex*granularity)
					}
				}

				peak = previousValue + (previousValue-previous)/scale*delta
				if currentValue > peak {
					// we need to adjust the peak, it may not be less than the decayed value
					if rowIndex < len(matrix)-1 {
						// y*s <= (x+1)*g <= (y+1)*s < (y+2)*s
						//           ^.........|_________|
						k := (currentValue - float32(matrix[rowIndex+1][columnIndex])) / float32(sampling) // > 0
						peak = float32(matrix[rowIndex][columnIndex]) +
							k*float32((rowIndex+1)*sampling-(columnIndex+1)*granularity)
						// peak > v2 > v1
					} else {
						peak = currentValue
						// not enough data to interpolate; this is at least not restricted
					}
				}

				raise((columnIndex+1)*granularity, peak)
				decay((columnIndex+1)*granularity, peak)
			default:
				// (x+1)*granularity < y*sampling
				// y*sampling..(y+1)sampling
				decay(rowIndex*sampling, float32(matrix[rowIndex-1][columnIndex]))
			}
		}
	}

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
