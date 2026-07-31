package modes

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/readers"
)

func TestCompactPersonBurndownDropsEmptyBandsAndInactiveDates(t *testing.T) {
	dates := []time.Time{
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data := &burndown.ProcessedBurndown{
		Matrix: [][]float64{
			{0, 0, 0, 0, 0},
			{0, 12, 10, 0, 0},
			{0, 0, 4, 2, 0},
			{0, 0, 0, 0, 0},
		},
		DateRange: dates,
		Labels:    []string{"unused-old", "2021", "2022", "unused-new"},
	}

	err := compactPersonBurndown(data)
	if err != nil {
		t.Fatalf("compactPersonBurndown() failed: %v", err)
	}

	wantMatrix := [][]float64{
		{12, 10, 0},
		{0, 4, 2},
	}
	if !reflect.DeepEqual(data.Matrix, wantMatrix) {
		t.Fatalf("compacted matrix = %v, want %v", data.Matrix, wantMatrix)
	}
	if want := []string{"2021", "2022"}; !reflect.DeepEqual(data.Labels, want) {
		t.Fatalf("compacted labels = %v, want %v", data.Labels, want)
	}
	if want := dates[1:4]; !reflect.DeepEqual(data.DateRange, want) {
		t.Fatalf("compacted dates = %v, want %v", data.DateRange, want)
	}
}

func TestCompactPersonBurndownKeepsNeighborForSinglePoint(t *testing.T) {
	dates := []time.Time{
		time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data := &burndown.ProcessedBurndown{
		Matrix:    [][]float64{{0, 7}},
		DateRange: dates,
		Labels:    []string{"2024"},
	}

	err := compactPersonBurndown(data)
	if err != nil {
		t.Fatalf("compactPersonBurndown() failed: %v", err)
	}
	if !reflect.DeepEqual(data.DateRange, dates) {
		t.Fatalf("single-point date range = %v, want %v", data.DateRange, dates)
	}
}

func TestCompactPersonBurndownRejectsEmptyActivity(t *testing.T) {
	data := &burndown.ProcessedBurndown{
		Matrix:    [][]float64{{0, 0}},
		DateRange: []time.Time{time.Unix(0, 0), time.Unix(1, 0)},
		Labels:    []string{"unused"},
	}
	err := compactPersonBurndown(data)
	if err == nil {
		t.Fatal("compactPersonBurndown() accepted an all-zero contributor")
	}
}

// personBurndownStubReader serves a fixed people burndown fan-out and forces the
// legacy person renderer by reporting the project burndown analysis as missing.
type personBurndownStubReader struct {
	readers.Reader

	people []readers.PeopleBurndown
}

func (reader personBurndownStubReader) GetPeopleBurndown() ([]readers.PeopleBurndown, error) {
	return reader.people, nil
}

func (personBurndownStubReader) GetProjectBurndownWithHeader() (burndown.BurndownHeader, string, [][]int, error) {
	return burndown.BurndownHeader{}, "", nil, readers.ErrAnalysisMissing
}

func activePersonMatrix() [][]int {
	return [][]int{
		{100, 90, 80},
		{120, 100, 85},
		{110, 95, 90},
	}
}

func idlePersonMatrix() [][]int {
	return [][]int{
		{0, 0, 0},
		{0, 0, 0},
		{0, 0, 0},
	}
}

func personBurndownTestRange() (*time.Time, *time.Time) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)

	return &start, &end
}

// captureStderr runs fn with os.Stderr redirected into a pipe and returns what
// fn wrote there.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}

	original := os.Stderr
	os.Stderr = writeEnd

	defer func() {
		os.Stderr = original
	}()

	done := make(chan string, 1)

	go func() {
		captured, _ := io.ReadAll(readEnd)
		done <- string(captured)
	}()

	fn()

	err = writeEnd.Close()
	if err != nil {
		t.Fatalf("close stderr pipe: %v", err)
	}

	captured := <-done

	err = readEnd.Close()
	if err != nil {
		t.Fatalf("close stderr read end: %v", err)
	}

	return captured
}

// An idle contributor is ordinary — a people-dict routinely covers more people
// than a single repository does — so it must not take down the whole fan-out.
func TestBurndownPersonSkipsIdlePeopleAndRendersTheRest(t *testing.T) {
	people := []readers.PeopleBurndown{
		{Person: "active-dev", Matrix: activePersonMatrix()},
		{Person: "idle-dev", Matrix: idlePersonMatrix()},
	}
	reader := personBurndownStubReader{people: people}
	output := filepath.Join(t.TempDir(), "burndown_person.png")

	_, outputFiles, err := personBurndownOutputPaths(people, output)
	if err != nil {
		t.Fatalf("personBurndownOutputPaths() failed: %v", err)
	}

	start, end := personBurndownTestRange()

	opts := defaultOptions()
	opts.Quiet = true

	err = BurndownPersonWithOptions(reader, output, start, end, opts)
	if err != nil {
		t.Fatalf("BurndownPersonWithOptions() failed on an idle contributor: %v", err)
	}

	_, err = os.Stat(outputFiles[0])
	if err != nil {
		t.Fatalf("active contributor chart missing at %s: %v", outputFiles[0], err)
	}

	if _, err := os.Stat(outputFiles[1]); !os.IsNotExist(err) {
		t.Fatalf("idle contributor chart at %s should not exist, stat err = %v", outputFiles[1], err)
	}
}

func TestBurndownPersonFailsWhenEveryPersonIsIdle(t *testing.T) {
	people := []readers.PeopleBurndown{
		{Person: "idle-one", Matrix: idlePersonMatrix()},
		{Person: "idle-two", Matrix: idlePersonMatrix()},
	}
	reader := personBurndownStubReader{people: people}
	output := filepath.Join(t.TempDir(), "burndown_person.png")

	start, end := personBurndownTestRange()

	opts := defaultOptions()
	opts.Quiet = true

	err := BurndownPersonWithOptions(reader, output, start, end, opts)
	if !errors.Is(err, errNoPersonActivity) {
		t.Fatalf("BurndownPersonWithOptions() = %v, want errNoPersonActivity", err)
	}
}

func TestBurndownPersonSkipNoticeHonorsQuiet(t *testing.T) {
	const notice = "Skipping person burndown for idle-dev"

	people := []readers.PeopleBurndown{
		{Person: "active-dev", Matrix: activePersonMatrix()},
		{Person: "idle-dev", Matrix: idlePersonMatrix()},
	}
	start, end := personBurndownTestRange()

	run := func(quiet bool) string {
		reader := personBurndownStubReader{people: people}
		output := filepath.Join(t.TempDir(), "burndown_person.png")

		opts := defaultOptions()
		opts.Quiet = quiet

		return captureStderr(t, func() {
			err := BurndownPersonWithOptions(reader, output, start, end, opts)
			if err != nil {
				t.Errorf("BurndownPersonWithOptions(quiet=%v) failed: %v", quiet, err)
			}
		})
	}

	if verbose := run(false); !strings.Contains(verbose, notice) {
		t.Fatalf("stderr = %q, want it to contain %q", verbose, notice)
	}

	if quiet := run(true); strings.Contains(quiet, "Skipping person burndown") {
		t.Fatalf("quiet stderr = %q, want no skip notice", quiet)
	}
}
