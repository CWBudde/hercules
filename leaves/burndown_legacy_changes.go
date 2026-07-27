package leaves

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	items "github.com/cwbudde/hercules/internal/plumbing"
)

func (analyser *LegacyBurndownAnalysis) onNewTick() {
	if analyser.tick > analyser.previousTick {
		analyser.previousTick = analyser.tick
	}

	analyser.mergedAuthor = core.AuthorMissing
}

func (analyser *LegacyBurndownAnalysis) updateGlobal(_ *linehistory.File, currentTime, previousTime, delta int) {
	_, curTick := analyser.unpackPersonWithTick(currentTime)
	_, prevTick := analyser.unpackPersonWithTick(previousTime)

	analyser.globalHistory.updateDelta(prevTick, curTick, delta)
}

// updateFile is bound to the specific `history` in the closure.
func (analyser *LegacyBurndownAnalysis) updateFile(
	history sparseHistory, currentTime, previousTime, delta int,
) {
	_, curTick := analyser.unpackPersonWithTick(currentTime)
	_, prevTick := analyser.unpackPersonWithTick(previousTime)

	history.updateDelta(prevTick, curTick, delta)
}

func (analyser *LegacyBurndownAnalysis) updateAuthor(_ *linehistory.File, currentTime, previousTime, delta int) {
	prevAuthor, prevTick := analyser.unpackPersonWithTick(previousTime)
	if prevAuthor == core.AuthorMissing {
		return
	}

	newAuthor, curTick := analyser.unpackPersonWithTick(currentTime)
	if delta > 0 && newAuthor != prevAuthor {
		analyser.l.Errorf("insertion must have the same author (%d, %d)", prevAuthor, newAuthor)

		delta = 0
	}

	history := analyser.peopleHistories[prevAuthor]
	if history == nil {
		history = sparseHistory{}
		analyser.peopleHistories[prevAuthor] = history
	}

	history.updateDelta(prevTick, curTick, delta)
}

func (analyser *LegacyBurndownAnalysis) updateChurnMatrix(_ *linehistory.File, currentTime, previousTime, delta int) {
	newAuthor, _ := analyser.unpackPersonWithTick(currentTime)
	prevAuthor, _ := analyser.unpackPersonWithTick(previousTime)

	if prevAuthor == core.AuthorMissing {
		return
	}

	if delta > 0 {
		if newAuthor != prevAuthor {
			analyser.l.Errorf("insertion must have the same author (%d, %d)", prevAuthor, newAuthor)

			delta = 0
		}

		newAuthor = authorSelf
	}

	row := analyser.matrix[prevAuthor]
	if row == nil {
		row = map[int]int64{}
		analyser.matrix[prevAuthor] = row
	}

	row[newAuthor] += int64(delta)
}

func (analyser *LegacyBurndownAnalysis) newFile(
	name string, author, tick, size int,
) *linehistory.File {
	updaters := make([]linehistory.Updater, 1, 4)

	updaters[0] = analyser.updateGlobal
	if analyser.TrackFiles {
		history := analyser.fileHistories[name]
		if history == nil {
			history = analyser.deletedFileHistories[name]
			if history == nil {
				// can be not nil if the file was created in a future branch
				history = sparseHistory{}
			}
		}

		analyser.fileHistories[name] = history
		delete(analyser.deletedFileHistories, name)

		updaters = append(updaters, func(_ *linehistory.File, currentTime, previousTime, delta int) {
			analyser.updateFile(history, currentTime, previousTime, delta)
		})
	}

	if analyser.PeopleNumber > 0 {
		updaters = append(updaters, analyser.updateAuthor)
		updaters = append(updaters, analyser.updateChurnMatrix)
		tick = analyser.packChangePersonWithTick(author, tick)
	}

	return linehistory.NewFile(0, tick, size, analyser.fileAllocator, updaters...)
}

func (analyser *LegacyBurndownAnalysis) handleInsertion(
	change *object.Change, author int, cache map[plumbing.Hash]*items.CachedBlob,
) error {
	blob := cache[change.To.TreeEntry.Hash]

	lines, err := blob.CountLines()
	if err != nil {
		if errors.Is(err, items.ErrBinary) {
			return nil
		}

		return fmt.Errorf("count lines in inserted file %s: %w", change.To.Name, err)
	}

	name := change.To.Name

	file := analyser.files[name]
	if file != nil {
		return fmt.Errorf("%w: %s", errFileAlreadyExists, name)
	}

	file = analyser.newFile(name, author, analyser.tick, lines)
	analyser.files[name] = file
	delete(analyser.deletions, name)

	if analyser.tick == linehistory.TreeMergeMark {
		analyser.mergedFiles[name] = true
	}

	return nil
}

//nolint:cyclop // Preserve the legacy transition state machine while keeping it isolated.
func (analyser *LegacyBurndownAnalysis) handleDeletion(
	change *object.Change, author int, cache map[plumbing.Hash]*items.CachedBlob,
) error {
	var name string
	if change.To.TreeEntry.Hash != plumbing.ZeroHash {
		// became binary
		name = change.To.Name
	} else {
		name = change.From.Name
	}

	file, exists := analyser.files[name]
	blob := cache[change.From.TreeEntry.Hash]

	lines, err := blob.CountLines()
	if errors.Is(err, items.ErrBinary) {
		if exists && file.Len() != 0 {
			return fmt.Errorf("%w: %s", errPreviousFileBecameBinary, name)
		}

		lines = 0
		err = nil
	}

	if exists && err != nil {
		return fmt.Errorf("%w: %s", errPreviousFileBecameBinary, name)
	}

	if !exists {
		return nil
	}

	tick := analyser.tick
	// Are we merging and this file has never been actually deleted in any branch?
	if analyser.tick == linehistory.TreeMergeMark && !analyser.deletions[name] {
		tick = 0
	}

	analyser.deletions[name] = true
	file.Update(analyser.packChangePersonWithTick(author, tick), 0, 0, lines)
	file.Delete()
	delete(analyser.files, name)

	if history := analyser.fileHistories[name]; history != nil {
		analyser.deletedFileHistories[name] = history
		delete(analyser.fileHistories, name)
	}

	analyser.clearRenameChain(name)

	if analyser.tick == linehistory.TreeMergeMark {
		analyser.mergedFiles[name] = false
	}

	return nil
}

func (analyser *LegacyBurndownAnalysis) clearRenameChain(name string) {
	delete(analyser.renames, "")

	stack := []string{name}
	visited := map[string]bool{}

	for len(stack) > 0 {
		head := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if head == "" || visited[head] {
			continue
		}

		visited[head] = true
		delete(analyser.renames, head)

		for key, val := range analyser.renames {
			if val == head {
				stack = append(stack, key)
			}
		}
	}
}

func (analyser *LegacyBurndownAnalysis) handleModification(
	change *object.Change, author int, cache map[plumbing.Hash]*items.CachedBlob,
	diffs map[string]items.FileDiffData,
) error {
	analyser.restoreDeletedFileHistory(change.From.Name)

	if analyser.tick == linehistory.TreeMergeMark {
		analyser.mergedFiles[change.To.Name] = true
	}

	file, exists := analyser.files[change.From.Name]
	if !exists {
		// this indeed may happen
		return analyser.handleInsertion(change, author, cache)
	}

	// possible rename
	if change.To.Name != change.From.Name {
		err := analyser.handleRename(change.From.Name, change.To.Name)
		if err != nil {
			return err
		}
	}

	handled, err := analyser.handleBinaryModification(change, author, cache)
	if handled || err != nil {
		return err
	}

	thisDiffs := diffs[change.To.Name]
	if file.Len() != thisDiffs.OldLinesOfCode {
		analyser.l.Infof("====TREE====\n%s", file.Dump())

		return fmt.Errorf("%s: %w src %d != %d %s -> %s",
			change.To.Name, errLegacyIntegrity, thisDiffs.OldLinesOfCode, file.Len(),
			change.From.TreeEntry.Hash.String(), change.To.TreeEntry.Hash.String())
	}

	err = analyser.applyModificationDiffs(file, change, author, thisDiffs.Diffs)
	if err != nil {
		return err
	}

	if file.Len() != thisDiffs.NewLinesOfCode {
		return fmt.Errorf("%s: %w dst %d != %d %s -> %s",
			change.To.Name, errLegacyIntegrity, thisDiffs.NewLinesOfCode, file.Len(),
			change.From.TreeEntry.Hash.String(), change.To.TreeEntry.Hash.String())
	}

	return nil
}

func (analyser *LegacyBurndownAnalysis) restoreDeletedFileHistory(name string) {
	if !analyser.TrackFiles || analyser.fileHistories[name] != nil {
		return
	}

	if history := analyser.deletedFileHistories[name]; history != nil {
		analyser.fileHistories[name] = history
		delete(analyser.deletedFileHistories, name)
	}
}

func (analyser *LegacyBurndownAnalysis) handleBinaryModification(
	change *object.Change, author int, cache map[plumbing.Hash]*items.CachedBlob,
) (bool, error) {
	_, errFrom := cache[change.From.TreeEntry.Hash].CountLines()

	toLines, errTo := cache[change.To.TreeEntry.Hash].CountLines()
	//nolint:nestif // Preserve the legacy binary-transition decision tree.
	if !errors.Is(errFrom, errTo) {
		file := analyser.files[change.To.Name]
		if errors.Is(errFrom, items.ErrBinary) {
			if file == nil {
				return true, analyser.handleInsertion(change, author, cache)
			}

			file.Update(analyser.packChangePersonWithTick(author, analyser.tick), 0, toLines, file.Len())

			return true, nil
		}

		if errFrom != nil {
			return true, fmt.Errorf("count lines in previous version of %s: %w", change.From.Name, errFrom)
		}

		if !errors.Is(errTo, items.ErrBinary) {
			return true, fmt.Errorf("count lines in new version of %s: %w", change.To.Name, errTo)
		}

		if file != nil {
			file.Update(analyser.packChangePersonWithTick(author, analyser.tick), 0, 0, file.Len())
		}

		return true, nil
	}

	if errFrom == nil {
		return false, nil
	}

	if errors.Is(errFrom, items.ErrBinary) {
		return true, nil
	}

	return true, fmt.Errorf("count lines in modified file %s: %w", change.To.Name, errFrom)
}

func (analyser *LegacyBurndownAnalysis) applyModificationDiffs(
	file *linehistory.File, change *object.Change, author int, diffs []diffmatchpatch.Diff,
) error {
	state := legacyDiffState{analyser: analyser, file: file, change: change, author: author}
	for _, edit := range diffs {
		err := state.process(edit)
		if err != nil {
			return err
		}
	}

	state.flush()

	return nil
}

type legacyDiffState struct {
	analyser *LegacyBurndownAnalysis
	file     *linehistory.File
	change   *object.Change
	author   int
	position int
	pending  diffmatchpatch.Diff
}

func (state *legacyDiffState) process(edit diffmatchpatch.Diff) error {
	before := ""
	if state.analyser.Debug {
		before = state.file.Dump()
	}

	length := utf8.RuneCountInString(edit.Text)
	switch edit.Type {
	case diffmatchpatch.DiffEqual:
		state.flush()
		state.position += length
	case diffmatchpatch.DiffInsert:
		if state.pending.Text == "" {
			state.pending = edit
			return nil
		}

		if state.pending.Type == diffmatchpatch.DiffInsert {
			state.debugError(length, before)
			return errDiffInsertAfterInsert
		}

		state.file.Update(
			state.analyser.packChangePersonWithTick(state.author, state.analyser.tick),
			state.position, length, utf8.RuneCountInString(state.pending.Text),
		)

		if state.analyser.Debug {
			state.file.Validate()
		}

		state.position += length
		state.pending.Text = ""
	case diffmatchpatch.DiffDelete:
		if state.pending.Text != "" {
			state.debugError(length, before)
			return errDiffDeleteAfterEdit
		}

		state.pending = edit
	default:
		state.debugError(length, before)
		return fmt.Errorf("%w: %d", errUnsupportedDiffOperation, edit.Type)
	}

	return nil
}

func (state *legacyDiffState) flush() {
	if state.pending.Text == "" {
		return
	}

	length := utf8.RuneCountInString(state.pending.Text)

	packed := state.analyser.packChangePersonWithTick(state.author, state.analyser.tick)
	if state.pending.Type == diffmatchpatch.DiffInsert {
		state.file.Update(packed, state.position, length, 0)
		state.position += length
	} else {
		state.file.Update(packed, state.position, 0, length)
	}

	if state.analyser.Debug {
		state.file.Validate()
	}

	state.pending.Text = ""
}

func (state *legacyDiffState) debugError(length int, before string) {
	state.analyser.l.Errorf("%s: internal diff error\n", state.change.To.Name)
	state.analyser.l.Errorf(
		"Update(%d, %d, %d (0), %d (0))\n", state.analyser.tick, state.position,
		length, utf8.RuneCountInString(state.pending.Text),
	)

	if before != "" {
		state.analyser.l.Errorf("====TREE BEFORE====\n%s====END====\n", before)
	}

	state.analyser.l.Errorf("====TREE AFTER====\n%s====END====\n", state.file.Dump())
}

func (analyser *LegacyBurndownAnalysis) handleRename(sourceName, targetName string) error {
	if sourceName == targetName {
		return nil
	}

	file, exists := analyser.files[sourceName]
	if !exists {
		return fmt.Errorf("%w (files): %s > %s", errLegacyFileMissing, sourceName, targetName)
	}

	delete(analyser.files, sourceName)
	analyser.files[targetName] = file
	delete(analyser.deletions, targetName)

	if analyser.tick == linehistory.TreeMergeMark {
		analyser.mergedFiles[sourceName] = false
	}

	if analyser.TrackFiles {
		history, err := analyser.historyAfterRename(sourceName, targetName)
		if err != nil {
			return err
		}

		delete(analyser.fileHistories, sourceName)
		analyser.fileHistories[targetName] = history
	}

	analyser.renames[sourceName] = targetName

	return nil
}

func (analyser *LegacyBurndownAnalysis) historyAfterRename(sourceName, targetName string) (sparseHistory, error) {
	if history := analyser.fileHistories[sourceName]; history != nil {
		return history, nil
	}

	if _, exists := analyser.renames[""]; exists {
		panic("burndown renames tracking corruption")
	}

	known := map[string]bool{sourceName: true}
	future := analyser.futureHistoryName(sourceName, known)

	if future == "" {
		return sparseHistory{}, nil
	}

	history := analyser.fileHistories[future]
	if history == nil {
		return nil, fmt.Errorf("%w: %s > %s (%s)", errLegacyHistoryMissing, sourceName, targetName, future)
	}

	return history, nil
}

func (analyser *LegacyBurndownAnalysis) futureHistoryName(sourceName string, known map[string]bool) string {
	if sourceName == "" {
		return ""
	}

	future, exists := analyser.renames[sourceName]
	for exists {
		if future == "" {
			return ""
		}

		next, nextExists := analyser.renames[future]
		if !nextExists {
			return future
		}

		if known[next] {
			return analyser.knownHistoryName(known)
		}

		known[future] = true
		future, exists = next, true
	}

	return future
}

func (analyser *LegacyBurndownAnalysis) knownHistoryName(known map[string]bool) string {
	for name := range known {
		if analyser.fileHistories[name] != nil {
			return name
		}
	}

	return ""
}

func (analyser *LegacyBurndownAnalysis) groupSparseHistory(
	history sparseHistory, lastTick int,
) (DenseHistory, int) {
	if len(history) == 0 {
		panic("empty history")
	}

	ticks, lastTick := prepareSparseHistoryTicks(history, lastTick)

	// [y][x]
	// y - sampling
	// x - granularity
	samples := lastTick/analyser.Sampling + 1
	bands := lastTick/analyser.Granularity + 1

	result := make(DenseHistory, samples)
	for i := range bands {
		result[i] = make([]int64, bands)
	}

	populateGroupedHistory(result, history, ticks, analyser.Sampling, analyser.Granularity)

	return result, lastTick
}

var _ = core.RegisterPipelineItem(&LegacyBurndownAnalysis{})
