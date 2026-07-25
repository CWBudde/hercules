package leaves

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/cwbudde/hercules/internal/burndown"
	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/rbtree"
)

// LegacyBurndownAnalysis allows to gather the line burndown statistics for a Git repository.
// It is a LeafPipelineItem.
// Reference: https://erikbern.com/2016/12/05/the-half-life-of-code.html
type LegacyBurndownAnalysis struct {
	// Granularity sets the size of each band - the number of ticks it spans.
	// Smaller values provide better resolution but require more work and eat more
	// memory. 30 ticks is usually enough.
	Granularity int
	// Sampling sets how detailed is the statistic - the size of the interval in
	// ticks between consecutive measurements. It may not be greater than Granularity. Try 15 or 30.
	Sampling int

	// TrackFiles enables or disables the fine-grained per-file burndown analysis.
	// It does not change the project level burndown results.
	TrackFiles bool

	// PeopleNumber is the number of developers for which to collect the burndown stats. 0 disables it.
	PeopleNumber int

	// HibernationThreshold sets the hibernation threshold for the underlying
	// RBTree allocator. It is useful to trade CPU time for reduced peak memory consumption
	// if there are many branches.
	HibernationThreshold int

	// HibernationToDisk specifies whether the hibernated RBTree allocator must be saved on disk
	// rather than kept in memory.
	HibernationToDisk bool

	// HibernationDirectory is the name of the temporary directory to use for saving hibernated
	// RBTree allocators.
	HibernationDirectory string

	// Debug activates the debugging mode. Analyse() runs slower in this mode
	// but it accurately checks all the intermediate states for invariant
	// violations.
	Debug bool

	// Repository points to the analysed Git repository struct from go-git.
	repository *git.Repository
	// globalHistory is the daily deltas of daily line counts.
	// E.g. tick 0: tick 0 +50 lines
	//      tick 10: tick 0 -10 lines; tick 10 +20 lines
	//      tick 12: tick 0 -5 lines; tick 10 -3 lines; tick 12 +10 lines
	// map [0] [0] = 50
	// map[10] [0] = -10
	// map[10][10] = 20
	// map[12] [0] = -5
	// map[12][10] = -3
	// map[12][12] = 10
	globalHistory sparseHistory
	// fileHistories is the daily deltas of each file's daily line counts.
	fileHistories map[string]sparseHistory
	// peopleHistories is the daily deltas of each person's daily line counts.
	peopleHistories []sparseHistory
	// files is the mapping <file path> -> *File.
	files map[string]*linehistory.File
	// fileAllocator is the allocator for RBTree-s in `files`.
	fileAllocator *rbtree.Allocator
	// hibernatedFileName is the path to the serialized `fileAllocator`.
	hibernatedFileName string
	// mergedFiles is used during merges to record the real file hashes
	mergedFiles map[string]bool
	// mergedAuthor of the processed merge commit
	mergedAuthor int
	// renames is a quick and dirty solution for the "future branch renames" problem.
	renames map[string]string
	// deletions is a quick and dirty solution for the "real merge removals" problem.
	deletions map[string]bool
	// matrix is the mutual deletions and self insertions.
	matrix []map[int]int64

	// TickSize indicates the size of each time granule: day, hour, week, etc.
	tickSize time.Duration
	// tick is the most recent tick index processed.
	tick int
	// previousTick is the tick from the previous sample period -
	// different from TicksSinceStart.previousTick.
	previousTick int
	// references IdentityDetector.ReversedPeopleDict
	reversedPeopleDict []string

	l core.Logger
}

const (
	// ConfigLegacyBurndownHibernationThreshold sets the hibernation threshold for the underlying
	// RBTree allocator. It is useful to trade CPU time for reduced peak memory consumption
	// if there are many branches.
	ConfigLegacyBurndownHibernationThreshold = "LegacyBurndown.HibernationThreshold"
	// ConfigLegacyBurndownHibernationToDisk sets whether the hibernated RBTree allocator must be saved
	// on disk rather than kept in memory.
	ConfigLegacyBurndownHibernationToDisk = "LegacyBurndown.HibernationOnDisk"
	// ConfigLegacyBurndownHibernationDirectory sets the name of the temporary directory to use for
	// saving hibernated RBTree allocators.
	ConfigLegacyBurndownHibernationDirectory = "LegacyBurndown.HibernationDirectory"
	// ConfigLegacyBurndownDebug enables some extra debug assertions.
	ConfigLegacyBurndownDebug = "LegacyBurndown.Debug"
)

// DenseHistory is the matrix [number of samples][number of bands] -> number of lines.
//
//	y                  x
type DenseHistory = burndown.DenseHistory

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (analyser *LegacyBurndownAnalysis) Name() string {
	return "LegacyBurndown"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (analyser *LegacyBurndownAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (analyser *LegacyBurndownAnalysis) Requires() []string {
	return []string{
		items.DependencyFileDiff, items.DependencyTreeChanges, items.DependencyBlobCache,
		items.DependencyTick, identity.DependencyAuthor,
	}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (analyser *LegacyBurndownAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name: ConfigLegacyBurndownHibernationThreshold,
			Description: "The minimum size for the allocated memory in each branch to be compressed." +
				"0 disables this optimization. Lower values trade CPU time more. Sane examples: Nx1000.",
			Flag:    "legacy-burndown-hibernation-threshold",
			Type:    core.IntConfigurationOption,
			Default: 0,
		}, {
			Name: ConfigLegacyBurndownHibernationToDisk,
			Description: "Save hibernated RBTree allocators to disk rather than keep it in memory; " +
				"requires --legacy-burndown-hibernation-threshold to be greater than zero.",
			Flag:    "legacy-burndown-hibernation-disk",
			Type:    core.BoolConfigurationOption,
			Default: false,
		}, {
			Name: ConfigLegacyBurndownHibernationDirectory,
			Description: "Temporary directory where to save the hibernated RBTree allocators; " +
				"requires --legacy-burndown-hibernation-disk.",
			Flag:    "legacy-burndown-hibernation-dir",
			Type:    core.PathConfigurationOption,
			Default: "",
		}, {
			Name:        ConfigLegacyBurndownDebug,
			Description: "Validate the trees at each step.",
			Flag:        "legacy-burndown-debug",
			Type:        core.BoolConfigurationOption,
			Default:     false,
		},
	}

	return append(BurndownSharedOptions[:], options[:]...)
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (analyser *LegacyBurndownAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		analyser.l = l
	} else {
		analyser.l = core.NewLogger()
	}

	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		analyser.tickSize = val
	}

	if val, exists := facts[ConfigBurndownGranularity].(int); exists {
		analyser.Granularity = val
	}

	if val, exists := facts[ConfigBurndownSampling].(int); exists {
		analyser.Sampling = val
	}

	if val, exists := facts[ConfigBurndownTrackFiles].(bool); exists {
		analyser.TrackFiles = val
	}

	if people, exists := facts[ConfigBurndownTrackPeople].(bool); people {
		if val, exists := facts[identity.FactIdentityDetectorReversedPeopleDict].([]string); exists {
			analyser.reversedPeopleDict = val
			analyser.PeopleNumber = len(val)
		}
	} else if exists {
		analyser.PeopleNumber = 0
	}

	if val, exists := facts[ConfigLegacyBurndownHibernationThreshold].(int); exists {
		analyser.HibernationThreshold = val
	}

	if val, exists := facts[ConfigLegacyBurndownHibernationToDisk].(bool); exists {
		analyser.HibernationToDisk = val
	}

	if val, exists := facts[ConfigLegacyBurndownHibernationDirectory].(string); exists {
		analyser.HibernationDirectory = val
	}

	if val, exists := facts[ConfigLegacyBurndownDebug].(bool); exists {
		analyser.Debug = val
	}

	return nil
}

func (analyser *LegacyBurndownAnalysis) ConfigureUpstream(_ map[string]any) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (analyser *LegacyBurndownAnalysis) Flag() string {
	return "legacy-burndown"
}

// Description returns the text which explains what the analysis is doing.
func (analyser *LegacyBurndownAnalysis) Description() string {
	return "Line burndown stats indicate the numbers of lines which were last edited within " +
		"specific time intervals through time. Search for \"git-of-theseus\" in the internet."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (analyser *LegacyBurndownAnalysis) Initialize(repository *git.Repository) error {
	analyser.l = core.NewLogger()
	if analyser.Granularity <= 0 {
		analyser.l.Warnf("adjusted the granularity to %d ticks\n",
			DefaultBurndownGranularity)
		analyser.Granularity = DefaultBurndownGranularity
	}

	if analyser.Sampling <= 0 {
		analyser.l.Warnf("adjusted the sampling to %d ticks\n",
			DefaultBurndownGranularity)
		analyser.Sampling = DefaultBurndownGranularity
	}

	if analyser.Sampling > analyser.Granularity {
		analyser.l.Warnf("granularity may not be less than sampling, adjusted to %d\n",
			analyser.Granularity)
		analyser.Sampling = analyser.Granularity
	}

	analyser.repository = repository
	analyser.globalHistory = sparseHistory{}

	analyser.fileHistories = map[string]sparseHistory{}
	if analyser.PeopleNumber < 0 {
		return fmt.Errorf("PeopleNumber is negative: %d", analyser.PeopleNumber)
	}

	analyser.peopleHistories = make([]sparseHistory, analyser.PeopleNumber)
	analyser.files = map[string]*linehistory.File{}
	analyser.fileAllocator = rbtree.NewAllocator()
	analyser.fileAllocator.HibernationThreshold = analyser.HibernationThreshold
	analyser.mergedFiles = map[string]bool{}
	analyser.mergedAuthor = core.AuthorMissing
	analyser.renames = map[string]string{}
	analyser.deletions = map[string]bool{}
	analyser.matrix = make([]map[int]int64, analyser.PeopleNumber)

	analyser.tick = 0
	analyser.previousTick = 0

	return nil
}

// Consume runs this PipelineItem on the next commit's data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (analyser *LegacyBurndownAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	if analyser.fileAllocator.Size() == 0 && len(analyser.files) > 0 {
		panic("LegacyBurndownAnalysis.Consume() was called on a hibernated instance")
	}

	author := deps[identity.DependencyAuthor].(int)
	tick := deps[items.DependencyTick].(int)

	analyser.tick = tick
	analyser.onNewTick()

	cache := deps[items.DependencyBlobCache].(map[plumbing.Hash]*items.CachedBlob)
	treeDiffs := deps[items.DependencyTreeChanges].(object.Changes)
	fileDiffs := deps[items.DependencyFileDiff].(map[string]items.FileDiffData)

	for _, change := range treeDiffs {
		action, _ := change.Action()
		var err error

		switch action {
		case merkletrie.Insert:
			err = analyser.handleInsertion(change, author, cache)
		case merkletrie.Delete:
			err = analyser.handleDeletion(change, author, cache)
		case merkletrie.Modify:
			err = analyser.handleModification(change, author, cache, fileDiffs)
		}

		if err != nil {
			return nil, err
		}
	}
	// in case there is a merge analyser.tick equals to TreeMergeMark
	analyser.tick = tick

	return noDependencies(), nil
}

// Fork clones this item. Everything is copied by reference except the files
// which are copied by value.

func (analyser *LegacyBurndownAnalysis) Fork(n int) []core.PipelineItem {
	result := make([]core.PipelineItem, n)
	for i := range result {
		clone := *analyser
		clone.files = map[string]*linehistory.File{}

		clone.fileAllocator = clone.fileAllocator.Clone()
		for key, file := range analyser.files {
			clone.files[key] = file.CloneShallow(clone.fileAllocator)
		}

		result[i] = &clone
	}

	return result
}

// Merge combines several items together. We apply the special file merging logic here.
func (analyser *LegacyBurndownAnalysis) Merge(branches []core.PipelineItem) {
	all := make([]*LegacyBurndownAnalysis, len(branches)+1)

	all[0] = analyser
	for i, branch := range branches {
		all[i+1] = branch.(*LegacyBurndownAnalysis)
	}

	keys := map[string]bool{}

	for _, burn := range all {
		for key, val := range burn.mergedFiles {
			// (*)
			// there can be contradicting flags,
			// e.g. item was renamed and a new item written on its place
			// this may be not exactly accurate
			keys[key] = keys[key] || val
		}
	}

	for key, val := range keys {
		if !val {
			for _, burn := range all {
				if f, exists := burn.files[key]; exists {
					f.Delete()
				}

				delete(burn.files, key)
			}

			continue
		}

		files := make([]*linehistory.File, 0, len(all))
		for _, burn := range all {
			file := burn.files[key]
			if file != nil {
				// file can be nil if it is considered binary in this branch
				files = append(files, file)
			}
		}

		if len(files) == 0 {
			// so we could be wrong in (*) and there is no such file eventually
			// it could be also removed in the merge commit itself
			continue
		}

		files[0].Merge(
			analyser.packPersonWithTick(analyser.mergedAuthor, analyser.tick),
			files[1:]...,
		)

		for _, burn := range all {
			if burn.files[key] != files[0] {
				if burn.files[key] != nil {
					burn.files[key].Delete()
				}

				burn.files[key] = files[0].CloneDeep(burn.fileAllocator)
			}
		}
	}

	analyser.onNewTick()
}

// Hibernate compresses the bound RBTree memory with the files.
func (analyser *LegacyBurndownAnalysis) Hibernate() error {
	analyser.fileAllocator.Hibernate()

	if analyser.HibernationToDisk {
		file, err := os.CreateTemp(analyser.HibernationDirectory, "*-hercules.bin")
		if err != nil {
			return err
		}

		analyser.hibernatedFileName = file.Name()

		err = file.Close()
		if err != nil {
			analyser.hibernatedFileName = ""
			return err
		}

		err = analyser.fileAllocator.Serialize(analyser.hibernatedFileName)
		if err != nil {
			analyser.hibernatedFileName = ""
			return err
		}
	}

	return nil
}

// Boot decompresses the bound RBTree memory with the files.
func (analyser *LegacyBurndownAnalysis) Boot() error {
	if analyser.hibernatedFileName != "" {
		err := analyser.fileAllocator.Deserialize(analyser.hibernatedFileName)
		if err != nil {
			return err
		}

		err = os.Remove(analyser.hibernatedFileName)
		if err != nil {
			return err
		}

		analyser.hibernatedFileName = ""
	}

	analyser.fileAllocator.Boot()

	return nil
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (analyser *LegacyBurndownAnalysis) Finalize() any {
	globalHistory, lastTick := analyser.groupSparseHistory(analyser.globalHistory, -1)
	fileHistories, fileOwnership := analyser.finalizeFiles(lastTick)
	peopleHistories := analyser.finalizePeopleHistories(globalHistory, lastTick)

	return BurndownResult{
		GlobalHistory: globalHistory, FileHistories: fileHistories, FileOwnership: fileOwnership,
		PeopleHistories: peopleHistories, PeopleMatrix: analyser.finalizePeopleMatrix(),
		tickSize: analyser.tickSize, reversedPeopleDict: analyser.reversedPeopleDict,
		sampling: analyser.Sampling, granularity: analyser.Granularity,
	}
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (analyser *LegacyBurndownAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	burndownResult, ok := result.(BurndownResult)
	if !ok {
		return fmt.Errorf("result is not a burndown result: '%v'", result)
	}

	if binary {
		return analyser.serializeBinary(&burndownResult, writer)
	}

	analyser.serializeText(&burndownResult, writer)

	return nil
}

// Deserialize converts the specified protobuf bytes to BurndownResult.
func (analyser *LegacyBurndownAnalysis) Deserialize(pbmessage []byte) (any, error) {
	msg := pb.BurndownAnalysisResults{}

	err := proto.Unmarshal(pbmessage, &msg)
	if err != nil {
		return nil, err
	}

	convertCSR := func(mat *pb.BurndownSparseMatrix) DenseHistory {
		res := make(DenseHistory, mat.GetNumberOfRows())
		for i := range mat.GetNumberOfRows() {
			res[i] = make([]int64, mat.GetNumberOfColumns())
			for j := range len(mat.GetRows()[i].GetColumns()) {
				res[i][j] = int64(mat.GetRows()[i].GetColumns()[j])
			}
		}

		return res
	}

	result := BurndownResult{
		GlobalHistory: convertCSR(msg.GetProject()),
		FileHistories: map[string]DenseHistory{},
		FileOwnership: map[string]map[int]int{},
		tickSize:      time.Duration(msg.GetTickSize()),

		granularity: int(msg.GetGranularity()),
		sampling:    int(msg.GetSampling()),
	}
	for i, mat := range msg.GetFiles() {
		result.FileHistories[mat.GetName()] = convertCSR(mat)
		ownership := map[int]int{}

		result.FileOwnership[mat.GetName()] = ownership
		for key, val := range msg.GetFilesOwnership()[i].GetValue() {
			ownership[int(key)] = int(val)
		}
	}

	result.reversedPeopleDict = make([]string, len(msg.GetPeople()))

	result.PeopleHistories = make([]DenseHistory, len(msg.GetPeople()))
	for i, mat := range msg.GetPeople() {
		result.PeopleHistories[i] = convertCSR(mat)
		result.reversedPeopleDict[i] = mat.GetName()
	}

	if msg.GetPeopleInteraction() != nil {
		result.PeopleMatrix = make(DenseHistory, msg.GetPeopleInteraction().GetNumberOfRows())
	}

	for i := 0; i < len(result.PeopleMatrix); i++ {
		result.PeopleMatrix[i] = make([]int64, msg.GetPeopleInteraction().GetNumberOfColumns())
		for j := int(msg.GetPeopleInteraction().GetIndptr()[i]); j < int(msg.GetPeopleInteraction().GetIndptr()[i+1]); j++ {
			result.PeopleMatrix[i][msg.GetPeopleInteraction().GetIndices()[j]] = msg.GetPeopleInteraction().GetData()[j]
		}
	}

	return result, nil
}

// MergeResults combines two BurndownResult-s together.
func (analyser *LegacyBurndownAnalysis) MergeResults(
	r1, r2 any, c1, c2 *core.CommonAnalysisResult,
) any {
	return (&BurndownAnalysis{}).MergeResults(r1, r2, c1, c2)
}

func (analyser *LegacyBurndownAnalysis) finalizeFiles(
	lastTick int,
) (map[string]DenseHistory, map[string]map[int]int) {
	histories := map[string]DenseHistory{}
	ownerships := map[string]map[int]int{}

	for key, history := range analyser.fileHistories {
		if len(history) == 0 {
			continue
		}

		histories[key], _ = analyser.groupSparseHistory(history, lastTick)
		file := analyser.files[key]
		previousLine := 0
		previousAuthor := core.AuthorMissing
		ownership := map[int]int{}
		ownerships[key] = ownership

		file.ForEach(func(line, value int) {
			length := line - previousLine
			if length > 0 {
				ownership[previousAuthor] += length
			}

			previousLine = line

			previousAuthor, _ = analyser.unpackPersonWithTick(value)
			if previousAuthor == core.AuthorMissing {
				previousAuthor = -1
			}
		})
	}

	return histories, ownerships
}

func (analyser *LegacyBurndownAnalysis) finalizePeopleHistories(
	global DenseHistory, lastTick int,
) []DenseHistory {
	histories := make([]DenseHistory, analyser.PeopleNumber)
	for i := range histories {
		history := analyser.peopleHistories[i]
		if len(history) > 0 {
			histories[i], _ = analyser.groupSparseHistory(history, lastTick)
		} else {
			histories[i] = make(DenseHistory, len(global))
			for j, row := range global {
				histories[i][j] = make([]int64, len(row))
			}
		}
	}

	return histories
}

func (analyser *LegacyBurndownAnalysis) finalizePeopleMatrix() DenseHistory {
	if len(analyser.matrix) == 0 {
		return nil
	}

	matrix := make(DenseHistory, analyser.PeopleNumber)
	for i := range matrix {
		matrix[i] = make([]int64, analyser.PeopleNumber+2)
		for key, value := range analyser.matrix[i] {
			switch key {
			case core.AuthorMissing:
				key = -1
			case authorSelf:
				key = -2
			}

			matrix[i][key+2] = value
		}
	}

	return matrix
}

func roundTime(t time.Time, d time.Duration, dir bool) int {
	if !dir {
		t = items.FloorTime(t, d)
	}

	ticks := float64(t.Unix()) / d.Seconds()
	if dir {
		return int(math.Ceil(ticks))
	}

	return int(math.Floor(ticks))
}

// mergeMatrices takes two [number of samples][number of bands] matrices,
// resamples them to ticks so that they become square, sums and resamples back to the
// least of (sampling1, sampling2) and (granularity1, granularity2).
func (analyser *LegacyBurndownAnalysis) mergeMatrices(
	m1, m2 DenseHistory, granularity1, sampling1, granularity2, sampling2 int, tickSize time.Duration,
	c1, c2 *core.CommonAnalysisResult,
) DenseHistory {
	//	defer print("mergeMatrices exit\n\n\n")
	//	print("mergeMatrices enter\n\n\n")
	commonMerged := c1.Copy()
	commonMerged.Merge(c2)

	var granularity, sampling int
	sampling = min(sampling1, sampling2)

	granularity = min(granularity1, granularity2)

	size := roundTime(commonMerged.EndTimeAsTime(), tickSize, true) -
		roundTime(commonMerged.BeginTimeAsTime(), tickSize, false)

	perTick := make([][]float32, size+granularity)
	for i := range perTick {
		perTick[i] = make([]float32, size+sampling)
	}

	//	var m runtime.MemStats
	//	runtime.ReadMemStats(&m)

	//	print("mergeMatrices Allocating: ", size+granularity, " x ", size+sampling, " = ", (size+granularity)*(size+sampling)*4/1024/1024, ", total ", m.Alloc/1024/1024, "\n")

	if len(m1) > 0 {
		addBurndownMatrix(m1, granularity1, sampling1, perTick,
			roundTime(c1.BeginTimeAsTime(), tickSize, false)-roundTime(commonMerged.BeginTimeAsTime(), tickSize, false))
	}

	if len(m2) > 0 {
		addBurndownMatrix(m2, granularity2, sampling2, perTick,
			roundTime(c2.BeginTimeAsTime(), tickSize, false)-roundTime(commonMerged.BeginTimeAsTime(), tickSize, false))
	}

	// convert daily to [][]int64
	result := make(DenseHistory, (size+sampling-1)/sampling)
	for i := range result {
		result[i] = make([]int64, (size+granularity-1)/granularity)

		sampledIndex := (i+1)*sampling - 1
		for j := range len(result[i]) {
			accum := float32(0)
			for k := j * granularity; k < (j+1)*granularity; k++ {
				accum += perTick[sampledIndex][k]
			}

			result[i][j] = int64(accum)
		}
	}

	//	runtime.ReadMemStats(&m)

	for i := range perTick {
		perTick[i] = nil
	}

	runtime.GC()

	//	var a runtime.MemStats
	//	runtime.ReadMemStats(&a)

	//	print("mergeMatrices Deallocated: ", (m.Alloc-a.Alloc)/1024/1024, "\n")

	return result
}

// Explode `matrix` so that it is daily sampled and has daily bands, shift by `offset` ticks
// and add to the accumulator. `daily` size is square and is guaranteed to fit `matrix` by
// the caller.
// Rows: *at least* len(matrix) * sampling + offset
// Columns: *at least* len(matrix[...]) * granularity + offset
// `matrix` can be sparse, so that the last columns which are equal to 0 are truncated.
func addBurndownMatrix(matrix DenseHistory, granularity, sampling int, accPerTick [][]float32, offset int) {
	burndown.AddBurndownMatrix(matrix, granularity, sampling, accPerTick, offset)
}

func (analyser *LegacyBurndownAnalysis) serializeText(result *BurndownResult, writer io.Writer) {
	(&BurndownAnalysis{}).serializeText(result, writer)
}

func (analyser *LegacyBurndownAnalysis) serializeBinary(result *BurndownResult, writer io.Writer) error {
	message := pb.BurndownAnalysisResults{
		Granularity: int32(result.granularity),
		Sampling:    int32(result.sampling),
		TickSize:    int64(result.tickSize),
	}
	if len(result.GlobalHistory) > 0 {
		message.Project = pb.ToBurndownSparseMatrix(result.GlobalHistory, "project")
	}

	if len(result.FileHistories) > 0 {
		message.Files = make([]*pb.BurndownSparseMatrix, len(result.FileHistories))
		message.FilesOwnership = make([]*pb.FilesOwnership, len(result.FileHistories))
		keys := sortedKeys(result.FileHistories)

		i := 0
		for _, key := range keys {
			message.Files[i] = pb.ToBurndownSparseMatrix(result.FileHistories[key], key)
			ownership := map[int32]int32{}

			message.FilesOwnership[i] = &pb.FilesOwnership{Value: ownership}
			for key, val := range result.FileOwnership[key] {
				ownership[int32(key)] = int32(val)
			}

			i++
		}
	}

	if len(result.PeopleHistories) > 0 {
		message.People = make(
			[]*pb.BurndownSparseMatrix, len(result.PeopleHistories),
		)
		for key, val := range result.PeopleHistories {
			if len(val) > 0 {
				message.People[key] = pb.ToBurndownSparseMatrix(val, result.reversedPeopleDict[key])
			}
		}
	}

	if result.PeopleMatrix != nil {
		message.PeopleInteraction = pb.DenseToCompressedSparseRowMatrix(result.PeopleMatrix)
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}

	_, err = writer.Write(serialized)

	return err
}

// We do a hack and store the tick in the first 14 bits and the author index in the last 18.
// Strictly speaking, int can be 64-bit and then the author index occupies 32+18 bits.
// This hack is needed to simplify the values storage inside File-s. We can compare
// different values together and they are compared as ticks for the same author.
func (analyser *LegacyBurndownAnalysis) packPersonWithTick(person, tick int) int {
	if analyser.PeopleNumber == 0 {
		return tick
	}

	result := tick & linehistory.TreeMergeMark

	if tick > linehistory.TreeMergeMark {
		log.Fatalf("tick > burndown.TreeMergeMark %d %d\n%s", tick, linehistory.TreeMergeMark, string(debug.Stack()))
		panic("tick >= burndown.TreeMergeMark")
	}

	result |= person << linehistory.TreeMaxBinPower

	if (person >= analyser.PeopleNumber) && (person != core.AuthorMissing) {
		log.Fatalf("person >= analyser.PeopleNumber %d %d\n%s", person, analyser.PeopleNumber, string(debug.Stack()))
		panic("person >= analyser.PeopleNumber")
	}

	// This effectively means max (16383 - 1) ticks (>44 years) and (262143 - 3) devs.
	// One tick less because burndown.TreeMergeMark = ((1 << 14) - 1) is a special tick.
	// Three devs less because:
	// - math.MaxUint32 is the special rbtree value with tick == TreeMergeMark (-1)
	// - core.AuthorMissing (-2)
	// - authorSelf (-3)
	return result
}

func (analyser *LegacyBurndownAnalysis) unpackPersonWithTick(value int) (int, int) {
	if analyser.PeopleNumber == 0 {
		return core.AuthorMissing, value
	}

	return value >> linehistory.TreeMaxBinPower, value & linehistory.TreeMergeMark
}

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
	_ plumbing.Hash, name string, author, tick, size int,
) (*linehistory.File, error) {
	updaters := make([]linehistory.Updater, 1, 4)

	updaters[0] = analyser.updateGlobal
	if analyser.TrackFiles {
		history := analyser.fileHistories[name]
		if history == nil {
			// can be not nil if the file was created in a future branch
			history = sparseHistory{}
		}

		analyser.fileHistories[name] = history

		updaters = append(updaters, func(_ *linehistory.File, currentTime, previousTime, delta int) {
			analyser.updateFile(history, currentTime, previousTime, delta)
		})
	}

	if analyser.PeopleNumber > 0 {
		updaters = append(updaters, analyser.updateAuthor)
		updaters = append(updaters, analyser.updateChurnMatrix)
		tick = analyser.packPersonWithTick(author, tick)
	}

	return linehistory.NewFile(0, tick, size, analyser.fileAllocator, updaters...), nil
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
		return fmt.Errorf("file %s already exists", name)
	}

	var hash plumbing.Hash
	if analyser.tick != linehistory.TreeMergeMark {
		hash = blob.Hash
	}

	file, err = analyser.newFile(hash, name, author, analyser.tick, lines)
	analyser.files[name] = file
	delete(analyser.deletions, name)

	if analyser.tick == linehistory.TreeMergeMark {
		analyser.mergedFiles[name] = true
	}

	return err
}

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
	if exists && err != nil {
		return fmt.Errorf("previous version of %s unexpectedly became binary", name)
	}

	if !exists {
		return nil
	}
	// Parallel independent file removals are incorrectly handled. The solution seems to be quite
	// complex, but feel free to suggest your ideas.
	// These edge cases happen *very* rarely, so we don't bother for now.
	tick := analyser.tick
	// Are we merging and this file has never been actually deleted in any branch?
	if analyser.tick == linehistory.TreeMergeMark && !analyser.deletions[name] {
		tick = 0
		// Early removal in one branch with pre-merge changes in another is not handled correctly.
	}

	analyser.deletions[name] = true
	file.Update(analyser.packPersonWithTick(author, tick), 0, 0, lines)
	file.Delete()
	delete(analyser.files, name)
	delete(analyser.fileHistories, name)

	stack := []string{name}
	for len(stack) > 0 {
		head := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		analyser.renames[head] = "" // TODO delete instead of zero value
		for key, val := range analyser.renames {
			if val == head {
				stack = append(stack, key)
			}
		}
	}

	if analyser.tick == linehistory.TreeMergeMark {
		analyser.mergedFiles[name] = false
	}

	return nil
}

func (analyser *LegacyBurndownAnalysis) handleModification(
	change *object.Change, author int, cache map[plumbing.Hash]*items.CachedBlob,
	diffs map[string]items.FileDiffData,
) error {
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

	// Check for binary changes
	blobFrom := cache[change.From.TreeEntry.Hash]
	_, errFrom := blobFrom.CountLines()
	blobTo := cache[change.To.TreeEntry.Hash]

	_, errTo := blobTo.CountLines()
	if !errors.Is(errFrom, errTo) {
		if errFrom != nil {
			// the file is no longer binary
			return analyser.handleInsertion(change, author, cache)
		}
		// the file became binary
		return analyser.handleDeletion(change, author, cache)
	} else if errFrom != nil {
		if errors.Is(errFrom, items.ErrBinary) {
			return nil
		}

		return fmt.Errorf("count lines in modified file %s: %w", change.To.Name, errFrom)
	}

	thisDiffs := diffs[change.To.Name]
	if file.Len() != thisDiffs.OldLinesOfCode {
		analyser.l.Infof("====TREE====\n%s", file.Dump())

		return fmt.Errorf("%s: internal integrity error src %d != %d %s -> %s",
			change.To.Name, thisDiffs.OldLinesOfCode, file.Len(),
			change.From.TreeEntry.Hash.String(), change.To.TreeEntry.Hash.String())
	}

	err := analyser.applyModificationDiffs(file, change, author, thisDiffs.Diffs)
	if err != nil {
		return err
	}

	if file.Len() != thisDiffs.NewLinesOfCode {
		return fmt.Errorf("%s: internal integrity error dst %d != %d %s -> %s",
			change.To.Name, thisDiffs.NewLinesOfCode, file.Len(),
			change.From.TreeEntry.Hash.String(), change.To.TreeEntry.Hash.String())
	}

	return nil
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
			return errors.New("DiffInsert may not appear after DiffInsert")
		}

		state.file.Update(
			state.analyser.packPersonWithTick(state.author, state.analyser.tick),
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
			return errors.New("DiffDelete may not appear after DiffInsert/DiffDelete")
		}

		state.pending = edit
	default:
		state.debugError(length, before)
		return fmt.Errorf("diff operation is not supported: %d", edit.Type)
	}

	return nil
}

func (state *legacyDiffState) flush() {
	if state.pending.Text == "" {
		return
	}

	length := utf8.RuneCountInString(state.pending.Text)

	packed := state.analyser.packPersonWithTick(state.author, state.analyser.tick)
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

func (analyser *LegacyBurndownAnalysis) handleRename(from, to string) error {
	if from == to {
		return nil
	}

	file, exists := analyser.files[from]
	if !exists {
		return fmt.Errorf("file %s > %s does not exist (files)", from, to)
	}

	delete(analyser.files, from)
	analyser.files[to] = file
	delete(analyser.deletions, to)

	if analyser.tick == linehistory.TreeMergeMark {
		analyser.mergedFiles[from] = false
	}

	if analyser.TrackFiles {
		history, err := analyser.historyAfterRename(from, to)
		if err != nil {
			return err
		}

		delete(analyser.fileHistories, from)
		analyser.fileHistories[to] = history
	}

	analyser.renames[from] = to

	return nil
}

func (analyser *LegacyBurndownAnalysis) historyAfterRename(from, to string) (sparseHistory, error) {
	if history := analyser.fileHistories[from]; history != nil {
		return history, nil
	}

	if _, exists := analyser.renames[""]; exists {
		panic("burndown renames tracking corruption")
	}

	known := map[string]bool{from: true}

	future, exists := analyser.renames[from]
	for exists {
		next, nextExists := analyser.renames[future]
		if !nextExists {
			break
		}

		if known[next] {
			future = ""

			for name := range known {
				if analyser.fileHistories[name] != nil {
					future = name
					break
				}
			}

			break
		}

		known[future] = true
		future, exists = next, true
	}

	if future == "" {
		return sparseHistory{}, nil
	}

	history := analyser.fileHistories[future]
	if history == nil {
		return nil, fmt.Errorf("file %s > %s (%s) does not exist (histories)", from, to, future)
	}

	return history, nil
}

func (analyser *LegacyBurndownAnalysis) groupSparseHistory(
	history sparseHistory, lastTick int,
) (DenseHistory, int) {
	if len(history) == 0 {
		panic("empty history")
	}

	var ticks []int
	for tick := range history {
		ticks = append(ticks, tick)
	}

	sort.Ints(ticks)

	if lastTick >= 0 {
		if ticks[len(ticks)-1] < lastTick {
			ticks = append(ticks, lastTick)
		} else if ticks[len(ticks)-1] > lastTick {
			panic("ticks corruption")
		}
	} else {
		lastTick = ticks[len(ticks)-1]
	}
	// [y][x]
	// y - sampling
	// x - granularity
	samples := lastTick/analyser.Sampling + 1
	bands := lastTick/analyser.Granularity + 1

	result := make(DenseHistory, samples)
	for i := range bands {
		result[i] = make([]int64, bands)
	}

	prevsi := 0

	for _, tick := range ticks {
		si := tick / analyser.Sampling
		if si > prevsi {
			state := result[prevsi]
			for i := prevsi + 1; i <= si; i++ {
				copy(result[i], state)
			}

			prevsi = si
		}

		sample := result[si]
		for t, value := range history[tick].deltas {
			sample[t/analyser.Granularity] += value
		}
	}

	return result, lastTick
}

var _ = core.RegisterPipelineItem(&LegacyBurndownAnalysis{})
