package plumbing

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
			return fmt.Errorf("negative renames detection timeout is not allowed: %d", val)
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

	return nil
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
	beginTime := time.Now()
	changes := deps[DependencyTreeChanges].(object.Changes)
	cache := deps[DependencyBlobCache].(map[plumbing.Hash]*CachedBlob)

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
		beginTime, addedBlobs, deletedBlobs, cache, maxCandidates, changes.Len(),
	)
	if err != nil {
		return nil, err
	}

	reducedChanges = appendRenameResults(
		reducedChanges, matches, addedBlobs, deletedBlobs, smallChanges,
	)

	return map[string]any{DependencyTreeChanges: reducedChanges}, nil
}

func cloneSortableBlobs(blobs sortableBlobs) sortableBlobs {
	cloned := make(sortableBlobs, len(blobs))
	copy(cloned, blobs)

	return cloned
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
	*CachedBlob, []int, sortableBlobs, map[plumbing.Hash]*CachedBlob, int, <-chan bool,
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

func (ra *RenameAnalysis) matchSimilarRenames(
	beginTime time.Time,
	addedBlobs, deletedBlobs sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob,
	maxCandidates, changeCount int,
) (object.Changes, sortableBlobs, sortableBlobs, error) {
	finished := make(chan bool, 2)
	finishedA := make(chan bool, 1)
	finishedB := make(chan bool, 1)
	errs := make(chan error)
	matchesA := make(object.Changes, 0, changeCount)
	matchesB := make(object.Changes, 0, changeCount)
	addedBlobsA := addedBlobs
	deletedBlobsA := deletedBlobs
	addedBlobsB := cloneSortableBlobs(addedBlobs)
	deletedBlobsB := cloneSortableBlobs(deletedBlobs)

	wg := sync.WaitGroup{}
	matchA := func() {
		defer func() { finished <- true; wg.Done() }()

		matchesA, addedBlobsA, deletedBlobsA = ra.matchDeletedBlobs(
			beginTime, addedBlobsA, deletedBlobsA, cache, maxCandidates, finished, errs,
		)

		finishedA <- true
	}
	matchB := func() {
		defer func() { finished <- true; wg.Done() }()

		matchesB, addedBlobsB, deletedBlobsB = ra.matchAddedBlobs(
			beginTime, addedBlobsB, deletedBlobsB, cache, maxCandidates, finished, errs,
		)

		finishedB <- true
	}
	// run two functions in parallel, and take the result from the one which finished earlier
	wg.Add(2)

	go matchA()
	go matchB()

	wg.Wait()
	var matches object.Changes

	select {
	case err := <-errs:
		return nil, nil, nil, err
	case <-finishedA:
		addedBlobs = addedBlobsA
		deletedBlobs = deletedBlobsA
		matches = matchesA
	case <-finishedB:
		addedBlobs = addedBlobsB
		deletedBlobs = deletedBlobsB
		matches = matchesB
	default:
		panic("Impossible happened: two functions returned without an error " +
			"but no results from both")
	}

	return matches, addedBlobs, deletedBlobs, nil
}

func (ra *RenameAnalysis) matchDeletedBlobs(
	start time.Time, added, deleted sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob, maxCandidates int,
	finished <-chan bool, errs chan<- error,
) (object.Changes, sortableBlobs, sortableBlobs) {
	return ra.matchBlobs(
		start, added, deleted, cache, maxCandidates, finished, errs, true,
	)
}

func (ra *RenameAnalysis) matchAddedBlobs(
	start time.Time, added, deleted sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob, maxCandidates int,
	finished <-chan bool, errs chan<- error,
) (object.Changes, sortableBlobs, sortableBlobs) {
	return ra.matchBlobs(
		start, added, deleted, cache, maxCandidates, finished, errs, false,
	)
}

func (ra *RenameAnalysis) matchBlobs(
	start time.Time, added, deleted sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob, maxCandidates int,
	finished <-chan bool, errs chan<- error, matchDeleted bool,
) (object.Changes, sortableBlobs, sortableBlobs) {
	var matches object.Changes
	primary, secondary, matchCandidate := ra.renameMatchDirection(added, deleted, matchDeleted)

	secondaryStart := 0

	for primaryIndex := 0; primaryIndex < primary.Len() &&
		time.Since(start) < ra.Timeout; primaryIndex++ {
		blob := cache[renameBlobHash(primary[primaryIndex], matchDeleted)]
		size := primary[primaryIndex].size

		for secondaryStart < secondary.Len() &&
			!ra.sizesAreClose(size, secondary[secondaryStart].size) {
			secondaryStart++
		}

		candidates := renameCandidates(secondaryStart, secondary.Len(), func(index int) bool {
			return ra.sizesAreClose(size, secondary[index].size)
		})
		sortRenameCandidates(
			candidates,
			filepath.Base(renameBlobName(primary[primaryIndex], matchDeleted)),
			func(index int) string {
				return renameBlobName(secondary[index], !matchDeleted)
			},
		)

		match, err := matchCandidate(blob, candidates, secondary, cache, maxCandidates, finished)
		if err != nil {
			errs <- err
			return matches, added, deleted
		}

		if match >= 0 {
			matches = append(matches, matchedRename(primary[primaryIndex], secondary[match], matchDeleted))
			primary = append(primary[:primaryIndex], primary[primaryIndex+1:]...)
			primaryIndex--

			secondary = append(secondary[:match], secondary[match+1:]...)
		}
	}

	if matchDeleted {
		return matches, secondary, primary
	}

	return matches, primary, secondary
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
	blob *CachedBlob, candidates []int, added sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob, limit int, finished <-chan bool,
) (int, error) {
	for candidateIndex, index := range candidates {
		select {
		case <-finished:
			return -1, nil
		default:
		}

		if candidateIndex > limit {
			break
		}

		areClose, err := ra.blobsAreClose(blob, cache[added[index].change.To.TreeEntry.Hash])
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
	blob *CachedBlob, candidates []int, deleted sortableBlobs,
	cache map[plumbing.Hash]*CachedBlob, limit int, finished <-chan bool,
) (int, error) {
	for candidateIndex, index := range candidates {
		select {
		case <-finished:
			return -1, nil
		default:
		}

		if candidateIndex > limit {
			break
		}

		areClose, err := ra.blobsAreClose(blob, cache[deleted[index].change.From.TreeEntry.Hash])
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
	cleanReturn := false
	defer ra.warnUncleanBlobComparison(&cleanReturn, blob1, blob2)

	if binary, similar := ra.binaryBlobsAreClose(blob1, blob2); binary {
		cleanReturn = true
		return similar, nil
	}

	src, dst := string(blob1.Data), string(blob2.Data)
	maxSize := internal.Max(1, internal.Max(utf8.RuneCountInString(src), utf8.RuneCountInString(dst)))

	diffs, srcPositions, dstPositions := renameLineDiffs(src, dst)
	var common, posSrc, prevPosSrc, posDst int
	possibleDelInsBlock := false

	for _, edit := range diffs {
		switch edit.Type {
		case diffmatchpatch.DiffDelete:
			possibleDelInsBlock = true
			prevPosSrc = posSrc
			posSrc += utf8.RuneCountInString(edit.Text)
		case diffmatchpatch.DiffInsert:
			nextPosDst := posDst + utf8.RuneCountInString(edit.Text)

			if possibleDelInsBlock {
				possibleDelInsBlock = false
				common += delInsCommonRunes(
					src, dst, srcPositions, dstPositions, prevPosSrc, posSrc, posDst, nextPosDst,
				)
			}

			posDst = nextPosDst
		case diffmatchpatch.DiffEqual:
			possibleDelInsBlock = false
			step := utf8.RuneCountInString(edit.Text)
			// for i := range edit.Text does *not* work
			// idk why, but `i` appears to be bigger than the number of runes
			for i := range step {
				common += srcPositions[posSrc+i+1] - srcPositions[posSrc+i]
			}

			posSrc += step
			posDst += step
		}

		if possibleDelInsBlock {
			continue
		}
		// supposing that the rest of the lines are the same (they are not - too optimistic),
		// estimate the maximum similarity and exit the loop if it lower than our threshold
		if decided, similar := ra.similarityDecision(
			common, maxSize, len(src)-srcPositions[posSrc], len(dst)-dstPositions[posDst],
		); decided {
			cleanReturn = true
			return similar, nil
		}
	}
	// the very last "overly optimistic" estimate was actually precise, so since we are still here
	// the blobs are similar
	cleanReturn = true

	return true, nil
}

func renameLineDiffs(src, dst string) ([]diffmatchpatch.Diff, []int, []int) {
	// Compute the line-by-line diff, then the char-level diffs of the del-ins blocks.
	// The ignored []string mapping is approximate and collides for huge files.
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = time.Hour
	srcLineRunes, dstLineRunes, _ := dmp.DiffLinesToRunes(src, dst)
	diffs := dmp.DiffMainRunes(srcLineRunes, dstLineRunes, false)

	return diffs, calcLinePositions(src), calcLinePositions(dst)
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

func delInsCommonRunes(src, dst string, srcPositions, dstPositions []int,
	prevPosSrc, posSrc, posDst, nextPosDst int,
) int {
	srcSize := srcPositions[posSrc] - srcPositions[prevPosSrc]

	dstSize := dstPositions[nextPosDst] - dstPositions[posDst]
	if internal.Max(srcSize, dstSize) >= RenameAnalysisByteDiffSizeThreshold {
		return 0
	}

	localDmp := diffmatchpatch.New()
	localDmp.DiffTimeout = time.Hour
	localSrc := src[srcPositions[prevPosSrc]:srcPositions[posSrc]]
	localDst := dst[dstPositions[posDst]:dstPositions[nextPosDst]]
	localDiffs := localDmp.DiffMainRunes(
		strToLiteralRunes(localSrc), strToLiteralRunes(localDst), false,
	)
	common := 0

	for _, localEdit := range localDiffs {
		if localEdit.Type == diffmatchpatch.DiffEqual {
			common += utf8.RuneCountInString(localEdit.Text)
		}
	}

	return common
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

func (change *sortableChange) Less(other *sortableChange) bool {
	for x := range 20 {
		if change.hash[x] < other.hash[x] {
			return true
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

func (change *sortableBlob) Less(other *sortableBlob) bool {
	return change.size < other.size
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

	sort.Slice(distances, func(i, j int) bool {
		return distances[i].Distance < distances[j].Distance
	})

	for i, cd := range distances {
		candidates[i] = cd.Candidate
	}
}

var _ = core.RegisterPipelineItem(&RenameAnalysis{})
