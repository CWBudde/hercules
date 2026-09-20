package leaves

import (
	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
)

// This file holds everything burndown reads off the line-history branch at Finalize(): which
// branch that is, the merge-resolution deltas still buffered on it, and the per-file ownership
// scan. See PLAN.md B14 for why the branch matters.

// finalResolver names files and reads pending merge deltas at Finalize(): the branch handed over
// by the last non-replica commit, falling back to the registered one for a run which consumed
// nothing.
func (analyser *BurndownAnalysis) finalResolver() core.FileIdResolver {
	if analyser.authoritativeResolver != nil {
		return analyser.authoritativeResolver
	}

	return analyser.primaryResolver
}

// consumePendingLineHistory accounts for merge-resolution deltas that were still buffered when
// the last commit was consumed. That happens when the analysed HEAD is itself a merge commit:
// LineHistoryAnalyser.Merge() runs after the final Consume(), so there is no commit left to
// carry its changes.
func consumePendingLineHistory(analyser *BurndownAnalysis) {
	resolver := analyser.finalResolver()
	if resolver == nil {
		return
	}

	pending := linehistory.PendingChanges(resolver)
	if len(pending) == 0 {
		return
	}

	consumeLineHistory(analyser, core.LineHistoryChanges{
		Changes:  pending,
		Resolver: resolver,
	})
}

func (analyser *BurndownAnalysis) collectFileOwnership(fileOwnership map[string]map[int]int) {
	analyser.fileResolver.ForEachFile(func(fileId core.FileId, fileName string) {
		previousLine := 0
		previousAuthor := core.AuthorMissing
		ownership := map[int]int{}

		if analyser.fileResolver.ScanFile(fileId,
			func(line int, tick core.TickNumber, author core.AuthorId) {
				length := line - previousLine
				if length > 0 {
					ownership[previousAuthor] += length
				}

				previousLine = line

				if author >= core.AuthorMissing {
					previousAuthor = -1
				} else {
					previousAuthor = int(author)
				}
			}) {
			fileOwnership[fileName] = ownership
		}
	})
}
