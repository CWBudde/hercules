package linehistory

import (
	"maps"
	"slices"
)

// adoptMergeCreatedFileIds re-keys the files this branch minted while replaying the merge commit
// onto the id the branch that really created the path is using.
//
// A merge commit that brings in a path this branch never held is reported as an insertion, so
// newFile() mints a fresh id - and because merge-commit edits carry TreeMergeMark, File.updateTime
// suppresses the insertion, so nothing is ever emitted for that id. The branch that added the path
// did emit its insertion, under its own id. matchingMergeFiles then pairs the two by path
// (PLAN.md B1d), the other branch's file is discarded by synchronization, and the accounting ends
// up split across two keys: the old id carries lines nobody will ever remove, while a removal
// against the new id cancels an insertion that was never sent and drives the file history negative.
//
// Adopting the other branch's id keeps both halves on one key. Ids are minted from a counter
// shared by every branch, so no two files are ever *created* under one id. That is not the same as
// the id being free here: an id follows its file across renames, so a branch which renamed the path
// still holds that id under the new name while the sibling kept it under the old one. Adopting into
// such an id would put two live files on one key, so an id already occupied at another path is
// skipped - see liveFileIds(). The lowest free matching id wins, for determinism (PLAN.md B12).
func (analyser *LineHistoryAnalyser) adoptMergeCreatedFileIds(others []*LineHistoryAnalyser) {
	live := analyser.liveFileIds()

	for _, id := range slices.Sorted(maps.Keys(analyser.mergeCreatedFiles)) {
		name := analyser.mergeCreatedFiles[id]

		file := analyser.files[name]
		if file == nil || file.Id != id {
			continue
		}

		adopted, found := adoptableFileId(others, name, file, live)
		if !found {
			continue
		}

		// Deliberately not recorded as an abandoned name: abandonedFileID() prefers the highest
		// matching id, so leaving this one behind lets a later merge resurrect a key that now holds
		// no accounting at all and mix its bands into the adopted file.
		delete(analyser.fileNames, id)
		delete(live, id)

		file.Id = adopted
		analyser.fileNames[adopted] = name
		live[adopted] = name
	}
}

// adoptableFileId returns the lowest id another branch holds for name which file may take over:
// it must differ from file's own id, cover the same number of lines, and not already be live at a
// different path here. Leaving the file on its minted id keeps the accounting split, which is the
// pre-existing defect adoption otherwise repairs; merging two paths onto one id would be worse.
func adoptableFileId(
	others []*LineHistoryAnalyser, name string, file *File, live map[FileId]string,
) (FileId, bool) {
	adopted, found := FileId(0), false

	for _, branch := range others {
		candidate := branch.files[name]
		if candidate == nil || candidate.Id == file.Id || candidate.Len() != file.Len() {
			continue
		}

		if holder, taken := live[candidate.Id]; taken && holder != name {
			continue
		}

		if !found || candidate.Id < adopted {
			adopted, found = candidate.Id, true
		}
	}

	return adopted, found
}

// liveFileIds maps every id currently held by a file of this branch to that file's path.
func (analyser *LineHistoryAnalyser) liveFileIds() map[FileId]string {
	live := make(map[FileId]string, len(analyser.files))
	for name, file := range analyser.files {
		if file != nil {
			live[file.Id] = name
		}
	}

	return live
}
