package leaves

import (
	"fmt"
	"path"

	"github.com/go-git/go-git/v5"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	items "github.com/cwbudde/hercules/internal/plumbing"
)

const dependencyOwnershipSnapshot = "ownership_snapshot"

// ownershipTotals is an immutable view of alive-line ownership at a point in time.
type ownershipTotals struct {
	TotalLines  int64
	AuthorLines map[int]int64
}

// ownershipSnapshotUpdate is produced once per commit and shared by all ownership metrics.
// ClosedTotals is non-nil only when the commit closes a preceding occupied tick. State points to
// the live accumulator and lets leaves obtain the final tick and subsystem totals after replay.
type ownershipSnapshotUpdate struct {
	ClosedTick   int
	ClosedTotals *ownershipTotals
	State        *ownershipSnapshotAccumulator
}

// ownershipSnapshotter is the single pipeline owner of incremental alive-line ownership state.
// Keeping this work in plumbing means enabling Bus Factor and Ownership Concentration together
// does not apply the same ownership changes twice.
type ownershipSnapshotter struct {
	core.NoopMerger

	l         core.Logger
	ownership ownershipSnapshotAccumulator
}

func (*ownershipSnapshotter) Name() string {
	return "OwnershipSnapshot"
}

func (*ownershipSnapshotter) Provides() []string {
	return []string{dependencyOwnershipSnapshot}
}

func (*ownershipSnapshotter) Requires() []string {
	return []string{
		linehistory.DependencyLineHistory,
		items.DependencyTick,
	}
}

func (*ownershipSnapshotter) ListConfigurationOptions() []core.ConfigurationOption {
	return nil
}

func (snapshotter *ownershipSnapshotter) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		snapshotter.l = l
	}

	return nil
}

func (*ownershipSnapshotter) ConfigureUpstream(map[string]any) error {
	return nil
}

func (snapshotter *ownershipSnapshotter) Initialize(*git.Repository) error {
	snapshotter.ownership.reset()

	if snapshotter.l == nil {
		snapshotter.l = core.NewLogger()
	}

	return nil
}

func (snapshotter *ownershipSnapshotter) Consume(
	deps map[string]any,
) (map[string]any, error) {
	reader := factReader{facts: deps}
	changes := readFact[core.LineHistoryChanges](&reader, linehistory.DependencyLineHistory)
	tick := readFact[int](&reader, items.DependencyTick)

	if reader.err != nil {
		return nil, reader.err
	}

	closedTick, closedTotals := snapshotter.ownership.consume(tick, changes)
	snapshotter.reportDivergence()

	return map[string]any{
		dependencyOwnershipSnapshot: ownershipSnapshotUpdate{
			ClosedTick:   closedTick,
			ClosedTotals: closedTotals,
			State:        &snapshotter.ownership,
		},
	}, nil
}

func (snapshotter *ownershipSnapshotter) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(snapshotter, n)
}

// reportDivergence says once that this history drives ownership totals below zero, and why.
// Saying it every time would be one line per commit on a repository with a long-lived branch.
func (snapshotter *ownershipSnapshotter) reportDivergence() {
	report := snapshotter.ownership.divergence
	if report == nil || report.reported {
		return
	}

	report.reported = true

	if snapshotter.l == nil {
		return
	}

	snapshotter.l.Warnf(
		"ownership of %s went to %d lines. Divergent branches remove the same lines from their "+
			"own copy of a file, and every branch feeds this one accumulator, so a total can dip "+
			"below zero until the branches merge and the duplication cancels. Snapshots report "+
			"zero for such a total; further occurrences are not reported.\n",
		report.scope, report.value,
	)
}

// ownershipSnapshotAccumulator incrementally follows LineHistoryChanges. It closes an occupied
// tick before applying the first commit from a later tick, so callers can never label future state
// as belonging to the previous tick.
type ownershipSnapshotAccumulator struct {
	lastTick    int
	totalLines  int64
	authorLines map[int]int64
	fileLines   map[core.FileId]map[int]int64
	resolver    core.FileIdResolver
	// divergence records the first total this history drove below zero, if any. Totals stay signed
	// internally so the branch that duplicated the removal can still cancel it out later; only the
	// snapshots handed to the metrics are clamped.
	divergence         *ownershipDivergence
	finalTotals        *ownershipTotals
	finalSubsystems    map[string]map[int]int64
	finalSubsystemsSet bool
}

// ownershipDivergence describes the first negative ownership total of a run.
type ownershipDivergence struct {
	scope    string
	value    int64
	reported bool
}

func (accumulator *ownershipSnapshotAccumulator) reset() {
	accumulator.lastTick = -1
	accumulator.totalLines = 0
	accumulator.authorLines = map[int]int64{}
	accumulator.fileLines = map[core.FileId]map[int]int64{}
	accumulator.resolver = nil
	accumulator.divergence = nil
	accumulator.finalTotals = nil
	accumulator.finalSubsystems = nil
	accumulator.finalSubsystemsSet = false
}

// consume returns the completed previous-tick snapshot, when this commit starts a later tick.
// The returned ownershipTotals owns its map and is not changed by later calls.
func (accumulator *ownershipSnapshotAccumulator) consume(
	tick int,
	changes core.LineHistoryChanges,
) (int, *ownershipTotals) {
	accumulator.finalTotals = nil
	accumulator.finalSubsystems = nil
	accumulator.finalSubsystemsSet = false

	closedTick := -1
	var closedTotals *ownershipTotals

	if accumulator.lastTick >= 0 && tick > accumulator.lastTick {
		closedTick = accumulator.lastTick
		totals := accumulator.snapshot()
		closedTotals = &totals
	}

	if accumulator.lastTick < 0 || tick > accumulator.lastTick {
		accumulator.lastTick = tick
	}

	accumulator.resolver = changes.Resolver

	for _, change := range changes.Changes {
		accumulator.apply(change)
	}

	return closedTick, closedTotals
}

// apply folds one line-history change into the running totals.
//
// The arithmetic is deliberately signed. Every branch feeds this one accumulator, and two branches
// which diverged before a file was rewritten each remove that file's lines from their own copy of
// it - so the same lines are removed twice and a total dips below zero until the branches merge and
// the duplicate cancels out. Refusing the negative would abort the run on a legitimate history;
// clamping it here would destroy the compensating positive and leave every later total too high.
// Only the snapshots handed to the metrics are clamped, in snapshot() and subsystemOwnership().
func (accumulator *ownershipSnapshotAccumulator) apply(change core.LineHistoryChange) {
	if change.IsDelete() || change.Delta == 0 || change.PrevAuthor == core.AuthorMissing {
		return
	}

	author := int(change.PrevAuthor)
	delta := int64(change.Delta)

	fileAuthors := accumulator.fileLines[change.FileId]
	if fileAuthors == nil {
		fileAuthors = map[int]int64{}
	}

	accumulator.totalLines = accumulator.add(accumulator.totalLines, delta, "the repository")
	setOwnershipCount(accumulator.authorLines, author, accumulator.add(
		accumulator.authorLines[author], delta, fmt.Sprintf("author %d", author),
	))
	setOwnershipCount(fileAuthors, author, accumulator.add(
		fileAuthors[author], delta, fmt.Sprintf("file %d author %d", change.FileId, author),
	))

	if len(fileAuthors) == 0 {
		delete(accumulator.fileLines, change.FileId)
	} else {
		accumulator.fileLines[change.FileId] = fileAuthors
	}
}

// add sums a delta into a total and remembers the first one that goes negative.
func (accumulator *ownershipSnapshotAccumulator) add(current, delta int64, scope string) int64 {
	total := current + delta
	if total < 0 && accumulator.divergence == nil {
		accumulator.divergence = &ownershipDivergence{scope: scope, value: total}
	}

	return total
}

func setOwnershipCount(counts map[int]int64, author int, lines int64) {
	if lines == 0 {
		delete(counts, author)
	} else {
		counts[author] = lines
	}
}

// snapshot is the reporting boundary: nobody downstream can do anything sensible with a negative
// number of alive lines, so a total which branch divergence drove below zero is reported as zero
// and an author left with nothing is left out entirely. The accumulator keeps the signed values.
func (accumulator *ownershipSnapshotAccumulator) snapshot() ownershipTotals {
	authorLines := make(map[int]int64, len(accumulator.authorLines))

	for author, lines := range accumulator.authorLines {
		if lines > 0 {
			authorLines[author] = lines
		}
	}

	return ownershipTotals{
		TotalLines:  max(accumulator.totalLines, 0),
		AuthorLines: authorLines,
	}
}

func (accumulator *ownershipSnapshotAccumulator) finalSnapshot() (int, *ownershipTotals) {
	if accumulator.lastTick < 0 {
		return -1, nil
	}

	if accumulator.finalTotals == nil {
		totals := accumulator.snapshot()
		accumulator.finalTotals = &totals
	}

	return accumulator.lastTick, accumulator.finalTotals
}

func subsystemDirectory(fileName string) string {
	directory := path.Dir(fileName)
	if directory == "." {
		return "/"
	}

	return directory
}

// subsystemOwnership returns final ownership grouped by the same directory keys emitted by both
// ownership analyses. It derives from the per-file accumulator, so subsystem totals reconcile with
// the final global snapshot.
func (accumulator *ownershipSnapshotAccumulator) subsystemOwnership() map[string]map[int]int64 {
	if accumulator.finalSubsystemsSet {
		return accumulator.finalSubsystems
	}

	accumulator.finalSubsystemsSet = true
	if accumulator.resolver == nil {
		return nil
	}

	subsystems := map[string]map[int]int64{}

	for fileID, fileAuthors := range accumulator.fileLines {
		fileName := accumulator.resolver.NameOf(fileID)
		if fileName == "" {
			continue
		}

		directory := subsystemDirectory(fileName)

		directoryAuthors := subsystems[directory]
		if directoryAuthors == nil {
			directoryAuthors = map[int]int64{}
			subsystems[directory] = directoryAuthors
		}

		for author, lines := range fileAuthors {
			if lines > 0 {
				directoryAuthors[author] += lines
			}
		}
	}

	accumulator.finalSubsystems = subsystems

	return subsystems
}

var _ = core.RegisterPipelineItem(&ownershipSnapshotter{})
