package modes

import (
	"io"
	"os"
	"strings"
	"testing"
)

// The pipeline records run_time_per_item in seconds (time.Since(start).Seconds()),
// so the summary has to report seconds as well.
func TestPrintRuntimeSummaryReportsSeconds(t *testing.T) {
	analysis := analyzeRuntimeStats(map[string]float64{"Burndown": 42})

	if analysis.Statistics.TotalTimeSeconds != 42 || analysis.Metrics[0].TimeSeconds != 42 {
		t.Fatalf("runtime analysis kept the wrong seconds: total=%v item=%v",
			analysis.Statistics.TotalTimeSeconds, analysis.Metrics[0].TimeSeconds)
	}

	summary := captureStdout(t, func() {
		printRuntimeSummary(analysis)
	})

	if !strings.Contains(summary, "42.00 s") {
		t.Fatalf("runtime summary does not report 42.00 s:\n%s", summary)
	}

	if strings.Contains(summary, "ms") {
		t.Fatalf("runtime summary reports milliseconds although the values are seconds:\n%s", summary)
	}
}

// captureStdout runs fn with os.Stdout redirected into a pipe and returns what
// fn wrote there.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}

	original := os.Stdout
	os.Stdout = writeEnd

	defer func() {
		os.Stdout = original
	}()

	done := make(chan string, 1)

	go func() {
		captured, _ := io.ReadAll(readEnd)
		done <- string(captured)
	}()

	fn()

	err = writeEnd.Close()
	if err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}

	captured := <-done

	err = readEnd.Close()
	if err != nil {
		t.Fatalf("close stdout read end: %v", err)
	}

	return captured
}
