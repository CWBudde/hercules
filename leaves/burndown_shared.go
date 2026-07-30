package leaves

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/cwbudde/hercules/internal/burndown"
	"github.com/cwbudde/hercules/internal/core"
)

const (
	// ConfigBurndownGranularity is the name of the option to set BurndownAnalysis.Granularity.
	ConfigBurndownGranularity = "Burndown.Granularity"
	// ConfigBurndownSampling is the name of the option to set BurndownAnalysis.Sampling.
	ConfigBurndownSampling = "Burndown.Sampling"
	// ConfigBurndownTrackFiles enables burndown collection for files.
	ConfigBurndownTrackFiles = "Burndown.TrackFiles"
	// ConfigBurndownTrackPeople enables burndown collection for authors.
	ConfigBurndownTrackPeople = "Burndown.TrackPeople"
	// ConfigBurndownStrictBalances makes a negative burndown balance abort the run instead of
	// being reported once as a warning.
	ConfigBurndownStrictBalances = "Burndown.StrictBalances"
	// DefaultBurndownGranularity is the default number of ticks for BurndownAnalysis.Granularity
	// and BurndownAnalysis.Sampling.
	DefaultBurndownGranularity = 30
	// authorSelf is the internal author index which is used in BurndownAnalysis.Finalize() to
	// format the author overwrites matrix.
	authorSelf = core.AuthorMissing + 1
	// ConfigBurndownHibernationDisk enables writing hibernated burndown data to disk.
	ConfigBurndownHibernationDisk = "Burndown.HibernationDisk"
	// ConfigBurndownHibernationDir is the temp directory for hibernated burndown data.
	ConfigBurndownHibernationDir = "Burndown.HibernationDir"
)

var errNegativeBurndownBalance = errors.New("negative burndown balance")

type negativeBurndownBalanceError struct {
	Operation string
	Scope     string
	Name      string
	Tick      int
	AgeBand   int
	Row       int
	Column    int
	Value     int64
}

func (err *negativeBurndownBalanceError) Error() string {
	return fmt.Sprintf(
		"%s: %s %q became %d at tick %d (row %d), age band %d (column %d) during %s",
		errNegativeBurndownBalance,
		err.Scope,
		err.Name,
		err.Value,
		err.Tick,
		err.Row,
		err.AgeBand,
		err.Column,
		err.Operation,
	)
}

func (err *negativeBurndownBalanceError) Unwrap() error {
	return errNegativeBurndownBalance
}

// burndownBalanceReport collects every cell in which a burndown matrix claims a negative number
// of alive lines. Such a cell is always an accounting defect - a repository cannot make one
// happen - but the defect predates this release and is not yet fixed, so the report exists to
// describe the damage rather than to stop at the first sign of it.
type burndownBalanceReport struct {
	// first is the offending cell in scan order. Strict mode reports this one, so that the
	// message stays stable regardless of how many other cells are affected.
	first *negativeBurndownBalanceError
	// worst is the most negative cell, which says far more about the scale of the problem.
	worst  *negativeBurndownBalanceError
	cells  int
	scopes map[string]int
}

func (report *burndownBalanceReport) add(err *negativeBurndownBalanceError) {
	report.cells++

	if report.scopes == nil {
		report.scopes = map[string]int{}
	}

	report.scopes[err.Scope]++

	if report.first == nil {
		report.first = err
	}

	if report.worst == nil || err.Value < report.worst.Value {
		report.worst = err
	}
}

// summary describes the worst cell plus the overall extent, for the single warning a
// non-strict run emits.
func (report *burndownBalanceReport) summary() string {
	return fmt.Sprintf(
		"%s; %d cell(s) affected in total (%s). The matrix is reported as computed - "+
			"pass --strict-burndown-balances to fail the run instead.",
		report.worst, report.cells, report.breakdown(),
	)
}

func (report *burndownBalanceReport) breakdown() string {
	parts := make([]string, 0, len(report.scopes))
	for _, scope := range sortedKeys(report.scopes) {
		parts = append(parts, fmt.Sprintf("%s: %d", scope, report.scopes[scope]))
	}

	return strings.Join(parts, ", ")
}

// reportBurndownBalances applies the configured policy to a result which may contain negative
// balances. Aborting a multi-hour run over a single -1 throws away every other analysis it
// produced, so by default the run continues and warns; --strict-burndown-balances restores the
// hard failure for anyone bisecting the accounting itself.
//
// reported, when not nil, limits the warning to once per analyser. The same result passes through
// several checks (finalization, then serialization, then any merge it takes part in), and
// repeating an identical diagnostic at each one is noise.
func reportBurndownBalances(
	logger core.Logger, strict bool, reported *bool, result *BurndownResult, operation string,
) error {
	if !strict && reported != nil && *reported {
		return nil
	}

	report := auditBurndownResultBalances(result, operation)
	if report.cells == 0 {
		return nil
	}

	if strict {
		return report.first
	}

	if logger == nil {
		logger = core.NewLogger()
	}

	if reported != nil {
		*reported = true
	}

	logger.Warn(report.summary())

	return nil
}

func auditBurndownResultBalances(
	result *BurndownResult, operation string,
) *burndownBalanceReport {
	report := &burndownBalanceReport{}

	auditBurndownHistory(report, result.GlobalHistory, result, operation, "project", "project")

	for _, name := range sortedKeys(result.FileHistories) {
		auditBurndownHistory(report, result.FileHistories[name], result, operation, "file", name)
	}

	for index, history := range result.PeopleHistories {
		name := indexedBurndownName(result.reversedPeopleDict, index)
		auditBurndownHistory(report, history, result, operation, "person", name)
	}

	for index, history := range result.RepositoryHistories {
		name := indexedBurndownName(result.ReversedRepositoryDict, index)
		auditBurndownHistory(report, history, result, operation, "repository", name)
	}

	return report
}

func auditBurndownHistory(
	report *burndownBalanceReport,
	history burndown.DenseHistory,
	result *BurndownResult,
	operation, scope, name string,
) {
	for row, values := range history {
		for column, value := range values {
			if value >= 0 {
				continue
			}

			report.add(&negativeBurndownBalanceError{
				Operation: operation,
				Scope:     scope,
				Name:      name,
				Tick:      row * result.sampling,
				AgeBand:   column * result.granularity,
				Row:       row,
				Column:    column,
				Value:     value,
			})
		}
	}
}

func indexedBurndownName(names []string, index int) string {
	if index < len(names) {
		return names[index]
	}

	return fmt.Sprintf("#%d", index)
}

func burndownSharedOptions() []core.ConfigurationOption {
	return []core.ConfigurationOption{{
		Name:        ConfigBurndownGranularity,
		Description: "How many time ticks there are in a single band.",
		Flag:        "granularity",
		Type:        core.IntConfigurationOption,
		Shared:      true,
		Default:     DefaultBurndownGranularity,
	}, {
		Name:        ConfigBurndownSampling,
		Description: "How frequently to record the state in time ticks.",
		Flag:        "sampling",
		Type:        core.IntConfigurationOption,
		Shared:      true,
		Default:     DefaultBurndownGranularity,
	}, {
		Name:        ConfigBurndownTrackFiles,
		Description: "Record detailed statistics per each file.",
		Flag:        "burndown-files",
		Type:        core.BoolConfigurationOption,
		Shared:      true,
		Default:     false,
	}, {
		Name:        ConfigBurndownTrackPeople,
		Description: "Record detailed statistics per each developer.",
		Flag:        "burndown-people",
		Type:        core.BoolConfigurationOption,
		Shared:      true,
		Default:     false,
	}, {
		Name: ConfigBurndownStrictBalances,
		Description: "Abort when a burndown matrix contains a negative number of alive lines. " +
			"Off by default: such cells are a known accounting defect which predates this " +
			"release, and failing the run discards every other analysis it computed.",
		Flag:    "strict-burndown-balances",
		Type:    core.BoolConfigurationOption,
		Shared:  true,
		Default: false,
	}}
}

// BurndownResult carries the result of running BurndownAnalysis - it is returned by
// BurndownAnalysis.Finalize().
type BurndownResult struct {
	// [number of samples][number of bands]
	// The number of samples depends on Sampling: the less Sampling, the bigger the number.
	// The number of bands depends on Granularity: the less Granularity, the bigger the number.
	GlobalHistory burndown.DenseHistory
	// The key is a path inside the Git repository. The value's dimensions are the same as
	// in GlobalHistory.
	FileHistories map[string]burndown.DenseHistory
	// The key is a path inside the Git repository. The value is a mapping from developer indexes
	// (see reversedPeopleDict) and the owned line numbers. Their sum equals to the total number of
	// lines in the file.
	FileOwnership map[string]map[int]int
	// [number of people][number of samples][number of bands]
	PeopleHistories []burndown.DenseHistory
	// [number of people][number of people + 2]
	// The first element is the total number of lines added by the author.
	// The second element is the number of removals by unidentified authors (outside reversedPeopleDict).
	// The rest of the elements are equal the number of line removals by the corresponding
	// authors in reversedPeopleDict: 2 -> 0, 3 -> 1, etc.
	PeopleMatrix burndown.DenseHistory
	// [number of repositories][number of samples][number of bands]
	// Per-repository burndown histories, similar to PeopleHistories but for repositories.
	// This is populated during combine operations or for single-repo analyses.
	RepositoryHistories []burndown.DenseHistory

	// The following members are private.

	// reversedPeopleDict is borrowed from IdentityDetector and becomes available after
	// Pipeline.Initialize(facts map[string]interface{}). Thus it can be obtained via
	// facts[FactIdentityDetectorReversedPeopleDict].
	reversedPeopleDict []string
	// ReversedRepositoryDict contains repository names in the same order as RepositoryHistories.
	// For single-repo analyses, this contains one element with the repository name.
	// For combined analyses, it contains all repository names.
	// This field is exported to allow initialization during combine operations.
	ReversedRepositoryDict []string
	// TickSize references TicksSinceStart.TickSize
	tickSize time.Duration
	// sampling and granularity are copied from BurndownAnalysis and stored for service purposes
	// such as merging several results together.
	sampling    int
	granularity int
}

// GetTickSize returns the tick size used to generate this burndown analysis result.
func (br BurndownResult) GetTickSize() time.Duration {
	return br.tickSize
}

// GetIdentities returns the list of developer identities used to generate this burndown analysis result.
// The format is |-joined keys, see internals/plumbing/identity for details.
func (br BurndownResult) GetIdentities() []string {
	return br.reversedPeopleDict
}

type sparseHistoryEntry struct {
	deltas map[int]int64
}

func newSparseHistoryEntry() sparseHistoryEntry {
	return sparseHistoryEntry{
		deltas: map[int]int64{},
	}
}

type sparseHistory map[int]sparseHistoryEntry

func (p sparseHistory) updateDelta(prevTick, curTick, delta int) {
	currentHistory, ok := p[curTick]
	if !ok {
		currentHistory = newSparseHistoryEntry()
		p[curTick] = currentHistory
	}

	currentHistory.deltas[prevTick] += int64(delta)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

func checkClose(c io.Closer) {
	err := c.Close()
	if err != nil {
		panic(err)
	}
}
