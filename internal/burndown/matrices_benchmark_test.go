package burndown

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/cwbudde/hercules/internal/core"
)

func BenchmarkMergeBurndownMatricesMultiYearDaily(b *testing.B) {
	for _, years := range []int{2, 5, 10} {
		b.Run(fmt.Sprintf("%d-years", years), func(b *testing.B) {
			const (
				granularity = 30
				sampling    = 1
			)
			days := years * 365
			history, common := benchmarkDailyBurndownHistory(days, granularity)

			b.ReportAllocs()
			b.ReportMetric(
				float64(2*burndownStreamWorkspaceBytes(history, granularity, sampling)),
				"interpolation-workspace-bytes",
			)
			b.ReportMetric(
				float64(burndownDenseOutputBytes(days, granularity, sampling)),
				"required-output-bytes",
			)
			b.ResetTimer()
			var merged DenseHistory
			for range b.N {
				merged = MergeBurndownMatrices(
					history, history,
					granularity, sampling,
					granularity, sampling,
					24*time.Hour,
					common, common,
				)
			}
			runtime.KeepAlive(merged)
		})
	}
}

func benchmarkDailyBurndownHistory(days, granularity int) (DenseHistory, *core.CommonAnalysisResult) {
	history := make(DenseHistory, days)
	for day := range history {
		history[day] = make([]int64, day/granularity+1)
		for band := range history[day] {
			history[day][band] = int64(1_000 + (day-band)%23)
		}
	}

	const begin = int64(946684800) // 2000-01-01T00:00:00Z
	seconds := int64((24 * time.Hour) / time.Second)
	common := &core.CommonAnalysisResult{
		BeginTime: begin,
		EndTime:   begin + int64(days)*seconds,
	}

	return history, common
}

func burndownStreamWorkspaceBytes(history DenseHistory, granularity, sampling int) int64 {
	bands := int64(maximumMatrixColumns(history))
	floatsPerBand := int64(granularity * (sampling + 1))

	return bands * floatsPerBand * 4
}

func burndownDenseOutputBytes(days, granularity, sampling int) int64 {
	rows := int64((days + sampling - 1) / sampling)
	columns := int64((days + granularity - 1) / granularity)

	return rows * columns * 8
}
