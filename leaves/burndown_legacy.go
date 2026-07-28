package leaves

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"

	"github.com/cwbudde/hercules/internal/burndown"
	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
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
		if err := analyser.consumeLegacyTreeChange(change, author, cache, fileDiffs); err != nil {
			return nil, err
		}
	}
	// in case there is a merge analyser.tick equals to TreeMergeMark
	analyser.tick = tick

	return noDependencies(), nil
}

func (analyser *LegacyBurndownAnalysis) consumeLegacyTreeChange(
	change *object.Change,
	author int,
	cache map[plumbing.Hash]*items.CachedBlob,
	fileDiffs map[string]items.FileDiffData,
) error {
	action, _ := change.Action()
	switch action {
	case merkletrie.Insert:
		return analyser.handleInsertion(change, author, cache)
	case merkletrie.Delete:
		return analyser.handleDeletion(change, author, cache)
	case merkletrie.Modify:
		return analyser.handleModification(change, author, cache, fileDiffs)
	default:
		return nil
	}
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
