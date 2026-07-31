package plumbing

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/cwbudde/hercules/internal"
	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/levenshtein"
)

var errNegativeRenameTimeout = errors.New("negative renames detection timeout is not allowed")

// RenameAnalysis improves TreeDiff's results by searching for changed blobs under different
// paths which are likely to be the result of a rename with subsequent edits.
// RenameAnalysis is a PipelineItem.
type RenameAnalysis struct {
	core.NoopMerger

	// SimilarityThreshold adjusts the heuristic to determine file renames.
	// It has the same units as cgit's -X rename-threshold or -M. Better to
	// set it to the default value of 80 (80%).
	SimilarityThreshold int

	// Timeout is the maximum time allowed to spend computing renames in a single commit.
	Timeout time.Duration

	repository *git.Repository

	// truncation counts the places where a wall-clock deadline cut rename matching short.
	// Fork hands out the same instance, so one counter serves every branch.
	truncation *truncationReport

	l core.Logger
}

const (
	// RenameAnalysisDefaultThreshold specifies the default percentage of common lines in a pair
	// of files to consider them linked. The exact code of the decision is sizesAreClose().
	// CGit's default is 50%. Ours is 80% because 50% can be too computationally expensive.
	RenameAnalysisDefaultThreshold = 80

	// RenameAnalysisDefaultTimeout is the default value of RenameAnalysis.Timeout (in milliseconds).
	RenameAnalysisDefaultTimeout = 60000

	// ConfigRenameAnalysisSimilarityThreshold is the name of the configuration option
	// (RenameAnalysis.Configure()) which sets the similarity threshold.
	ConfigRenameAnalysisSimilarityThreshold = "RenameAnalysis.SimilarityThreshold"

	// ConfigRenameAnalysisTimeout is the name of the configuration option
	// (RenameAnalysis.Configure()) which sets the maximum time allowed to spend
	// computing renames in a single commit.
	ConfigRenameAnalysisTimeout = "RenameAnalysis.Timeout"

	// RenameAnalysisMinimumSize is the minimum size of a blob to be considered.
	RenameAnalysisMinimumSize = 32

	// RenameAnalysisMaxCandidates is the maximum number of rename candidates to consider per file.
	RenameAnalysisMaxCandidates = 50

	// RenameAnalysisSetSizeLimit is the maximum number of added + removed files for
	// RenameAnalysisMaxCandidates to be active; the bigger numbers set it to 1.
	RenameAnalysisSetSizeLimit = 1000

	// RenameAnalysisByteDiffSizeThreshold is the maximum size of each of the compared parts
	// to be diff-ed on byte level.
	RenameAnalysisByteDiffSizeThreshold = 100000
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (ra *RenameAnalysis) Name() string {
	return "RenameAnalysis"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (ra *RenameAnalysis) Provides() []string {
	return []string{DependencyTreeChanges}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (ra *RenameAnalysis) Requires() []string {
	return []string{DependencyBlobCache, DependencyTreeChanges}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (ra *RenameAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name:        ConfigRenameAnalysisSimilarityThreshold,
			Description: "The threshold on the similarity index used to detect renames.",
			Flag:        "M",
			Type:        core.IntConfigurationOption,
			Default:     RenameAnalysisDefaultThreshold,
		}, {
			Name: ConfigRenameAnalysisTimeout,
			Description: "The maximum time (milliseconds) allowed to spend computing " +
				"renames in a single commit. 0 sets the default.",
			Flag:    "renames-timeout",
			Type:    core.IntConfigurationOption,
			Default: RenameAnalysisDefaultTimeout,
		},
	}

	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (ra *RenameAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		ra.l = l
	}

	if val, exists := facts[ConfigRenameAnalysisSimilarityThreshold].(int); exists {
		ra.SimilarityThreshold = val
	}

	if val, exists := facts[ConfigRenameAnalysisTimeout].(int); exists {
		if val < 0 {
			return fmt.Errorf("%w: %d", errNegativeRenameTimeout, val)
		}

		ra.Timeout = time.Duration(val) * time.Millisecond
	}

	return nil
}

func (*RenameAnalysis) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (ra *RenameAnalysis) Initialize(repository *git.Repository) error {
	ra.l = core.NewLogger()
	if ra.SimilarityThreshold < 0 || ra.SimilarityThreshold > 100 {
		ra.l.Warnf("adjusted the similarity threshold to %d\n",
			RenameAnalysisDefaultThreshold)
		ra.SimilarityThreshold = RenameAnalysisDefaultThreshold
	}

	if ra.Timeout == 0 {
		ra.Timeout = time.Duration(RenameAnalysisDefaultTimeout) * time.Millisecond
	}

	ra.repository = repository
	ra.truncation = newRenameTruncationReport()

	return nil
}

// Dispose flushes the deadline-truncation summary once per run, on success and on failure alike.
func (ra *RenameAnalysis) Dispose() {
	ra.truncationReporter().summarise(ra.l)
}

func matchExactRenames(
	changes object.Changes,
) (object.Changes, object.Changes, object.Changes, error) {
	reduced, added, deleted, err := splitRenameChanges(changes)
	if err != nil {
		return nil, nil, nil, err
	}

	sort.Sort(added)
	sort.Sort(deleted)

	var remainingAdded, remainingDeleted object.Changes

	addedIndex, deletedIndex := 0, 0
	for addedIndex < added.Len() && deletedIndex < deleted.Len() {
		switch {
		case added[addedIndex].hash == deleted[deletedIndex].hash:
			reduced = append(reduced, &object.Change{
				From: deleted[deletedIndex].change.From, To: added[addedIndex].change.To,
			})
			addedIndex++
			deletedIndex++
		case added[addedIndex].Less(&deleted[deletedIndex]):
			remainingAdded = append(remainingAdded, added[addedIndex].change)
			addedIndex++
		default:
			remainingDeleted = append(remainingDeleted, deleted[deletedIndex].change)
			deletedIndex++
		}
	}

	for ; addedIndex < added.Len(); addedIndex++ {
		remainingAdded = append(remainingAdded, added[addedIndex].change)
	}

	for ; deletedIndex < deleted.Len(); deletedIndex++ {
		remainingDeleted = append(remainingDeleted, deleted[deletedIndex].change)
	}

	return reduced, remainingAdded, remainingDeleted, nil
}

func splitRenameChanges(
	changes object.Changes,
) (object.Changes, sortableChanges, sortableChanges, error) {
	reduced := make(object.Changes, 0, changes.Len())
	var added, deleted sortableChanges

	for _, change := range changes {
		action, err := change.Action()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("classify rename change: %w", err)
		}

		switch action {
		case merkletrie.Insert:
			added = append(added, sortableChange{change, change.To.TreeEntry.Hash})
		case merkletrie.Delete:
			deleted = append(deleted, sortableChange{change, change.From.TreeEntry.Hash})
		case merkletrie.Modify:
			reduced = append(reduced, change)
		}
	}

	return reduced, added, deleted, nil
}

func classifyRenameBlobs(
	added, deleted object.Changes, cache map[plumbing.Hash]*CachedBlob,
) (sortableBlobs, sortableBlobs, object.Changes) {
	var addedBlobs, deletedBlobs sortableBlobs
	var small object.Changes

	for _, change := range added {
		blob := cache[change.To.TreeEntry.Hash]
		if blob.Size < RenameAnalysisMinimumSize {
			small = append(small, change)
		} else {
			addedBlobs = append(addedBlobs, sortableBlob{change: change, size: blob.Size})
		}
	}

	for _, change := range deleted {
		blob := cache[change.From.TreeEntry.Hash]
		if blob.Size < RenameAnalysisMinimumSize {
			small = append(small, change)
		} else {
			deletedBlobs = append(deletedBlobs, sortableBlob{change: change, size: blob.Size})
		}
	}

	sort.Sort(addedBlobs)
	sort.Sort(deletedBlobs)

	return addedBlobs, deletedBlobs, small
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (ra *RenameAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ra.Timeout)
	defer cancel()

	changes, err := dependencyValue[object.Changes](deps, DependencyTreeChanges)
	if err != nil {
		return nil, err
	}

	cache, err := dependencyValue[map[plumbing.Hash]*CachedBlob](deps, DependencyBlobCache)
	if err != nil {
		return nil, err
	}

	reducedChanges, stillAdded, stillDeleted, err := matchExactRenames(changes)
	if err != nil {
		return nil, err
	}

	// Stage 2 - apply the similarity threshold
	// n^2 but actually linear
	// We sort the blobs by size and do the single linear scan.
	maxCandidates := RenameAnalysisMaxCandidates
	if len(stillAdded)+len(stillDeleted) > RenameAnalysisSetSizeLimit {
		maxCandidates = 1
	}

	addedBlobs, deletedBlobs, smallChanges := classifyRenameBlobs(stillAdded, stillDeleted, cache)

	matches, addedBlobs, deletedBlobs, err := ra.matchSimilarRenames(
		ctx, addedBlobs, deletedBlobs, cache, maxCandidates,
	)
	if err != nil {
		return nil, err
	}

	reducedChanges = appendRenameResults(
		reducedChanges, matches, addedBlobs, deletedBlobs, smallChanges,
	)

	return map[string]any{DependencyTreeChanges: reducedChanges}, nil
}

func appendRenameResults(
	reduced, matches object.Changes,
	addedBlobs, deletedBlobs sortableBlobs,
	small object.Changes,
) object.Changes {
	reduced = append(reduced, matches...)
	for _, blob := range addedBlobs {
		reduced = append(reduced, blob.change)
	}

	for _, blob := range deletedBlobs {
		reduced = append(reduced, blob.change)
	}

	return append(reduced, small...)
}

type renameCandidateMatcher func(
	context.Context, *CachedBlob, []int, sortableBlobs, map[plumbing.Hash]*CachedBlob, int,
) (int, error)

func renameBlobHash(blob sortableBlob, deleted bool) plumbing.Hash {
	if deleted {
		return blob.change.From.TreeEntry.Hash
	}

	return blob.change.To.TreeEntry.Hash
}

func renameBlobName(blob sortableBlob, deleted bool) string {
	if deleted {
		return blob.change.From.Name
	}

	return blob.change.To.Name
}

func matchedRename(primary, secondary sortableBlob, primaryIsDeleted bool) *object.Change {
	if primaryIsDeleted {
		return &object.Change{From: primary.change.From, To: secondary.change.To}
	}

	return &object.Change{From: secondary.change.From, To: primary.change.To}
}

func renameCandidates(start, end int, closeEnough func(int) bool) []int {
	var candidates []int
	for index := start; index < end && closeEnough(index); index++ {
		candidates = append(candidates, index)
	}

	return candidates
}

// Fork clones this PipelineItem.
func (ra *RenameAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(ra, n)
}

// truncationReporter tolerates a RenameAnalysis built directly in a test, without Initialize().
func (ra *RenameAnalysis) truncationReporter() *truncationReport {
	if ra.truncation == nil {
		ra.truncation = newRenameTruncationReport()
	}

	return ra.truncation
}

// matchSimilarRenames pairs the leftover added and deleted blobs by content similarity.
//
// Matching is greedy and removes each claimed blob from the working set, so scanning the
// deleted side and scanning the added side are not equivalent: they produce different pairings
// whenever a blob has more than one plausible partner. This used to run both directions
// concurrently and keep whichever goroutine finished first, which handed the choice to the Go
// scheduler and made the whole analysis non-reproducible run to run. The direction is now
// derived from the data instead: iterating the smaller side is the cheaper one, which is what
// the race was really approximating. Equal sizes go to the deleted side.
func (ra *RenameAnalysis) matchSimilarRenames(
	ctx context.Context,
	addedBlobs, deletedBlobs sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob,
	maxCandidates int,
) (object.Changes, sortableBlobs, sortableBlobs, error) {
	if len(deletedBlobs) <= len(addedBlobs) {
		return ra.matchDeletedBlobs(ctx, addedBlobs, deletedBlobs, cache, maxCandidates)
	}

	return ra.matchAddedBlobs(ctx, addedBlobs, deletedBlobs, cache, maxCandidates)
}

func isRenameCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (ra *RenameAnalysis) matchDeletedBlobs(
	ctx context.Context,
	added, deleted sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob,
	maxCandidates int,
) (object.Changes, sortableBlobs, sortableBlobs, error) {
	return ra.matchBlobs(
		ctx, added, deleted, cache, maxCandidates, true,
	)
}

func (ra *RenameAnalysis) matchAddedBlobs(
	ctx context.Context,
	added, deleted sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob,
	maxCandidates int,
) (object.Changes, sortableBlobs, sortableBlobs, error) {
	return ra.matchBlobs(
		ctx, added, deleted, cache, maxCandidates, false,
	)
}

func (ra *RenameAnalysis) matchBlobs(
	ctx context.Context,
	added, deleted sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob,
	maxCandidates int,
	matchDeleted bool,
) (object.Changes, sortableBlobs, sortableBlobs, error) {
	var matches object.Changes
	primary, secondary, matchCandidate := ra.renameMatchDirection(added, deleted, matchDeleted)

	secondaryStart := 0

	for primaryIndex := 0; primaryIndex < primary.Len(); primaryIndex++ {
		if ctx.Err() != nil {
			ra.truncationReporter().record(ra.l, renameTruncationCandidateScan)

			break
		}

		blob := cache[renameBlobHash(primary[primaryIndex], matchDeleted)]

		var candidates []int
		candidates, secondaryStart = ra.rankedCandidates(
			primary[primaryIndex], secondary, secondaryStart, matchDeleted,
		)

		match, err := matchCandidate(ctx, blob, candidates, secondary, cache, maxCandidates)
		if err != nil {
			if isRenameCancellation(err) {
				ra.truncationReporter().record(ra.l, renameTruncationCandidateScan)

				break
			}

			return nil, nil, nil, err
		}

		if match >= 0 {
			matches = append(matches, matchedRename(primary[primaryIndex], secondary[match], matchDeleted))
			primary = append(primary[:primaryIndex], primary[primaryIndex+1:]...)
			primaryIndex--

			secondary = append(secondary[:match], secondary[match+1:]...)
		}
	}

	if matchDeleted {
		return matches, secondary, primary, nil
	}

	return matches, primary, secondary, nil
}

// rankedCandidates returns the size-compatible partners for one blob, ordered by how close
// their basename is, along with the advanced scan cursor. Both blob lists are sorted by size,
// so the cursor only ever moves forward.
func (ra *RenameAnalysis) rankedCandidates(
	blob sortableBlob, secondary sortableBlobs, secondaryStart int, matchDeleted bool,
) ([]int, int) {
	size := blob.size

	for secondaryStart < secondary.Len() &&
		!ra.sizesAreClose(size, secondary[secondaryStart].size) {
		secondaryStart++
	}

	candidates := renameCandidates(secondaryStart, secondary.Len(), func(index int) bool {
		return ra.sizesAreClose(size, secondary[index].size)
	})
	sortRenameCandidates(
		candidates,
		filepath.Base(renameBlobName(blob, matchDeleted)),
		func(index int) string {
			return renameBlobName(secondary[index], !matchDeleted)
		},
	)

	return candidates, secondaryStart
}

func (ra *RenameAnalysis) renameMatchDirection(
	added, deleted sortableBlobs, matchDeleted bool,
) (sortableBlobs, sortableBlobs, renameCandidateMatcher) {
	if matchDeleted {
		return deleted, added, ra.matchDeletedCandidate
	}

	return added, deleted, ra.matchAddedCandidate
}

func (ra *RenameAnalysis) matchDeletedCandidate(
	ctx context.Context,
	blob *CachedBlob,
	candidates []int,
	added sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob,
	limit int,
) (int, error) {
	for candidateIndex, index := range candidates {
		select {
		case <-ctx.Done():
			return -1, fmt.Errorf("match deleted rename candidate: %w", ctx.Err())
		default:
		}

		if candidateIndex > limit {
			break
		}

		areClose, err := ra.blobsAreCloseContext(
			ctx, blob, cache[added[index].change.To.TreeEntry.Hash],
		)
		if err != nil {
			return -1, err
		}

		if areClose {
			return index, nil
		}
	}

	return -1, nil
}

func (ra *RenameAnalysis) matchAddedCandidate(
	ctx context.Context,
	blob *CachedBlob,
	candidates []int,
	deleted sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob,
	limit int,
) (int, error) {
	for candidateIndex, index := range candidates {
		select {
		case <-ctx.Done():
			return -1, fmt.Errorf("match added rename candidate: %w", ctx.Err())
		default:
		}

		if candidateIndex > limit {
			break
		}

		areClose, err := ra.blobsAreCloseContext(
			ctx, blob, cache[deleted[index].change.From.TreeEntry.Hash],
		)
		if err != nil || areClose {
			if areClose {
				return index, err
			}

			return -1, err
		}
	}

	return -1, nil
}

func (ra *RenameAnalysis) sizesAreClose(size1, size2 int64) bool {
	size := internal.Max64(1, internal.Max64(size1, size2))
	return (internal.Abs64(size1-size2)*10000)/size <= int64(100-ra.SimilarityThreshold)*100
}

func (ra *RenameAnalysis) blobsAreClose(blob1, blob2 *CachedBlob) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ra.Timeout)
	defer cancel()

	return ra.blobsAreCloseContext(ctx, blob1, blob2)
}

func (ra *RenameAnalysis) blobsAreCloseContext(
	ctx context.Context, blob1, blob2 *CachedBlob,
) (bool, error) {
	cleanReturn := false

	defer func() {
		if ctx.Err() == nil {
			ra.warnUncleanBlobComparison(&cleanReturn, blob1, blob2)
		}
	}()

	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("compare rename blobs: %w", err)
	}

	if binary, similar := ra.binaryBlobsAreClose(blob1, blob2); binary {
		cleanReturn = true
		return similar, nil
	}

	src, dst := string(blob1.Data), string(blob2.Data)

	diffs, srcPositions, dstPositions, err := renameLineDiffs(ctx, ra.diffBudget(), src, dst)
	if err != nil {
		return false, err
	}

	similar, err := ra.renameDiffSimilarity(ctx, src, dst, diffs, srcPositions, dstPositions)
	if err != nil {
		return false, err
	}

	cleanReturn = true

	return similar, nil
}

type renameSimilarityState struct {
	budget              renameDiffBudget
	common              int
	posSrc              int
	prevPosSrc          int
	posDst              int
	possibleDelInsBlock bool
}

func (ra *RenameAnalysis) renameDiffSimilarity(
	ctx context.Context,
	src, dst string,
	diffs []diffmatchpatch.Diff,
	srcPositions, dstPositions []int,
) (bool, error) {
	state := renameSimilarityState{budget: ra.diffBudget()}
	maxSize := internal.Max(1, internal.Max(utf8.RuneCountInString(src), utf8.RuneCountInString(dst)))

	for _, edit := range diffs {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("evaluate rename similarity: %w", err)
		}

		err := state.applyEdit(ctx, edit, src, dst, srcPositions, dstPositions)
		if err != nil {
			return false, err
		}

		if state.possibleDelInsBlock {
			continue
		}

		// supposing that the rest of the lines are the same (they are not - too optimistic),
		// estimate the maximum similarity and exit the loop if it lower than our threshold
		if decided, similar := ra.similarityDecision(
			state.common,
			maxSize,
			len(src)-srcPositions[state.posSrc],
			len(dst)-dstPositions[state.posDst],
		); decided {
			return similar, nil
		}
	}

	// the very last "overly optimistic" estimate was actually precise, so since we are still here
	// the blobs are similar
	return true, nil
}

func (state *renameSimilarityState) applyEdit(
	ctx context.Context,
	edit diffmatchpatch.Diff,
	src, dst string,
	srcPositions, dstPositions []int,
) error {
	switch edit.Type {
	case diffmatchpatch.DiffDelete:
		state.possibleDelInsBlock = true
		state.prevPosSrc = state.posSrc
		state.posSrc += utf8.RuneCountInString(edit.Text)
	case diffmatchpatch.DiffInsert:
		nextPosDst := state.posDst + utf8.RuneCountInString(edit.Text)
		if state.possibleDelInsBlock {
			state.possibleDelInsBlock = false

			localCommon, err := delInsCommonRunes(
				ctx,
				state.budget,
				src,
				dst,
				srcPositions,
				dstPositions,
				state.prevPosSrc,
				state.posSrc,
				state.posDst,
				nextPosDst,
			)
			if err != nil {
				return err
			}

			state.common += localCommon
		}

		state.posDst = nextPosDst
	case diffmatchpatch.DiffEqual:
		state.applyEqual(edit.Text, srcPositions)
	}

	return nil
}

func (state *renameSimilarityState) applyEqual(text string, srcPositions []int) {
	state.possibleDelInsBlock = false
	step := utf8.RuneCountInString(text)

	// Ranging over text does not work here because its indices are byte offsets, not rune offsets.
	for index := range step {
		state.common += srcPositions[state.posSrc+index+1] - srcPositions[state.posSrc+index]
	}

	state.posSrc += step
	state.posDst += step
}

func renameLineDiffs(
	ctx context.Context, budget renameDiffBudget, src, dst string,
) ([]diffmatchpatch.Diff, []int, []int, error) {
	err := ctx.Err()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("prepare rename line diff: %w", err)
	}

	// Compute the line-by-line diff, then the char-level diffs of the del-ins blocks.
	// The ignored []string mapping is approximate and collides for huge files.
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = budget.timeout
	srcLineRunes, dstLineRunes, _ := dmp.DiffLinesToRunes(src, dst)

	err = ctx.Err()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode rename line diff: %w", err)
	}

	diffs := budget.diffed(dmp, func() []diffmatchpatch.Diff {
		return dmp.DiffMainRunes(srcLineRunes, dstLineRunes, false)
	})

	err = ctx.Err()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("compute rename line diff: %w", err)
	}

	return diffs, calcLinePositions(src), calcLinePositions(dst), nil
}

// renameDiffBudget carries the per-diff wall-clock bound and the place to report it firing.
//
// The bound used to be whatever was left of the commit's rename budget, which made a pair's
// similarity verdict depend on how long the *earlier* pairs happened to take. It is now the
// configured timeout, so a given pair is judged the same way wherever it appears.
type renameDiffBudget struct {
	timeout time.Duration
	report  *truncationReport
	l       core.Logger
}

func (ra *RenameAnalysis) diffBudget() renameDiffBudget {
	timeout := ra.Timeout
	if timeout <= 0 {
		timeout = time.Duration(RenameAnalysisDefaultTimeout) * time.Millisecond
	}

	return renameDiffBudget{timeout: timeout, report: ra.truncationReporter(), l: ra.l}
}

// diffed runs a bounded diff and reports whether the bound was what stopped it. diffmatchpatch
// gives no signal of its own, so the elapsed time is the only evidence available.
func (budget renameDiffBudget) diffed(
	dmp *diffmatchpatch.DiffMatchPatch, run func() []diffmatchpatch.Diff,
) []diffmatchpatch.Diff {
	dmp.DiffTimeout = budget.timeout

	start := time.Now()
	diffs := run()

	if time.Since(start) >= budget.timeout && budget.report != nil {
		budget.report.record(budget.l, renameTruncationPairDiff)
	}

	return diffs
}

// The two ways a deadline can cut rename detection short. Which files end up reported as
// renames differs between the truncated and the untruncated run, so both are worth counting
// separately: one means the candidate scan gave up, the other that a single pair was judged on
// an incomplete diff.
const (
	renameTruncationCandidateScan = "candidate scan"
	renameTruncationPairDiff      = "pair comparison"
)

func newRenameTruncationReport() *truncationReport {
	return newTruncationReport(
		"rename detection",
		"Raise --renames-timeout to make the deadline unreachable, or lower --M to cut the "+
			"search space.",
	)
}

func (ra *RenameAnalysis) warnUncleanBlobComparison(cleanReturn *bool, blob1, blob2 *CachedBlob) {
	if !*cleanReturn {
		ra.l.Warnf("\nunclean return detected for blobs '%s' and '%s'\n",
			blob1.Hash.String(), blob2.Hash.String())
	}
}

func (ra *RenameAnalysis) binaryBlobsAreClose(blob1, blob2 *CachedBlob) (bool, bool) {
	_, firstErr := blob1.CountLines()

	_, secondErr := blob2.CountLines()
	if !errors.Is(firstErr, ErrBinary) && !errors.Is(secondErr, ErrBinary) {
		return false, false
	}

	diffLength := DiffBytes(blob1.Data, blob2.Data)
	delta := int((int64(diffLength) * 100) / internal.Max64(
		internal.Min64(blob1.Size, blob2.Size), 1,
	))

	return true, 100-delta >= ra.SimilarityThreshold
}

func (ra *RenameAnalysis) similarityDecision(common, maxSize, srcPendingSize, dstPendingSize int) (bool, bool) {
	maxCommon := common + internal.Min(srcPendingSize, dstPendingSize)
	if (maxCommon*100)/maxSize < ra.SimilarityThreshold {
		return true, false
	}

	if (common*100)/maxSize >= ra.SimilarityThreshold {
		return true, true
	}

	return false, false
}

func delInsCommonRunes(
	ctx context.Context,
	budget renameDiffBudget,
	src, dst string,
	srcPositions, dstPositions []int,
	prevPosSrc, posSrc, posDst, nextPosDst int,
) (int, error) {
	err := ctx.Err()
	if err != nil {
		return 0, fmt.Errorf("prepare byte-level rename diff: %w", err)
	}

	srcSize := srcPositions[posSrc] - srcPositions[prevPosSrc]

	dstSize := dstPositions[nextPosDst] - dstPositions[posDst]
	if internal.Max(srcSize, dstSize) >= RenameAnalysisByteDiffSizeThreshold {
		return 0, nil
	}

	localDmp := diffmatchpatch.New()
	localSrc := src[srcPositions[prevPosSrc]:srcPositions[posSrc]]
	localDst := dst[dstPositions[posDst]:dstPositions[nextPosDst]]
	localDiffs := budget.diffed(localDmp, func() []diffmatchpatch.Diff {
		return localDmp.DiffMainRunes(
			strToLiteralRunes(localSrc), strToLiteralRunes(localDst), false,
		)
	})

	err = ctx.Err()
	if err != nil {
		return 0, fmt.Errorf("compute byte-level rename diff: %w", err)
	}

	common := 0

	for _, localEdit := range localDiffs {
		if localEdit.Type == diffmatchpatch.DiffEqual {
			common += utf8.RuneCountInString(localEdit.Text)
		}
	}

	return common, nil
}

func calcLinePositions(text string) []int {
	if text == "" {
		return []int{0}
	}

	lines := strings.Split(text, "\n")
	positions := make([]int, len(lines)+1)

	accum := 0
	for i, l := range lines {
		positions[i] = accum
		accum += len(l) + 1 // +1 for \n
	}

	if len(lines) > 0 && lines[len(lines)-1] != "\n" {
		accum--
	}

	positions[len(lines)] = accum

	return positions
}

func strToLiteralRunes(s string) []rune {
	lrunes := make([]rune, len(s))
	for i, b := range []byte(s) {
		lrunes[i] = rune(b)
	}

	return lrunes
}

type sortableChange struct {
	change *object.Change
	hash   plumbing.Hash
}

type sortableChanges []sortableChange

// Less orders changes by their blob hash. The comparison must stop at the first byte which
// differs: continuing past it lets a later smaller byte outvote an earlier greater one, so both
// a.Less(b) and b.Less(a) can hold. sort.Sort then produces an arbitrary order, and because
// matchExactRenames walks the added and deleted lists as a merge scan, identical hashes end up on
// opposite sides of the cursor and an exact rename is reported as a delete plus a create instead.
func (change *sortableChange) Less(other *sortableChange) bool {
	for x := range len(change.hash) {
		if change.hash[x] != other.hash[x] {
			return change.hash[x] < other.hash[x]
		}
	}

	return false
}

func (slice sortableChanges) Len() int {
	return len(slice)
}

func (slice sortableChanges) Less(i, j int) bool {
	return slice[i].Less(&slice[j])
}

func (slice sortableChanges) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

type sortableBlob struct {
	change *object.Change
	size   int64
}

type sortableBlobs []sortableBlob

// Less orders blobs by size, then by identity. The identity tie-break is what makes this a
// total order: the sort at classifyRenameBlobs is not stable, and the resulting position
// decides which candidate the greedy matcher tries first, so equal-sized blobs left in an
// arbitrary relative order would leak that arbitrariness into the rename pairings.
func (change *sortableBlob) Less(other *sortableBlob) bool {
	if change.size != other.size {
		return change.size < other.size
	}

	return change.identity() < other.identity()
}

// identity is a stable per-change key. A change is either an addition (From is empty) or a
// deletion (To is empty), so concatenating both sides yields one comparable string either way.
func (change *sortableBlob) identity() string {
	if change.change == nil {
		return ""
	}

	from, to := change.change.From, change.change.To

	return from.Name + "\x00" + from.TreeEntry.Hash.String() +
		"\x00" + to.Name + "\x00" + to.TreeEntry.Hash.String()
}

func (slice sortableBlobs) Len() int {
	return len(slice)
}

func (slice sortableBlobs) Less(i, j int) bool {
	return slice[i].Less(&slice[j])
}

func (slice sortableBlobs) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

type candidateDistance struct {
	Candidate int
	Distance  int
}

func sortRenameCandidates(candidates []int, origin string, nameGetter func(int) string) {
	distances := make([]candidateDistance, len(candidates))
	ctx := levenshtein.Context{}

	for i, x := range candidates {
		name := filepath.Base(nameGetter(x))
		distances[i] = candidateDistance{x, ctx.Distance(origin, name)}
	}

	// Ties are common — moved files often share a basename length — and the first candidate
	// that passes blobsAreClose wins, so the tie-break on the candidate index is what keeps
	// the choice reproducible under a non-stable sort.
	sort.Slice(distances, func(i, j int) bool {
		if distances[i].Distance != distances[j].Distance {
			return distances[i].Distance < distances[j].Distance
		}

		return distances[i].Candidate < distances[j].Candidate
	})

	for i, cd := range distances {
		candidates[i] = cd.Candidate
	}
}

var _ = core.RegisterPipelineItem(&RenameAnalysis{})
