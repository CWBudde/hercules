package leaves

import (
	"errors"
	"fmt"
	"maps"
	"path"

	"github.com/go-git/go-git/v5"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	items "github.com/cwbudde/hercules/internal/plumbing"
)

const dependencyOwnershipSnapshot = "ownership_snapshot"

// errOwnershipUnresolvedMerge reports a scan which met a line still waiting for
// LineHistoryAnalyser.Merge() to resolve its owner. Such a line has no author yet, and crediting
// it to anybody would be wrong, so the run stops instead. It cannot happen by construction: a
// merge commit's replicas emit no changes, so no file is rescanned before Merge() has run.
var errOwnershipUnresolvedMerge = errors.New("ownership scan met an unresolved merge line")

// ownershipTotals is an immutable view of alive-line ownership at a point in time.
type ownershipTotals struct {
	TotalLines  int64
	AuthorLines map[int]int64
}

// ownershipSnapshotUpdate is produced once per commit and shared by all ownership metrics. State
// is the consuming branch's accumulator. The leaves keep the one handed to them by the last
// authoritative (non-replica) commit, which is the branch that survives to HEAD, and read the
// closed-tick series and the final totals from it in Finalize().
type ownershipSnapshotUpdate struct {
	State *ownershipSnapshotAccumulator
}

// ownershipSnapshotter is the single pipeline owner of alive-line ownership state, so enabling
// Bus Factor and Ownership Concentration together scans every file once.
//
// Ownership is read off the line-history trees, never summed from deltas. Every branch feeds
// LineHistoryChanges into the pipeline, and two branches which diverged before a file was
// rewritten each remove that file's lines from their own copy - summing those deltas into one
// accumulator removed the lines twice (PLAN.md B1c/B3). The trees do not have that problem: each
// branch's trees describe exactly that branch's alive lines, so a per-branch table filled from
// them is exact at every commit, and the branch which survives a merge is rebuilt from the merged
// trees.
type ownershipSnapshotter struct {
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

func (*ownershipSnapshotter) Configure(map[string]any) error {
	return nil
}

func (*ownershipSnapshotter) ConfigureUpstream(map[string]any) error {
	return nil
}

func (snapshotter *ownershipSnapshotter) Initialize(*git.Repository) error {
	snapshotter.ownership.reset()

	return nil
}

// Consume closes the previous tick when this commit starts a later one, then refreshes the
// ownership of every file the commit touched from its tree. A merge replica carries no changes,
// so it only closes ticks; Merge() rebuilds the table once the merge is resolved.
func (snapshotter *ownershipSnapshotter) Consume(
	deps map[string]any,
) (map[string]any, error) {
	reader := factReader{facts: deps}
	changes := readFact[core.LineHistoryChanges](&reader, linehistory.DependencyLineHistory)
	tick := readFact[int](&reader, items.DependencyTick)

	if reader.err != nil {
		return nil, reader.err
	}

	err := snapshotter.ownership.consume(tick, changes)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		dependencyOwnershipSnapshot: ownershipSnapshotUpdate{State: &snapshotter.ownership},
	}, nil
}

// Fork gives every new branch its own copy of the table. The origin keeps its branch slot.
func (snapshotter *ownershipSnapshotter) Fork(n int) []core.PipelineItem {
	clones := make([]core.PipelineItem, n)
	for index := range clones {
		clones[index] = &ownershipSnapshotter{ownership: snapshotter.ownership.clone()}
	}

	return clones
}

// Merge runs after LineHistoryAnalyser.Merge() has resolved the merge commit and synchronized
// every sibling to the merged trees. The receiver consumed the merge commit, but a merge replica
// emits no changes, so its table still describes the state before that commit; it is rebuilt from
// the trees here, before the next commit changes them again. Siblings do not consume again after
// a merge, but are rebuilt as well so that no branch can carry a stale table.
func (snapshotter *ownershipSnapshotter) Merge(items []core.PipelineItem) {
	snapshotter.ownership.mustRebuild()

	for _, item := range items {
		if sibling, ok := item.(*ownershipSnapshotter); ok && sibling != snapshotter {
			sibling.ownership.mustRebuild()
		}
	}
}

// ownershipSnapshotAccumulator holds one branch's alive-line ownership per file, the running
// totals derived from it, and the end-of-tick snapshots that branch has closed so far. It closes
// an occupied tick before applying the first commit from a later tick, so callers can never label
// future state as belonging to the previous tick.
type ownershipSnapshotAccumulator struct {
	lastTick    int
	totalLines  int64
	authorLines map[int]int64
	fileLines   map[core.FileId]map[int]int64
	resolver    core.FileIdResolver
	// series holds the closed end-of-tick snapshots. Values are never mutated, so a fork shares
	// them and the branch which survives a merge carries the lineage's series to HEAD.
	series             map[int]*ownershipTotals
	finalTotals        *ownershipTotals
	finalSubsystems    map[string]map[int]int64
	finalSubsystemsSet bool
}

func (accumulator *ownershipSnapshotAccumulator) reset() {
	accumulator.lastTick = -1
	accumulator.totalLines = 0
	accumulator.authorLines = map[int]int64{}
	accumulator.fileLines = map[core.FileId]map[int]int64{}
	accumulator.resolver = nil
	accumulator.series = map[int]*ownershipTotals{}
	accumulator.invalidateFinals()
}

func (accumulator *ownershipSnapshotAccumulator) invalidateFinals() {
	accumulator.finalTotals = nil
	accumulator.finalSubsystems = nil
	accumulator.finalSubsystemsSet = false
}

// clone copies the per-file table and shares the immutable closed snapshots.
func (accumulator *ownershipSnapshotAccumulator) clone() ownershipSnapshotAccumulator {
	clone := *accumulator
	clone.authorLines = maps.Clone(accumulator.authorLines)
	clone.fileLines = make(map[core.FileId]map[int]int64, len(accumulator.fileLines))

	for id, lines := range accumulator.fileLines {
		clone.fileLines[id] = maps.Clone(lines)
	}

	clone.series = maps.Clone(accumulator.series)
	clone.invalidateFinals()

	return clone
}

// consume closes the previous tick when this commit starts a later one, then rescans every file
// the commit touched. The closing snapshot is taken before the rescans, so it describes the state
// after the previous tick's last commit and before this one.
func (accumulator *ownershipSnapshotAccumulator) consume(
	tick int,
	changes core.LineHistoryChanges,
) error {
	accumulator.invalidateFinals()
	accumulator.resolver = changes.Resolver

	if accumulator.lastTick >= 0 && tick > accumulator.lastTick {
		accumulator.series[accumulator.lastTick] = accumulator.snapshot()
	}

	if accumulator.lastTick < 0 || tick > accumulator.lastTick {
		accumulator.lastTick = tick
	}

	touched := map[core.FileId]struct{}{}

	for _, change := range changes.Changes {
		if _, seen := touched[change.FileId]; seen {
			continue
		}

		touched[change.FileId] = struct{}{}

		err := accumulator.rescan(change.FileId)
		if err != nil {
			return err
		}
	}

	return nil
}

// rescan replaces one file's ownership with what its tree holds now. A file the resolver no
// longer knows has been deleted and leaves the table. Rescanning an unchanged file is a no-op, so
// touching a superset of the changed files is harmless.
func (accumulator *ownershipSnapshotAccumulator) rescan(id core.FileId) error {
	if accumulator.resolver == nil {
		return nil
	}

	lines, live, err := scanFileOwnership(accumulator.resolver, id)
	if err != nil {
		return fmt.Errorf("file %d: %w", id, err)
	}

	for author, count := range accumulator.fileLines[id] {
		accumulator.addAuthorLines(author, -count)
	}

	delete(accumulator.fileLines, id)

	if !live || len(lines) == 0 {
		return nil
	}

	accumulator.fileLines[id] = lines

	for author, count := range lines {
		accumulator.addAuthorLines(author, count)
	}

	return nil
}

func (accumulator *ownershipSnapshotAccumulator) addAuthorLines(author int, delta int64) {
	accumulator.totalLines += delta

	if total := accumulator.authorLines[author] + delta; total == 0 {
		delete(accumulator.authorLines, author)
	} else {
		accumulator.authorLines[author] = total
	}
}

// rebuild discards the table and reads every live file off the trees.
func (accumulator *ownershipSnapshotAccumulator) rebuild() error {
	accumulator.invalidateFinals()
	accumulator.totalLines = 0
	accumulator.authorLines = map[int]int64{}
	accumulator.fileLines = map[core.FileId]map[int]int64{}

	if accumulator.resolver == nil {
		return nil
	}

	var firstErr error

	accumulator.resolver.ForEachFile(func(id core.FileId, _ string) {
		if firstErr == nil {
			firstErr = accumulator.rescan(id)
		}
	})

	return firstErr
}

// mustRebuild is rebuild for Merge(), which has no error channel. The only failure is an
// unresolved merge line, which LineHistoryAnalyser.Merge() guarantees cannot remain.
func (accumulator *ownershipSnapshotAccumulator) mustRebuild() {
	err := accumulator.rebuild()
	if err != nil {
		panic(fmt.Sprintf("ownership snapshot: rebuild after merge: %v", err))
	}
}

// snapshot copies the running totals. Every count is positive by construction, because each
// per-file histogram is read off a tree rather than summed from deltas.
func (accumulator *ownershipSnapshotAccumulator) snapshot() *ownershipTotals {
	return &ownershipTotals{
		TotalLines:  accumulator.totalLines,
		AuthorLines: maps.Clone(accumulator.authorLines),
	}
}

// closedSnapshots returns the end-of-tick snapshots this branch's lineage has closed, keyed by
// tick. The last occupied tick is not in it; finalSnapshot() supplies that one.
func (accumulator *ownershipSnapshotAccumulator) closedSnapshots() map[int]*ownershipTotals {
	return accumulator.series
}

func (accumulator *ownershipSnapshotAccumulator) finalSnapshot() (int, *ownershipTotals) {
	if accumulator.lastTick < 0 {
		return -1, nil
	}

	if accumulator.finalTotals == nil {
		accumulator.finalTotals = accumulator.snapshot()
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
// ownership analyses. It derives from the per-file table, so subsystem totals reconcile with the
// final global snapshot.
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
			directoryAuthors[author] += lines
		}
	}

	accumulator.finalSubsystems = subsystems

	return subsystems
}

// scanFileOwnership reads one file's alive-line ownership off its line-history tree.
//
// ScanFile yields run boundaries, not lines: each callback carries the first line of a run and
// that run's owner, and the last one is the TreeEnd sentinel (author -1, tick TreeMergeMark). A
// run's length is therefore the distance to the next boundary and belongs to the previous
// callback's author. Runs owned by core.AuthorMissing - identities no --people-dict entry matched
// - are not counted, which keeps the definition the delta-based accounting used. The second
// result is false when id is not a live file.
func scanFileOwnership(
	resolver core.FileIdResolver, id core.FileId,
) (map[int]int64, bool, error) {
	lines := map[int]int64{}
	previousLine := 0
	previousAuthor := -1

	var err error

	live := resolver.ScanFile(id, func(line int, tick core.TickNumber, author core.AuthorId) {
		if length := line - previousLine; length > 0 && previousAuthor >= 0 {
			lines[previousAuthor] += int64(length)
		}

		previousLine = line

		switch {
		case author < 0 || author >= core.AuthorMissing:
			previousAuthor = -1
		case tick == linehistory.TreeMergeMark:
			err = errOwnershipUnresolvedMerge
			previousAuthor = -1
		default:
			previousAuthor = int(author)
		}
	})

	if err != nil {
		return nil, live, err
	}

	return lines, live, nil
}

var _ = core.RegisterPipelineItem(&ownershipSnapshotter{})
