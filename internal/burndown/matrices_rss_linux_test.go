//go:build linux

package burndown

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"testing"
	"time"
)

var errUnexpectedProcStatm = errors.New("unexpected /proc/self/statm contents")

func BenchmarkMergeBurndownMatricesPeakRSSMultiYearDaily(b *testing.B) {
	for _, years := range []int{2, 5, 10} {
		b.Run(fmt.Sprintf("%d-years", years), func(b *testing.B) {
			const (
				granularity = 30
				sampling    = 1
			)
			days := years * 365
			history, common := benchmarkDailyBurndownHistory(days, granularity)

			debug.FreeOSMemory()
			baseline, err := currentRSSBytes()
			if err != nil {
				b.Skipf("read resident memory: %v", err)
			}

			stop := make(chan struct{})
			peak := make(chan uint64, 1)
			go samplePeakRSS(stop, baseline, peak)

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
			b.StopTimer()

			close(stop)
			peakBytes := <-peak
			b.ReportMetric(float64(peakBytes-baseline), "peak-RSS-delta-bytes")
			b.ReportMetric(
				float64(2*burndownStreamWorkspaceBytes(history, granularity, sampling)),
				"interpolation-workspace-bytes",
			)
			b.ReportMetric(
				float64(burndownDenseOutputBytes(days, granularity, sampling)),
				"required-output-bytes",
			)
		})
	}
}

func samplePeakRSS(stop <-chan struct{}, initial uint64, result chan<- uint64) {
	peak := initial
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			current, err := currentRSSBytes()
			if err == nil && current > peak {
				peak = current
			}
		case <-stop:
			current, err := currentRSSBytes()
			if err == nil && current > peak {
				peak = current
			}
			result <- peak

			return
		}
	}
}

func currentRSSBytes() (uint64, error) {
	statm, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, fmt.Errorf("read /proc/self/statm: %w", err)
	}

	fields := bytes.Fields(statm)
	if len(fields) < 2 {
		return 0, errUnexpectedProcStatm
	}
	residentPages, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse resident pages: %w", err)
	}
	pageSize, err := strconv.ParseUint(strconv.Itoa(os.Getpagesize()), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse page size: %w", err)
	}

	return residentPages * pageSize, nil
}
