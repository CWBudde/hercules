package modes

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/hercules/internal/render/readers"
)

const testTickSizeNanos = int64(24 * time.Hour)

// midTickBegin is deliberately not on a tick boundary: 13:47 UTC on a day-sized
// tick grid. Every flooring assertion below depends on that.
var midTickBegin = time.Date(2021, time.June, 15, 13, 47, 3, 0, time.UTC)

func TestTickAxisAnchorsOnTheFlooredTickBoundary(t *testing.T) {
	axis, ok := newTickAxis(midTickBegin.Unix(), testTickSizeNanos)
	if !ok {
		t.Fatal("newTickAxis() rejected a valid header")
	}

	// Tick 0 starts at the UTC midnight at or before the first commit, not at
	// the commit itself. The naive begin + tick*tickSize formula would put it
	// 13h47m later and shift every point on the chart with it.
	want := time.Date(2021, time.June, 15, 0, 0, 0, 0, time.UTC)
	if got := axis.dateOf(0); !got.Equal(want) {
		t.Fatalf("dateOf(0) = %s, want %s", got, want)
	}

	if got := axis.dateOf(0); got.After(midTickBegin) {
		t.Fatalf("tick 0 at %s starts after the first commit at %s", got, midTickBegin)
	}
}

func TestTickAxisRoundTripsTickIndices(t *testing.T) {
	axis, ok := newTickAxis(midTickBegin.Unix(), testTickSizeNanos)
	if !ok {
		t.Fatal("newTickAxis() rejected a valid header")
	}

	for tick := -5; tick <= 1000; tick++ {
		if got := axis.tickOf(axis.dateOf(tick)); got != tick {
			t.Fatalf("tickOf(dateOf(%d)) = %d", tick, got)
		}

		// Any instant inside a tick belongs to that tick, not the next one.
		inside := axis.dateOf(tick).Add(axis.step - time.Second)
		if got := axis.tickOf(inside); got != tick {
			t.Fatalf("tickOf(%s) = %d, want %d", inside, got, tick)
		}
	}
}

func TestTickAxisFloorsInstantsBeforeTickZero(t *testing.T) {
	axis, _ := newTickAxis(midTickBegin.Unix(), testTickSizeNanos)

	// Integer division truncates towards zero, so a half-open tick before the
	// repository starts would otherwise be reported as tick 0 and survive a
	// filter that meant to exclude it.
	before := axis.dateOf(0).Add(-time.Hour)
	if got := axis.tickOf(before); got != -1 {
		t.Fatalf("tickOf(one hour before tick 0) = %d, want -1", got)
	}
}

func TestTickAxisRejectsMissingHeader(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		begin    int64
		tickSize int64
	}{
		{name: "no header", begin: 0, tickSize: testTickSizeNanos},
		{name: "negative begin", begin: -1, tickSize: testTickSizeNanos},
		{name: "no tick size", begin: midTickBegin.Unix(), tickSize: 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, ok := newTickAxis(testCase.begin, testCase.tickSize); ok {
				t.Fatal("newTickAxis() accepted an unusable header")
			}
		})
	}
}

func TestSelectReportTicksKeepsBothBoundaries(t *testing.T) {
	axis, _ := newTickAxis(midTickBegin.Unix(), testTickSizeNanos)
	ticks := []int{0, 1, 2, 3, 4, 5}

	start := axis.dateOf(1)
	end := axis.dateOf(4)

	got := selectReportTicks(ticks, axis, true, &start, &end, "test")

	want := []int{1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("selectReportTicks() = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selectReportTicks() = %v, want %v", got, want)
		}
	}
}

func TestSelectReportTicksLeavesAnUnrequestedRangeAlone(t *testing.T) {
	axis, _ := newTickAxis(midTickBegin.Unix(), testTickSizeNanos)
	ticks := []int{0, 1, 2}

	if got := selectReportTicks(ticks, axis, true, nil, nil, "test"); len(got) != len(ticks) {
		t.Fatalf("selectReportTicks() dropped ticks with no range requested: %v", got)
	}
}

func TestSelectReportTicksWarnsWhenTicksCannotBeDated(t *testing.T) {
	ticks := []int{0, 1, 2}
	start := midTickBegin

	var got []int

	stderr := captureStderr(t, func() {
		got = selectReportTicks(ticks, tickAxis{}, false, &start, nil, "bus factor")
	})

	if len(got) != len(ticks) {
		t.Fatalf("an unhonourable range must leave the data alone, got %v", got)
	}

	if !strings.Contains(stderr, "bus factor") || !strings.Contains(stderr, "--start-date") {
		t.Fatalf("expected a warning naming the chart and the flag, got %q", stderr)
	}
}

func TestRefactoringProxyTicksInRangeIncludesBothBoundaries(t *testing.T) {
	base := time.Date(2023, time.March, 1, 0, 0, 0, 0, time.UTC)
	ticks := make([]readers.RefactoringProxyTick, 5)

	for i := range ticks {
		ticks[i] = readers.RefactoringProxyTick{Timestamp: base.AddDate(0, 0, i).Unix()}
	}

	start := base.AddDate(0, 0, 1)
	end := base.AddDate(0, 0, 3)

	got := refactoringProxyTicksInRange(ticks, &start, &end)
	if len(got) != 3 {
		t.Fatalf("refactoringProxyTicksInRange() kept %d ticks, want 3", len(got))
	}

	if got[0].Timestamp != start.Unix() || got[2].Timestamp != end.Unix() {
		t.Fatalf("both boundary ticks must survive, got %v", got)
	}
}

// TestBusFactorTimelinePlotsRealDates renders to SVG, whose tick labels are
// plain text, so the axis can actually be inspected rather than merely assumed.
func TestBusFactorTimelinePlotsRealDates(t *testing.T) {
	output := filepath.Join(t.TempDir(), "bus-factor.svg")

	err := BusFactor(newDatedBusFactorReader(midTickBegin.Unix()), output, nil, nil)
	if err != nil {
		t.Fatalf("BusFactor() unexpected error: %v", err)
	}

	timeline := readRenderedFile(t, filepath.Join(filepath.Dir(output), "bus-factor_timeline.svg"))
	if !strings.Contains(timeline, "2022") && !strings.Contains(timeline, "2023") {
		t.Fatal("expected calendar years on the timeline axis")
	}

	// The fixture spans two and a half years, which crosses the point where the
	// default formatter drops to a bare year while the locator keeps placing
	// sub-year ticks - producing an axis that reads "2021 2021 2021 2022 2022
	// 2022". Months on the axis are what tell those ticks apart.
	labels := dateAxisLabels(timeline)
	if len(labels) < 4 {
		t.Fatalf("expected several date ticks, got %v", labels)
	}

	months := 0

	for _, label := range labels {
		if monthLabelPattern.MatchString(label) {
			months++
		}
	}

	if months == 0 {
		t.Fatalf("every tick is a bare year, so ticks within a year are indistinguishable: %v", labels)
	}
}

// dateAxisLabels pulls the date-looking tick labels out of a rendered SVG. The
// value axis is numeric, so anything with a month name or a bare year is a date
// tick.
func dateAxisLabels(svg string) []string {
	labels := []string{}

	for _, match := range svgTextPattern.FindAllStringSubmatch(svg, -1) {
		text := strings.TrimSpace(match[1])
		if dateLabelPattern.MatchString(text) {
			labels = append(labels, text)
		}
	}

	return labels
}

var (
	svgTextPattern    = regexp.MustCompile(`<text[^>]*>([^<]*)</text>`)
	monthLabelPattern = regexp.MustCompile(`^(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)`)
	dateLabelPattern  = regexp.MustCompile(
		`^(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)( \d{4})?$|^\d{4}$`,
	)
)

// TestBusFactorTimelineFallsBackToTickIndices pins the other half: with no
// header the chart must keep the tick axis rather than anchor the series at
// 1970, which would be silently wrong instead of merely coarse.
func TestBusFactorTimelineFallsBackToTickIndices(t *testing.T) {
	output := filepath.Join(t.TempDir(), "bus-factor.svg")

	err := BusFactor(newDatedBusFactorReader(0), output, nil, nil)
	if err != nil {
		t.Fatalf("BusFactor() unexpected error: %v", err)
	}

	timeline := readRenderedFile(t, filepath.Join(filepath.Dir(output), "bus-factor_timeline.svg"))
	for _, year := range []string{"1970", "2021", "2022"} {
		if strings.Contains(timeline, year) {
			t.Fatalf("undated input must not produce a date axis, found %q", year)
		}
	}
}

func TestBusFactorRejectsARangeThatSelectsNothing(t *testing.T) {
	output := filepath.Join(t.TempDir(), "bus-factor.svg")
	start := midTickBegin.AddDate(10, 0, 0)

	err := BusFactor(newDatedBusFactorReader(midTickBegin.Unix()), output, &start, nil)
	if err == nil {
		t.Fatal("expected an error when the date range excludes every snapshot")
	}
}

func readRenderedFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected rendered output at %s: %v", path, err)
	}

	return string(content)
}

// datedBusFactorReader is a bus-factor reader with a real header, spanning
// enough ticks that the date axis has to print calendar years.
type datedBusFactorReader struct {
	*NoDataReader

	begin int64
	data  *readers.BusFactorData
}

func newDatedBusFactorReader(begin int64) *datedBusFactorReader {
	snapshots := make(map[int]readers.BusFactorSnapshot, 31)
	for tick := range 31 {
		snapshots[tick*30] = readers.BusFactorSnapshot{
			BusFactor:   tick%4 + 1,
			TotalLines:  int64(100 + tick*10),
			AuthorLines: map[int]int64{0: int64(80 + tick), 1: 20},
		}
	}

	return &datedBusFactorReader{
		NoDataReader: &NoDataReader{},
		begin:        begin,
		data: &readers.BusFactorData{
			People:    []string{"alice", "bob"},
			Snapshots: snapshots,
			Threshold: 0.8,
			TickSize:  testTickSizeNanos,
		},
	}
}

func (r *datedBusFactorReader) GetHeader() (int64, int64) {
	if r.begin == 0 {
		return 0, 0
	}

	return r.begin, r.begin + 31*30*24*60*60
}

func (r *datedBusFactorReader) GetBusFactor() (*readers.BusFactorData, error) {
	return r.data, nil
}
