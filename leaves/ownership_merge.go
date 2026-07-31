package leaves

import (
	"sort"
	"time"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/join"
	items "github.com/cwbudde/hercules/internal/plumbing"
)

// ownershipTickLines is one tick of an alive-line ownership distribution, as both
// BusFactorAnalysis and OwnershipConcentrationAnalysis snapshot it.
type ownershipTickLines struct {
	TotalLines  int64
	AuthorLines map[int]int64
}

// mergedTickOffsets rebases two tick axes onto the earlier repository start, which is exactly
// what CommonAnalysisResult.Merge() records as the merged BeginTime.
func mergedTickOffsets(
	commonResult1, commonResult2 *core.CommonAnalysisResult, tickSize time.Duration,
) (int, int) {
	if tickSize <= 0 || commonResult1 == nil || commonResult2 == nil {
		return 0, 0
	}

	firstStart := items.FloorTime(commonResult1.BeginTimeAsTime(), tickSize)
	secondStart := items.FloorTime(commonResult2.BeginTimeAsTime(), tickSize)

	startTime := firstStart
	if secondStart.Before(startTime) {
		startTime = secondStart
	}

	offset1 := int(firstStart.Sub(startTime) / tickSize)
	offset2 := int(secondStart.Sub(startTime) / tickSize)

	return offset1, offset2
}

// mergeOwnershipTicks adds two per-tick ownership distributions on a common, rebased tick axis.
//
// Ownership is additive across repositories: at any tick every repository's living lines are part
// of the corpus, so the merge sums the per-identity counts instead of selecting one side. A
// repository which has stopped producing snapshots keeps contributing its last known distribution
// — those lines still exist — while one which has not started yet contributes nothing. Metrics
// derived from the distribution (the bus factor, Gini, HHI) are thresholds over the summed
// distribution and have to be recomputed by the caller afterwards, not merged from the parts.
func mergeOwnershipTicks(
	first, second map[int]ownershipTickLines,
	offsetFirst, offsetSecond int,
	people join.IdentityMapping,
) map[int]ownershipTickLines {
	left := rebaseOwnershipTicks(first, offsetFirst, people.First)
	right := rebaseOwnershipTicks(second, offsetSecond, people.Second)

	merged := make(map[int]ownershipTickLines, len(left)+len(right))

	var carriedLeft, carriedRight *ownershipTickLines

	for _, tick := range mergedOwnershipTickAxis(left, right) {
		if lines, exists := left[tick]; exists {
			carriedLeft = &lines
		}

		if lines, exists := right[tick]; exists {
			carriedRight = &lines
		}

		merged[tick] = addOwnershipTicks(carriedLeft, carriedRight)
	}

	return merged
}

// rebaseOwnershipTicks shifts one source onto the merged tick axis and translates its author
// indices into the merged people dictionary.
func rebaseOwnershipTicks(
	source map[int]ownershipTickLines, offset int, identities []int,
) map[int]ownershipTickLines {
	rebased := make(map[int]ownershipTickLines, len(source))

	for tick, lines := range source {
		authorLines := make(map[int]int64, len(lines.AuthorLines))
		for author, count := range lines.AuthorLines {
			authorLines[mapOwnershipAuthor(author, identities)] += count
		}

		rebased[tick+offset] = ownershipTickLines{
			TotalLines:  lines.TotalLines,
			AuthorLines: authorLines,
		}
	}

	return rebased
}

// mapOwnershipAuthor translates a source author index into the merged dictionary. Indices outside
// the source dictionary — core.AuthorMissing above all — have no name to reconcile and stay put.
func mapOwnershipAuthor(author int, identities []int) int {
	if author < 0 || author >= len(identities) {
		return author
	}

	return identities[author]
}

// mergedOwnershipTickAxis returns every tick either side holds, in ascending order.
func mergedOwnershipTickAxis(left, right map[int]ownershipTickLines) []int {
	ticks := make([]int, 0, len(left)+len(right))
	for tick := range left {
		ticks = append(ticks, tick)
	}

	for tick := range right {
		if _, duplicate := left[tick]; !duplicate {
			ticks = append(ticks, tick)
		}
	}

	sort.Ints(ticks)

	return ticks
}

// addOwnershipTicks sums the two sides which are alive at one tick. Either may be nil, meaning
// that repository has not produced a snapshot yet.
func addOwnershipTicks(left, right *ownershipTickLines) ownershipTickLines {
	sum := ownershipTickLines{AuthorLines: map[int]int64{}}

	for _, side := range []*ownershipTickLines{left, right} {
		if side == nil {
			continue
		}

		sum.TotalLines += side.TotalLines

		for author, count := range side.AuthorLines {
			sum.AuthorLines[author] += count
		}
	}

	return sum
}
