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

var errOwnershipUnderflow = errors.New("incremental ownership total became negative")

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

func (snapshotter *ownershipSnapshotter) Consume(
	deps map[string]any,
) (map[string]any, error) {
	reader := factReader{facts: deps}
	changes := readFact[core.LineHistoryChanges](&reader, linehistory.DependencyLineHistory)
	tick := readFact[int](&reader, items.DependencyTick)

	if reader.err != nil {
		return nil, reader.err
	}

	closedTick, closedTotals, err := snapshotter.ownership.consume(tick, changes)
	if err != nil {
		return nil, fmt.Errorf("update ownership snapshot: %w", err)
	}

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

// ownershipSnapshotAccumulator incrementally follows LineHistoryChanges. It closes an occupied
// tick before applying the first commit from a later tick, so callers can never label future state
// as belonging to the previous tick.
type ownershipSnapshotAccumulator struct {
	lastTick           int
	totalLines         int64
	authorLines        map[int]int64
	fileLines          map[core.FileId]map[int]int64
	resolver           core.FileIdResolver
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
	accumulator.finalTotals = nil
	accumulator.finalSubsystems = nil
	accumulator.finalSubsystemsSet = false
}

// consume returns the completed previous-tick snapshot, when this commit starts a later tick.
// The returned ownershipTotals owns its map and is not changed by later calls.
func (accumulator *ownershipSnapshotAccumulator) consume(
	tick int,
	changes core.LineHistoryChanges,
) (int, *ownershipTotals, error) {
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
		err := accumulator.apply(change)
		if err != nil {
			return -1, nil, err
		}
	}

	return closedTick, closedTotals, nil
}

func (accumulator *ownershipSnapshotAccumulator) apply(change core.LineHistoryChange) error {
	if change.IsDelete() || change.Delta == 0 || change.PrevAuthor == core.AuthorMissing {
		return nil
	}

	author := int(change.PrevAuthor)
	delta := int64(change.Delta)

	nextAuthorLines, err := checkedOwnershipTotal(
		accumulator.authorLines[author],
		delta,
		fmt.Sprintf("author %d", author),
	)
	if err != nil {
		return err
	}

	fileAuthors := accumulator.fileLines[change.FileId]
	if fileAuthors == nil {
		fileAuthors = map[int]int64{}
	}

	nextFileLines, err := checkedOwnershipTotal(
		fileAuthors[author],
		delta,
		fmt.Sprintf("file %d author %d", change.FileId, author),
	)
	if err != nil {
		return err
	}

	nextTotalLines, err := checkedOwnershipTotal(accumulator.totalLines, delta, "repository")
	if err != nil {
		return err
	}

	accumulator.totalLines = nextTotalLines
	setOwnershipCount(accumulator.authorLines, author, nextAuthorLines)
	setOwnershipCount(fileAuthors, author, nextFileLines)

	if len(fileAuthors) == 0 {
		delete(accumulator.fileLines, change.FileId)
	} else {
		accumulator.fileLines[change.FileId] = fileAuthors
	}

	return nil
}

func setOwnershipCount(counts map[int]int64, author int, lines int64) {
	if lines == 0 {
		delete(counts, author)
	} else {
		counts[author] = lines
	}
}

func checkedOwnershipTotal(current, delta int64, scope string) (int64, error) {
	total := current + delta
	if total < 0 {
		return 0, fmt.Errorf(
			"%w: %s has %d lines after delta %d",
			errOwnershipUnderflow,
			scope,
			total,
			delta,
		)
	}

	return total, nil
}

func (accumulator *ownershipSnapshotAccumulator) snapshot() ownershipTotals {
	authorLines := make(map[int]int64, len(accumulator.authorLines))
	maps.Copy(authorLines, accumulator.authorLines)

	return ownershipTotals{
		TotalLines:  accumulator.totalLines,
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
			directoryAuthors[author] += lines
		}
	}

	accumulator.finalSubsystems = subsystems

	return subsystems
}

var _ = core.RegisterPipelineItem(&ownershipSnapshotter{})
