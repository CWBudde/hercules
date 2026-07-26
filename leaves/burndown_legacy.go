package leaves

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
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

var (
	errNegativePeopleNumber     = errors.New("PeopleNumber is negative")
	errFileAlreadyExists        = errors.New("file already exists")
	errPreviousFileBecameBinary = errors.New("previous version unexpectedly became binary")
	errLegacyIntegrity          = errors.New("internal integrity error")
	errDiffInsertAfterInsert    = errors.New("DiffInsert may not appear after DiffInsert")
	errDiffDeleteAfterEdit      = errors.New("DiffDelete may not appear after DiffInsert/DiffDelete")
	errUnsupportedDiffOperation = errors.New("diff operation is not supported")
	errLegacyFileMissing        = errors.New("file does not exist")
	errLegacyHistoryMissing     = errors.New("file history does not exist")
	// ErrLegacyBurndownAuthorCapacity indicates that people tracking cannot represent an author.
	ErrLegacyBurndownAuthorCapacity = errors.New("legacy burndown author exceeds packed capacity")
	// ErrLegacyBurndownTickCapacity indicates that line state cannot represent a tick.
	ErrLegacyBurndownTickCapacity = errors.New("legacy burndown tick exceeds packed capacity")
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
	// deletedFileHistories retains histories which may still be live in a parallel branch.
	// A later change or merge insertion at the same path restores the original map.
	deletedFileHistories map[string]sparseHistory
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

	return append(burndownSharedOptions(), options[:]...)
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (analyser *LegacyBurndownAnalysis) Configure(facts map[string]any) error {
	configureBurndownBasics(
		facts, &analyser.l, &analyser.tickSize, &analyser.Granularity,
		&analyser.Sampling, &analyser.TrackFiles,
	)

	if people, exists := facts[ConfigBurndownTrackPeople].(bool); people {
		if val, exists := facts[identity.FactIdentityDetectorReversedPeopleDict].([]string); exists {
			analyser.reversedPeopleDict = val
			analyser.PeopleNumber = len(val)
		}
	} else if exists {
		analyser.PeopleNumber = 0
	}

	assignFact(facts, ConfigLegacyBurndownHibernationThreshold, &analyser.HibernationThreshold)
	assignFact(facts, ConfigLegacyBurndownHibernationToDisk, &analyser.HibernationToDisk)
	assignFact(facts, ConfigLegacyBurndownHibernationDirectory, &analyser.HibernationDirectory)
	assignFact(facts, ConfigLegacyBurndownDebug, &analyser.Debug)

	return analyser.validateConfiguredPackedCapacity(facts)
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
	analyser.Dispose()

	if analyser.l == nil {
		analyser.l = core.NewLogger()
	}

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
	analyser.deletedFileHistories = map[string]sparseHistory{}

	if analyser.PeopleNumber < 0 {
		return fmt.Errorf("%w: %d", errNegativePeopleNumber, analyser.PeopleNumber)
	}

	if analyser.PeopleNumber > int(core.AuthorMissing) {
		return fmt.Errorf(
			"%w: got %d people, maximum is %d",
			ErrLegacyBurndownAuthorCapacity, analyser.PeopleNumber, core.AuthorMissing,
		)
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

	author, tick, cache, treeDiffs, fileDiffs, err := legacyBurndownDependencies(deps)
	if err != nil {
		return nil, err
	}

	err = analyser.validatePackedOwnership(author, tick)
	if err != nil {
		return nil, err
	}

	analyser.tick = tick
	analyser.onNewTick()

	isMerge, _ := deps[core.DependencyIsMerge].(bool)
	if isMerge {
		analyser.mergedAuthor = author
		analyser.tick = linehistory.TreeMergeMark
	}

	for _, change := range treeDiffs {
		action, _ := change.Action()
		var changeErr error

		switch action {
		case merkletrie.Insert:
			changeErr = analyser.handleInsertion(change, author, cache)
		case merkletrie.Delete:
			changeErr = analyser.handleDeletion(change, author, cache)
		case merkletrie.Modify:
			changeErr = analyser.handleModification(change, author, cache, fileDiffs)
		}

		if changeErr != nil {
			return nil, changeErr
		}
	}
	// in case there is a merge analyser.tick equals to TreeMergeMark
	analyser.tick = tick

	return noDependencies(), nil
}

func legacyBurndownDependencies(
	deps map[string]any,
) (
	int,
	int,
	map[plumbing.Hash]*items.CachedBlob,
	object.Changes,
	map[string]items.FileDiffData,
	error,
) {
	author, err := requiredFact[int](deps, identity.DependencyAuthor)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	tick, err := requiredFact[int](deps, items.DependencyTick)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	cache, err := requiredFact[map[plumbing.Hash]*items.CachedBlob](deps, items.DependencyBlobCache)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	treeDiffs, err := requiredFact[object.Changes](deps, items.DependencyTreeChanges)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	fileDiffs, err := requiredFact[map[string]items.FileDiffData](deps, items.DependencyFileDiff)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	return author, tick, cache, treeDiffs, fileDiffs, nil
}

// Fork clones this item. Everything is copied by reference except the files
// which are copied by value.

func (analyser *LegacyBurndownAnalysis) Fork(cloneCount int) []core.PipelineItem {
	result := make([]core.PipelineItem, cloneCount)
	for cloneIndex := range result {
		clone := *analyser
		clone.files = map[string]*linehistory.File{}

		clone.fileAllocator = clone.fileAllocator.Clone()
		for key, file := range analyser.files {
			clone.files[key] = file.CloneShallow(clone.fileAllocator)
		}

		result[cloneIndex] = &clone
	}

	return result
}

// Merge combines several items together. We apply the special file merging logic here.
func (analyser *LegacyBurndownAnalysis) Merge(branches []core.PipelineItem) {
	all := legacyBurndownBranches(analyser, branches)
	for fileName, retained := range mergedLegacyFiles(all) {
		mergeLegacyBranchFile(analyser, all, fileName, retained)
	}

	analyser.onNewTick()
}

func legacyBurndownBranches(
	analyser *LegacyBurndownAnalysis,
	branches []core.PipelineItem,
) []*LegacyBurndownAnalysis {
	all := make([]*LegacyBurndownAnalysis, len(branches)+1)
	all[0] = analyser

	for index, branch := range branches {
		legacyBranch, ok := branch.(*LegacyBurndownAnalysis)
		if !ok {
			panic("legacy burndown branch has an unexpected type")
		}

		all[index+1] = legacyBranch
	}

	return all
}

func mergedLegacyFiles(branches []*LegacyBurndownAnalysis) map[string]bool {
	files := map[string]bool{}

	for _, branch := range branches {
		for fileName, retained := range branch.mergedFiles {
			// There can be contradicting flags, for example when an item was
			// renamed and a new item was written in its place.
			files[fileName] = files[fileName] || retained
		}
	}

	return files
}

func mergeLegacyBranchFile(
	analyser *LegacyBurndownAnalysis,
	branches []*LegacyBurndownAnalysis,
	fileName string,
	retained bool,
) {
	if !retained {
		deleteLegacyBranchFile(branches, fileName)
		return
	}

	files := existingLegacyBranchFiles(branches, fileName)
	if len(files) == 0 {
		// Contradicting merge flags can leave no file, as can a removal in the
		// merge commit itself.
		return
	}

	merged := files[0]
	merged.Merge(
		analyser.packPersonWithTick(analyser.mergedAuthor, analyser.tick),
		files[1:]...,
	)
	synchronizeLegacyBranchFile(branches, fileName, merged)
}

func deleteLegacyBranchFile(branches []*LegacyBurndownAnalysis, fileName string) {
	for _, branch := range branches {
		if file, exists := branch.files[fileName]; exists {
			file.Delete()
		}

		delete(branch.files, fileName)
		delete(branch.fileHistories, fileName)
		delete(branch.deletedFileHistories, fileName)
	}
}

func existingLegacyBranchFiles(
	branches []*LegacyBurndownAnalysis,
	fileName string,
) []*linehistory.File {
	files := make([]*linehistory.File, 0, len(branches))
	for _, branch := range branches {
		if file := branch.files[fileName]; file != nil {
			// A file can be nil if it is considered binary in this branch.
			files = append(files, file)
		}
	}

	return files
}

func synchronizeLegacyBranchFile(
	branches []*LegacyBurndownAnalysis,
	fileName string,
	merged *linehistory.File,
) {
	for _, branch := range branches {
		if branch.files[fileName] == merged {
			continue
		}

		if branch.files[fileName] != nil {
			branch.files[fileName].Delete()
		}

		branch.files[fileName] = merged.CloneDeep(branch.fileAllocator)
	}
}

// Hibernate compresses the bound RBTree memory with the files.
func (analyser *LegacyBurndownAnalysis) Hibernate() error {
	analyser.fileAllocator.Hibernate()

	if analyser.HibernationToDisk {
		file, err := os.CreateTemp(analyser.HibernationDirectory, "*-hercules.bin")
		if err != nil {
			return fmt.Errorf("create hibernation file: %w", err)
		}

		analyser.hibernatedFileName = file.Name()

		err = file.Close()
		if err != nil {
			removeErr := removeLegacyHibernationFile(analyser)

			return errors.Join(
				fmt.Errorf("close hibernation file %q: %w", file.Name(), err),
				removeErr,
			)
		}

		err = analyser.fileAllocator.Serialize(analyser.hibernatedFileName)
		if err != nil {
			hibernatedFileName := analyser.hibernatedFileName
			removeErr := removeLegacyHibernationFile(analyser)

			return errors.Join(
				fmt.Errorf("serialize hibernation state to %q: %w", hibernatedFileName, err),
				removeErr,
			)
		}
	}

	return nil
}

// Boot decompresses the bound RBTree memory with the files.
func (analyser *LegacyBurndownAnalysis) Boot() error {
	if analyser.hibernatedFileName != "" {
		hibernatedFileName := analyser.hibernatedFileName

		err := analyser.fileAllocator.Deserialize(hibernatedFileName)
		if err != nil {
			removeErr := removeLegacyHibernationFile(analyser)

			return errors.Join(
				fmt.Errorf("deserialize hibernation state from %q: %w", hibernatedFileName, err),
				removeErr,
			)
		}

		err = removeLegacyHibernationFile(analyser)
		if err != nil {
			return err
		}
	}

	analyser.fileAllocator.Boot()

	return nil
}

// Dispose removes temporary hibernation state left by a completed or failed run.
func (analyser *LegacyBurndownAnalysis) Dispose() {
	_ = removeLegacyHibernationFile(analyser)
}

func removeLegacyHibernationFile(analyser *LegacyBurndownAnalysis) error {
	hibernatedFileName := analyser.hibernatedFileName
	if hibernatedFileName == "" {
		return nil
	}

	err := os.Remove(hibernatedFileName)

	err = ignoreMissingHibernationFile(err)
	if err != nil {
		return fmt.Errorf("remove hibernation file %q: %w", hibernatedFileName, err)
	}

	analyser.hibernatedFileName = ""

	return nil
}

func ignoreMissingHibernationFile(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (analyser *LegacyBurndownAnalysis) Finalize() any {
	globalHistory, lastTick := analyser.groupSparseHistory(analyser.globalHistory, -1)
	fileHistories, fileOwnership := analyser.finalizeFiles(lastTick)
	peopleHistories := analyser.finalizePeopleHistories(globalHistory, lastTick)

	result := BurndownResult{
		GlobalHistory: globalHistory, FileHistories: fileHistories, FileOwnership: fileOwnership,
		PeopleHistories: peopleHistories, PeopleMatrix: analyser.finalizePeopleMatrix(),
		tickSize: analyser.tickSize, reversedPeopleDict: analyser.reversedPeopleDict,
		sampling: analyser.Sampling, granularity: analyser.Granularity,
	}

	err := validateBurndownResultBalances(&result, "legacy finalization")
	if err != nil {
		return err
	}

	return result
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (analyser *LegacyBurndownAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	burndownResult, ok := result.(BurndownResult)
	if !ok {
		return fmt.Errorf("%w: '%v'", errUnexpectedBurndownResult, result)
	}

	err := validateBurndownResultBalances(&burndownResult, "legacy serialization")
	if err != nil {
		return err
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

	err := unmarshalAnalysis(pbmessage, &msg)
	if err != nil {
		return nil, fmt.Errorf("unmarshal burndown result: %w", err)
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

		file := analyser.files[key]
		if file == nil {
			continue
		}

		histories[key], _ = analyser.groupSparseHistory(history, lastTick)
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
	for personIndex := range histories {
		history := analyser.peopleHistories[personIndex]
		if len(history) > 0 {
			histories[personIndex], _ = analyser.groupSparseHistory(history, lastTick)
		} else {
			histories[personIndex] = make(DenseHistory, len(global))
			for rowIndex, row := range global {
				histories[personIndex][rowIndex] = make([]int64, len(row))
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
	for personIndex := range matrix {
		matrix[personIndex] = make([]int64, analyser.PeopleNumber+2)
		for key, value := range analyser.matrix[personIndex] {
			switch key {
			case core.AuthorMissing:
				key = -1
			case authorSelf:
				key = -2
			}

			matrix[personIndex][key+2] = value
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
	matrix1, matrix2 DenseHistory, granularity1, sampling1, granularity2, sampling2 int, tickSize time.Duration,
	common1, common2 *core.CommonAnalysisResult,
) DenseHistory {
	//	defer print("mergeMatrices exit\n\n\n")
	//	print("mergeMatrices enter\n\n\n")
	commonMerged := common1.Copy()
	commonMerged.Merge(common2)

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

	if len(matrix1) > 0 {
		addBurndownMatrix(matrix1, granularity1, sampling1, perTick,
			roundTime(common1.BeginTimeAsTime(), tickSize, false)-
				roundTime(commonMerged.BeginTimeAsTime(), tickSize, false))
	}

	if len(matrix2) > 0 {
		addBurndownMatrix(matrix2, granularity2, sampling2, perTick,
			roundTime(common2.BeginTimeAsTime(), tickSize, false)-
				roundTime(commonMerged.BeginTimeAsTime(), tickSize, false))
	}

	// convert daily to [][]int64
	result := make(DenseHistory, (size+sampling-1)/sampling)
	for sampleIndex := range result {
		result[sampleIndex] = make([]int64, (size+granularity-1)/granularity)

		sampledIndex := (sampleIndex+1)*sampling - 1
		for bandIndex := range len(result[sampleIndex]) {
			accum := float32(0)
			for k := bandIndex * granularity; k < (bandIndex+1)*granularity; k++ {
				accum += perTick[sampledIndex][k]
			}

			result[sampleIndex][bandIndex] = int64(accum)
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
	granularity, err := intToProtoInt32(result.granularity, "legacy burndown granularity")
	if err != nil {
		return err
	}

	sampling, err := intToProtoInt32(result.sampling, "legacy burndown sampling")
	if err != nil {
		return err
	}

	message := pb.BurndownAnalysisResults{
		Granularity: granularity,
		Sampling:    sampling,
		TickSize:    int64(result.tickSize),
	}
	if len(result.GlobalHistory) > 0 {
		message.Project = pb.ToBurndownSparseMatrix(result.GlobalHistory, "project")
	}

	if len(result.FileHistories) > 0 {
		message.Files, message.FilesOwnership, err = legacyFilesToProto(result)
		if err != nil {
			return err
		}
	}

	if len(result.PeopleHistories) > 0 {
		message.People = legacyPeopleToProto(result)
	}

	if result.PeopleMatrix != nil {
		message.PeopleInteraction = pb.DenseToCompressedSparseRowMatrix(result.PeopleMatrix)
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal burndown result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write burndown result: %w", err)
	}

	return nil
}

func legacyFilesToProto(
	result *BurndownResult,
) ([]*pb.BurndownSparseMatrix, []*pb.FilesOwnership, error) {
	files := make([]*pb.BurndownSparseMatrix, len(result.FileHistories))
	filesOwnership := make([]*pb.FilesOwnership, len(result.FileHistories))

	for fileIndex, fileName := range sortedKeys(result.FileHistories) {
		files[fileIndex] = pb.ToBurndownSparseMatrix(result.FileHistories[fileName], fileName)

		ownership, err := legacyOwnershipToProto(result.FileOwnership[fileName])
		if err != nil {
			return nil, nil, err
		}

		filesOwnership[fileIndex] = &pb.FilesOwnership{Value: ownership}
	}

	return files, filesOwnership, nil
}

func legacyOwnershipToProto(ownership map[int]int) (map[int32]int32, error) {
	converted := make(map[int32]int32, len(ownership))

	for developer, lines := range ownership {
		developerID, err := intToProtoInt32(developer, "legacy burndown file owner")
		if err != nil {
			return nil, err
		}

		lineCount, err := intToProtoInt32(lines, "legacy burndown file ownership line count")
		if err != nil {
			return nil, err
		}

		converted[developerID] = lineCount
	}

	return converted, nil
}

func legacyPeopleToProto(result *BurndownResult) []*pb.BurndownSparseMatrix {
	people := make([]*pb.BurndownSparseMatrix, len(result.PeopleHistories))

	for developer, history := range result.PeopleHistories {
		if len(history) > 0 {
			people[developer] = pb.ToBurndownSparseMatrix(
				history, result.reversedPeopleDict[developer],
			)
		}
	}

	return people
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

	result |= person << linehistory.TreeMaxBinPower

	// This effectively means max (16383 - 1) ticks (>44 years) and (262143 - 3) devs.
	// One tick less because burndown.TreeMergeMark = ((1 << 14) - 1) is a special tick.
	// Three devs less because:
	// - math.MaxUint32 is the special rbtree value with tick == TreeMergeMark (-1)
	// - core.AuthorMissing (-2)
	// - authorSelf (-3)
	return result
}

func (analyser *LegacyBurndownAnalysis) packChangePersonWithTick(person, tick int) int {
	if tick == linehistory.TreeMergeMark {
		return linehistory.TreeMergeMark
	}

	return analyser.packPersonWithTick(person, tick)
}

func (analyser *LegacyBurndownAnalysis) validatePackedOwnership(person, tick int) error {
	if tick < 0 || tick >= linehistory.TreeMergeMark {
		return fmt.Errorf(
			"%w: got %d, allowed range is [0, %d]",
			ErrLegacyBurndownTickCapacity, tick, linehistory.TreeMergeMark-1,
		)
	}

	if analyser.PeopleNumber > 0 &&
		(person < 0 || person >= analyser.PeopleNumber) &&
		person != core.AuthorMissing {
		return fmt.Errorf(
			"%w: got %d, configured people count is %d",
			ErrLegacyBurndownAuthorCapacity, person, analyser.PeopleNumber,
		)
	}

	return nil
}

func (analyser *LegacyBurndownAnalysis) validateConfiguredPackedCapacity(
	facts map[string]any,
) error {
	if analyser.PeopleNumber > int(core.AuthorMissing) {
		return fmt.Errorf(
			"%w: got %d people, maximum is %d",
			ErrLegacyBurndownAuthorCapacity, analyser.PeopleNumber, core.AuthorMissing,
		)
	}

	commits, commitsOK := facts[core.ConfigPipelineCommits].([]*object.Commit)
	if !commitsOK || len(commits) == 0 || commits[0] == nil || analyser.tickSize <= 0 {
		return nil
	}

	tick0 := items.FloorTime(commits[0].Committer.When, analyser.tickSize)
	previousTick := 0

	for _, commit := range commits {
		if commit == nil {
			continue
		}

		tick := max(int(commit.Committer.When.Sub(tick0)/analyser.tickSize), previousTick)
		if tick >= linehistory.TreeMergeMark {
			return fmt.Errorf(
				"%w: commit %s requires tick %d, maximum is %d",
				ErrLegacyBurndownTickCapacity,
				commit.Hash.String(),
				tick,
				linehistory.TreeMergeMark-1,
			)
		}

		previousTick = tick
	}

	return nil
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
