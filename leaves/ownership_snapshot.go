package leaves

import (
	"errors"
	"fmt"
	"maps"
	"path"

	"github.com/cwbudde/hercules/internal/core"
)

var errOwnershipUnderflow = errors.New("incremental ownership total became negative")

// ownershipTotals is an immutable view of alive-line ownership at a point in time.
type ownershipTotals struct {
	TotalLines  int64
	AuthorLines map[int]int64
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
}

func (accumulator *ownershipSnapshotAccumulator) reset() {
	accumulator.lastTick = -1
	accumulator.totalLines = 0
	accumulator.authorLines = map[int]int64{}
	accumulator.fileLines = map[core.FileId]map[int]int64{}
	accumulator.resolver = nil
}

// consume returns the completed previous-tick snapshot, when this commit starts a later tick.
// The returned ownershipTotals owns its map and is not changed by later calls.
func (accumulator *ownershipSnapshotAccumulator) consume(
	tick int,
	changes core.LineHistoryChanges,
) (int, *ownershipTotals, error) {
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

	totals := accumulator.snapshot()

	return accumulator.lastTick, &totals
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

	return subsystems
}
