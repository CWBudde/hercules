package plumbing

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/cwbudde/hercules/internal/core"
)

// truncationReport announces that a wall-clock deadline changed the analysis.
//
// Deadlines are the remaining source of run-to-run variation once the rename matching race is
// gone (PLAN.md B12): whether one fires depends on how fast the machine is, and when it does,
// the result differs from an untruncated run. That is a legitimate trade — the alternative is
// an unbounded diff on a pathological file — but it must not be silent, or a later measurement
// gets compared against numbers that were never comparable.
//
// The first occurrence warns; the rest are counted and flushed once at the end of the run,
// following the same shape as oversizeReport in blob_cache.go. The mutex is insurance: branch
// execution is sequential today, but this package runs under -race.
type truncationReport struct {
	mutex      sync.Mutex
	subject    string
	advice     string
	reported   bool
	summarised bool
	counts     map[string]int
}

func newTruncationReport(subject, advice string) *truncationReport {
	return &truncationReport{subject: subject, advice: advice, counts: map[string]int{}}
}

// record counts one truncation of the given kind and warns on the first one overall.
func (report *truncationReport) record(logger core.Logger, kind string) {
	report.mutex.Lock()

	if report.counts == nil {
		report.counts = map[string]int{}
	}

	report.counts[kind]++

	first := !report.reported
	report.reported = true

	report.mutex.Unlock()

	if !first {
		return
	}

	if logger == nil {
		logger = core.NewLogger()
	}

	logger.Warnf(
		"%s hit its time budget and stopped early (%s), so its result depends on how fast this "+
			"machine is and this run's output is not reproducible. %s\n",
		report.subject, kind, report.advice,
	)
}

// summarise emits the end-of-run tally, once, and only when there was more than the one
// occurrence the warning already covered.
func (report *truncationReport) summarise(logger core.Logger) {
	report.mutex.Lock()

	total := 0
	for _, count := range report.counts {
		total += count
	}

	if report.summarised || total <= 1 {
		report.mutex.Unlock()

		return
	}

	report.summarised = true
	kinds := make([]string, 0, len(report.counts))

	for kind := range report.counts {
		kinds = append(kinds, kind)
	}

	sort.Strings(kinds)

	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		parts = append(parts, fmt.Sprintf("%s: %d", kind, report.counts[kind]))
	}

	report.mutex.Unlock()

	if logger == nil {
		logger = core.NewLogger()
	}

	logger.Warnf(
		"%s hit its time budget %d time(s) in total (%s); this run's output is not reproducible.\n",
		report.subject, total, strings.Join(parts, ", "),
	)
}
