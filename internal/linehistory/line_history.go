package linehistory

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path"
	"slices"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/cwbudde/hercules/internal/core"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/rbtree"
)

// LineHistoryAnalyser allows to gather per-line history and statistics for a Git repository.
// It is a PipelineItem.
type LineHistoryAnalyser struct {
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

	// ExcludePathPatterns is a list of glob patterns (as per path.Match).
	// Any file whose path matches at least one pattern is excluded from line
	// history tracking.  Use this to skip large generated files that would
	// otherwise balloon memory usage.
	ExcludePathPatterns []string

	// Repository points to the analysed Git repository struct from go-git.
	repository *git.Repository

	fileIdCounter *counterHolder
	// names of unique file ids
	fileNames map[FileId]string
	// names of unique file ids
	fileAbandonedNames         map[FileId]string
	fileAbandonedNamesOfParent map[FileId]string

	// files is the mapping <file path> -> *File.
	files map[string]*File

	// fileAllocator is the allocator for RBTree-s in `files`.
	fileAllocator *rbtree.Allocator
	// hibernatedFileName is the path to the serialized `fileAllocator`.
	hibernatedFileName string

	// tick is the most recent tick index processed.
	tick core.TickNumber
	// previousTick is the tick from the previous sample period -
	// different from TicksSinceStart.previousTick.
	previousTick core.TickNumber
	// mergedAuthor is the author of the merge commit currently waiting for Merge().
	// Merge-commit edits use TreeMergeMark while Consume() runs, then Merge() resolves
	// the remaining marked lines to this author at tick.
	mergedAuthor core.AuthorId
	mergePending bool

	changes []core.LineHistoryChange

	l core.Logger
}

type counterHolder struct {
	atomicCounter atomic.Int32
}

var (
	errFileAlreadyExists               = errors.New("file already exists")
	errPreviousVersionBecameBinary     = errors.New("previous version unexpectedly became binary")
	errSourceIntegrityCheckFailed      = errors.New("source line history integrity check failed")
	errDestinationIntegrityCheckFailed = errors.New("destination line history integrity check failed")
	errDiffInsertAfterInsert           = errors.New("DiffInsert may not appear after DiffInsert")
	errDiffDeleteAfterPending          = errors.New("DiffDelete may not appear after DiffInsert/DiffDelete")
	errUnsupportedDiffOperation        = errors.New("diff operation is not supported")
	errRenamedFileNotFound             = errors.New("file to rename does not exist")
	errInvalidDependencyType           = errors.New("line history dependency has an invalid type")
	// ErrPackedAuthorCapacity indicates that an author cannot be represented by line history.
	ErrPackedAuthorCapacity = errors.New("line history author exceeds packed capacity")
	// ErrPackedTickCapacity indicates that a tick cannot be represented by line history.
	ErrPackedTickCapacity = errors.New("line history tick exceeds packed capacity")
)

func (p *counterHolder) next() FileId {
	return FileId(p.atomicCounter.Add(1))
}

var _ core.FileIdResolver = FileIdResolver{}

type FileIdResolver struct {
	analyser *LineHistoryAnalyser
}

func (v FileIdResolver) NameOf(id FileId) string {
	if v.analyser == nil {
		return ""
	}

	if n, ok := v.analyser.fileNames[id]; ok {
		return n
	}

	return v.abandonedNameOf(id)
}

func (v FileIdResolver) MergedWith(id FileId) (FileId, string, bool) {
	if v.analyser == nil {
		return 0, "", false
	}

	switch f, n := v.analyser.findFileAndName(id); {
	case f != nil:
		return f.Id, n, true
	case n != "":
		return 0, n, false
	}

	return 0, v.abandonedNameOf(id), false
}

func (v FileIdResolver) ForEachFile(callback func(id FileId, name string)) bool {
	if v.analyser == nil {
		return false
	}

	ids := make([]FileId, 0, len(v.analyser.files))
	names := make(map[FileId]string, len(v.analyser.files))

	for name, file := range v.analyser.files {
		ids = append(ids, file.Id)
		names[file.Id] = name
	}

	slices.Sort(ids)

	for _, id := range ids {
		callback(id, names[id])
	}

	return true
}

func (v FileIdResolver) ScanFile(id FileId, callback func(line int, tick core.TickNumber, author core.AuthorId)) bool {
	if v.analyser == nil {
		return false
	}

	file, _ := v.analyser.findFileAndName(id)
	if file == nil {
		return false
	}

	file.ForEach(func(line, value int) {
		author, tick := unpackPersonWithTick(value)
		callback(line, tick, author)
	})

	return true
}

const (
	DependencyLineHistory = "line_history"

	// ConfigLinesHibernationThreshold sets the hibernation threshold for the underlying
	// RBTree allocator. It is useful to trade CPU time for reduced peak memory consumption
	// if there are many branches.
	ConfigLinesHibernationThreshold = "LineHistory.HibernationThreshold"
	// ConfigLinesHibernationToDisk sets whether the hibernated RBTree allocator must be saved
	// on disk rather than kept in memory.
	ConfigLinesHibernationToDisk = "LineHistory.HibernationOnDisk"
	// ConfigLinesHibernationDirectory sets the name of the temporary directory to use for
	// saving hibernated RBTree allocators.
	ConfigLinesHibernationDirectory = "LineHistory.HibernationDirectory"
	// ConfigLinesDebug enables some extra debug assertions.
	ConfigLinesDebug = "LineHistory.Debug"
	// ConfigLinesExcludePaths is the config key for ExcludePathPatterns.
	ConfigLinesExcludePaths = "LineHistory.ExcludePaths"
)

func (analyser *LineHistoryAnalyser) Name() string {
	return "LineHistory"
}

func (analyser *LineHistoryAnalyser) Provides() []string {
	return []string{DependencyLineHistory}
}

func (analyser *LineHistoryAnalyser) Requires() []string {
	return []string{
		items.DependencyFileDiff, items.DependencyTreeChanges, items.DependencyBlobCache,
		items.DependencyTick, identity.DependencyAuthor,
	}
}

func (*LineHistoryAnalyser) Features() []string {
	return []string{core.FeatureGitCommits}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (analyser *LineHistoryAnalyser) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name: ConfigLinesHibernationThreshold,
			Description: "The minimum size for the allocated memory in each branch to be compressed." +
				"0 disables this optimization. Lower values trade CPU time more. Sane examples: Nx1000.",
			Flag:    "lines-hibernation-threshold",
			Type:    core.IntConfigurationOption,
			Default: 0,
		}, {
			Name:        ConfigLinesHibernationToDisk,
			Description: "Save hibernated RBTree allocators to disk rather than keep it in memory.",
			Flag:        "lines-hibernation-disk",
			Type:        core.BoolConfigurationOption,
			Default:     false,
		}, {
			Name:        ConfigLinesHibernationDirectory,
			Description: "Temporary directory where to save the hibernated RBTree allocators.",
			Flag:        "lines-hibernation-dir",
			Type:        core.PathConfigurationOption,
			Default:     "",
		}, {
			Name:        ConfigLinesDebug,
			Description: "Validate the trees at each step.",
			Flag:        "lines-debug",
			Type:        core.BoolConfigurationOption,
			Default:     false,
		}, {
			Name: ConfigLinesExcludePaths,
			Description: "Glob patterns of file paths to exclude from line history tracking (e.g. generated files). " +
				"Repeat the flag or separate with commas.",
			Flag:    "lines-exclude-paths",
			Type:    core.StringsConfigurationOption,
			Default: []string{},
		},
	}

	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (analyser *LineHistoryAnalyser) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		analyser.l = l
	} else {
		analyser.l = core.NewLogger()
	}

	if val, exists := facts[ConfigLinesHibernationThreshold].(int); exists {
		analyser.HibernationThreshold = val
	}

	if val, exists := facts[ConfigLinesHibernationToDisk].(bool); exists {
		analyser.HibernationToDisk = val
	}

	if val, exists := facts[ConfigLinesHibernationDirectory].(string); exists {
		analyser.HibernationDirectory = val
	}

	if val, exists := facts[ConfigLinesDebug].(bool); exists {
		analyser.Debug = val
	}

	if val, exists := facts[ConfigLinesExcludePaths].([]string); exists {
		analyser.ExcludePathPatterns = val
	}

	err := validateConfiguredPackedCapacity(facts)
	if err != nil {
		return err
	}

	var resolver core.FileIdResolver = FileIdResolver{analyser}
	facts[core.FactLineHistoryResolver] = resolver

	return nil
}

func (analyser *LineHistoryAnalyser) ConfigureUpstream(_ map[string]any) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (analyser *LineHistoryAnalyser) Initialize(repository *git.Repository) error {
	analyser.Dispose()

	if analyser.l == nil {
		analyser.l = core.NewLogger()
	}

	analyser.repository = repository
	analyser.fileNames = map[FileId]string{}
	analyser.fileIdCounter = &counterHolder{}
	analyser.files = map[string]*File{}
	analyser.fileAllocator = rbtree.NewAllocator()
	analyser.fileAllocator.HibernationThreshold = analyser.HibernationThreshold

	analyser.tick = 0
	analyser.previousTick = 0
	analyser.mergedAuthor = core.AuthorMissing
	analyser.mergePending = false

	return nil
}

func (analyser *LineHistoryAnalyser) Consume(deps map[string]any) (map[string]any, error) {
	if analyser.fileAllocator.Size() == 0 && len(analyser.files) > 0 {
		panic("LineHistoryAnalyser.Consume() was called on a hibernated instance")
	}

	author, tick, cache, treeDiffs, fileDiffs, err := lineHistoryDependencies(deps)
	if err != nil {
		return nil, err
	}

	err = validatePackedOwnership(author, tick)
	if err != nil {
		return nil, err
	}

	analyser.tick = tick
	analyser.onNewTick()
	analyser.changes = make([]core.LineHistoryChange, 0, len(treeDiffs)*4)

	isMerge, _ := deps[core.DependencyIsMerge].(bool)
	if isMerge {
		analyser.mergedAuthor = author
		analyser.mergePending = true
		analyser.tick = TreeMergeMark
	}

	err = analyser.consumeTreeDiffs(treeDiffs, author, cache, fileDiffs)
	analyser.tick = tick
	if err != nil {
		return nil, err
	}

	result := map[string]any{DependencyLineHistory: core.LineHistoryChanges{
		Changes:  analyser.changes,
		Resolver: FileIdResolver{analyser},
	}}

	analyser.changes = nil

	return result, nil
}

func lineHistoryDependencies(deps map[string]any) (
	core.AuthorId,
	core.TickNumber,
	map[plumbing.Hash]*items.CachedBlob,
	object.Changes,
	map[string]items.FileDiffData,
	error,
) {
	authorID, err := lineHistoryDependency[int](deps, identity.DependencyAuthor)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	tick, err := lineHistoryDependency[int](deps, items.DependencyTick)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	cache, err := lineHistoryDependency[map[plumbing.Hash]*items.CachedBlob](deps, items.DependencyBlobCache)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	treeDiffs, err := lineHistoryDependency[object.Changes](deps, items.DependencyTreeChanges)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	fileDiffs, err := lineHistoryDependency[map[string]items.FileDiffData](deps, items.DependencyFileDiff)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	author, err := intToAuthorID(authorID)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	tickNumber, err := intToTickNumber(tick)
	if err != nil {
		return 0, 0, nil, nil, nil, err
	}

	return author, tickNumber, cache, treeDiffs, fileDiffs, nil
}

func lineHistoryDependency[T any](deps map[string]any, name string) (T, error) {
	value, ok := deps[name].(T)
	if !ok {
		var zero T

		return zero, fmt.Errorf("%w: %q", errInvalidDependencyType, name)
	}

	return value, nil
}

func intToAuthorID(value int) (core.AuthorId, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("%w: author ID %d is out of range", errInvalidDependencyType, value)
	}

	return core.AuthorId(value), nil
}

func intToTickNumber(value int) (core.TickNumber, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("%w: tick number %d is out of range", errInvalidDependencyType, value)
	}

	return core.TickNumber(value), nil
}

// Fork clones this item. Everything is copied by reference except the files
// which are copied by value.
func (analyser *LineHistoryAnalyser) Fork(n int) []core.PipelineItem {
	result := make([]core.PipelineItem, n)

	for cloneIndex := range result {
		clone := *analyser

		clone.files = make(map[string]*File, len(analyser.files))
		clone.fileNames = make(map[FileId]string, len(analyser.fileNames))
		clone.fileAbandonedNames = nil
		clone.fileAbandonedNamesOfParent = nil

		clone.fileAllocator = clone.fileAllocator.Clone()
		for key, file := range analyser.files {
			clone.files[key] = file.CloneShallowWithUpdaters(clone.fileAllocator, clone.updateChangeList)
			clone.fileNames[file.Id] = key
		}

		clone.changes = nil

		result[cloneIndex] = &clone
	}

	for id, name := range analyser.fileNames {
		if file := analyser.files[name]; file == nil {
			analyser.addAbandonedName(id, name)
		} else if file.Id != id {
			panic("Inconsistent file name mapping")
		}
	}

	if abandonedNames := analyser.inheritAbandonedNames(); len(abandonedNames) > 0 {
		for _, item := range result {
			mustLineHistoryAnalyser(item).fileAbandonedNamesOfParent = abandonedNames
		}
	}

	return result
}

// Merge resolves the merge commit against the other parents and synchronizes every branch to the
// resulting tree. The branch which consumed the merge commit is authoritative for path presence;
// equal-length files from the other parents supply ownership for lines left with TreeMergeMark.
func (analyser *LineHistoryAnalyser) Merge(items []core.PipelineItem) {
	branches := make([]*LineHistoryAnalyser, 1, len(items)+1)

	branches[0] = analyser
	for _, item := range items {
		branches = append(branches, mustLineHistoryAnalyser(item))
	}

	if analyser.mergePending {
		for name, file := range analyser.files {
			others := matchingMergeFiles(branches[1:], name, file)
			file.Merge(packPersonWithTick(analyser.mergedAuthor, analyser.tick), others...)
		}
	}

	for _, branch := range branches[1:] {
		rememberLineHistoryBranchNames(analyser, branch)
		synchronizeLineHistoryBranch(analyser, branch)
	}

	analyser.mergePending = false
	analyser.mergedAuthor = core.AuthorMissing
	analyser.changes = nil
	analyser.onNewTick()
}

func matchingMergeFiles(
	branches []*LineHistoryAnalyser, name string, target *File,
) []*File {
	files := make([]*File, 0, len(branches))
	for _, branch := range branches {
		file := branch.files[name]
		if file != nil && file.Id == target.Id && file.Len() == target.Len() {
			files = append(files, file)
		}
	}

	return files
}

func rememberLineHistoryBranchNames(analyser, branch *LineHistoryAnalyser) {
	for name, file := range branch.files {
		if _, ok := analyser.fileNames[file.Id]; !ok {
			analyser.mergeAbandonedName(file.Id, name)
		}
	}

	for id, name := range branch.fileNames {
		if _, ok := analyser.fileNames[id]; !ok {
			analyser.mergeAbandonedName(id, name)
		}
	}
}

func synchronizeLineHistoryBranch(source, target *LineHistoryAnalyser) {
	for _, file := range target.files {
		file.Delete()
	}

	target.files = make(map[string]*File, len(source.files))
	target.fileNames = make(map[FileId]string, len(source.fileNames))

	for name, file := range source.files {
		target.files[name] = file.CloneDeepWithUpdaters(target.fileAllocator, target.updateChangeList)
		target.fileNames[file.Id] = name
	}

	target.fileAbandonedNames = maps.Clone(source.fileAbandonedNames)
	target.fileAbandonedNamesOfParent = source.fileAbandonedNamesOfParent
	target.tick = source.tick
	target.previousTick = source.previousTick
	target.mergedAuthor = core.AuthorMissing
	target.mergePending = false
	target.changes = nil
}

func mustLineHistoryAnalyser(item core.PipelineItem) *LineHistoryAnalyser {
	analyser, ok := item.(*LineHistoryAnalyser)
	if !ok {
		panic("line history branch has an unexpected type")
	}

	return analyser
}

// Hibernate compresses the bound RBTree memory with the files.
func (analyser *LineHistoryAnalyser) Hibernate() error {
	analyser.fileAllocator.Hibernate()

	if analyser.HibernationToDisk {
		file, err := os.CreateTemp(analyser.HibernationDirectory, "*-hercules.bin")
		if err != nil {
			return fmt.Errorf("create line history hibernation file: %w", err)
		}

		analyser.hibernatedFileName = file.Name()

		err = file.Close()
		if err != nil {
			removeErr := removeLineHistoryHibernationFile(analyser)

			return errors.Join(
				fmt.Errorf("close line history hibernation file %q: %w", file.Name(), err),
				removeErr,
			)
		}

		err = analyser.fileAllocator.Serialize(analyser.hibernatedFileName)
		if err != nil {
			hibernatedFileName := analyser.hibernatedFileName
			removeErr := removeLineHistoryHibernationFile(analyser)

			return errors.Join(
				fmt.Errorf("serialize line history allocator to %q: %w", hibernatedFileName, err),
				removeErr,
			)
		}
	}

	return nil
}

// Boot decompresses the bound RBTree memory with the files.
func (analyser *LineHistoryAnalyser) Boot() error {
	if analyser.hibernatedFileName != "" {
		hibernatedFileName := analyser.hibernatedFileName

		err := analyser.fileAllocator.Deserialize(hibernatedFileName)
		if err != nil {
			removeErr := removeLineHistoryHibernationFile(analyser)

			return errors.Join(
				fmt.Errorf("deserialize line history allocator from %q: %w", hibernatedFileName, err),
				removeErr,
			)
		}

		err = removeLineHistoryHibernationFile(analyser)
		if err != nil {
			return fmt.Errorf("remove line history hibernation file %q: %w", hibernatedFileName, err)
		}
	}

	analyser.fileAllocator.Boot()

	return nil
}

// Dispose removes temporary hibernation state left by a completed or failed run.
func (analyser *LineHistoryAnalyser) Dispose() {
	_ = removeLineHistoryHibernationFile(analyser)
}

func removeLineHistoryHibernationFile(analyser *LineHistoryAnalyser) error {
	hibernatedFileName := analyser.hibernatedFileName
	if hibernatedFileName == "" {
		return nil
	}

	err := os.Remove(hibernatedFileName)

	err = ignoreNotExist(err)
	if err != nil {
		return fmt.Errorf("remove line history hibernation file %q: %w", hibernatedFileName, err)
	}

	analyser.hibernatedFileName = ""

	return nil
}

func ignoreNotExist(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}

func (analyser *LineHistoryAnalyser) consumeTreeDiffs(
	treeDiffs object.Changes,
	author core.AuthorId,
	cache map[plumbing.Hash]*items.CachedBlob,
	fileDiffs map[string]items.FileDiffData,
) error {
	for _, change := range treeDiffs {
		if analyser.isExcluded(change.From.Name, change.To.Name) {
			continue
		}

		err := analyser.consumeTreeDiff(change, author, cache, fileDiffs)
		if err != nil {
			return err
		}
	}

	return nil
}

func (analyser *LineHistoryAnalyser) consumeTreeDiff(
	change *object.Change,
	author core.AuthorId,
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
	}

	return nil
}

func (v FileIdResolver) abandonedNameOf(id FileId) string {
	if n, ok := v.analyser.fileAbandonedNames[id]; ok {
		return n
	}

	return v.analyser.fileAbandonedNamesOfParent[id]
}

// isExcluded reports whether a file change should be skipped because either
// the source or destination path matches one of ExcludePathPatterns.
func (analyser *LineHistoryAnalyser) isExcluded(fromName, toName string) bool {
	for _, pattern := range analyser.ExcludePathPatterns {
		if fromName != "" {
			if ok, _ := path.Match(pattern, fromName); ok {
				return true
			}
		}

		if toName != "" {
			if ok, _ := path.Match(pattern, toName); ok {
				return true
			}
		}
	}

	return false
}

func (analyser *LineHistoryAnalyser) findFileAndName(id FileId) (*File, string) {
	if n, ok := analyser.fileNames[id]; ok {
		if f := analyser.files[n]; f != nil {
			return f, n
		}

		analyser.addAbandonedName(id, n)
	}

	return nil, ""
}

func (analyser *LineHistoryAnalyser) addAbandonedName(id FileId, name string) {
	if analyser.fileAbandonedNames == nil {
		analyser.fileAbandonedNames = map[FileId]string{}
	}

	analyser.fileAbandonedNames[id] = name
	delete(analyser.fileNames, id)
}

func (analyser *LineHistoryAnalyser) mergeAbandonedName(id FileId, name string) {
	if analyser.fileAbandonedNames == nil {
		analyser.fileAbandonedNames = map[FileId]string{}
	} else if _, ok := analyser.fileAbandonedNames[id]; ok {
		return
	}

	analyser.fileAbandonedNames[id] = name
}

func (analyser *LineHistoryAnalyser) inheritAbandonedNames() map[FileId]string {
	if len(analyser.fileAbandonedNamesOfParent) == 0 {
		return analyser.fileAbandonedNames
	}

	if len(analyser.fileAbandonedNames) == 0 {
		return analyser.fileAbandonedNamesOfParent
	}

	m := make(map[FileId]string, len(analyser.fileAbandonedNames)+len(analyser.fileAbandonedNamesOfParent))
	maps.Copy(m, analyser.fileAbandonedNamesOfParent)

	maps.Copy(m, analyser.fileAbandonedNames)

	return m
}

// We do a hack and store the tick in the first 14 bits and the author index in the last 18.
// Strictly speaking, int can be 64-bit and then the author index occupies 32+18 bits.
// This hack is needed to simplify the values storage inside File-s. We can compare
// different values together and they are compared as ticks for the same author.
func packPersonWithTick(author core.AuthorId, tick core.TickNumber) int {
	result := int(tick) & TreeMergeMark
	result |= int(author) << TreeMaxBinPower

	// This effectively means max (16383 - 1) ticks (>44 years) and (262143 - 3) devs.
	// One tick less because TreeMergeMark = ((1 << 14) - 1) is a special tick.
	// Three devs less because:
	// - math.MaxUint32 is the special rbtree value with tick == TreeMergeMark (-1)
	// - core.AuthorMissing (-2)
	// - authorSelf (-3)
	return result
}

func packChangePersonWithTick(author core.AuthorId, tick core.TickNumber) int {
	if tick == TreeMergeMark {
		// The author bits are irrelevant until Merge() resolves the marker. Keeping
		// them zero also avoids colliding with the reserved TreeEnd value when the
		// merge author is AuthorMissing.
		return TreeMergeMark
	}

	return packPersonWithTick(author, tick)
}

func validatePackedOwnership(author core.AuthorId, tick core.TickNumber) error {
	if author < 0 || author > core.AuthorMissing {
		return fmt.Errorf(
			"%w: got %d, allowed range is [0, %d]",
			ErrPackedAuthorCapacity, author, core.AuthorMissing,
		)
	}

	if tick < 0 || tick >= TreeMergeMark {
		return fmt.Errorf(
			"%w: got %d, allowed range is [0, %d]",
			ErrPackedTickCapacity, tick, TreeMergeMark-1,
		)
	}

	return nil
}

func validateConfiguredPackedCapacity(facts map[string]any) error {
	err := validateConfiguredAuthorCapacity(facts)
	if err != nil {
		return err
	}

	return validateConfiguredTickCapacity(facts)
}

func validateConfiguredAuthorCapacity(facts map[string]any) error {
	if resolver, ok := facts[core.FactIdentityResolver].(core.IdentityResolver); ok &&
		resolver.MaxCount() > int(core.AuthorMissing) {
		return fmt.Errorf(
			"%w: got %d authors, maximum is %d",
			ErrPackedAuthorCapacity, resolver.MaxCount(), core.AuthorMissing,
		)
	}

	return nil
}

func validateConfiguredTickCapacity(facts map[string]any) error {
	commits, commitsOK := facts[core.ConfigPipelineCommits].([]*object.Commit)
	tickSize, tickSizeOK := facts[items.FactTickSize].(time.Duration)

	if !commitsOK || len(commits) == 0 || !tickSizeOK || tickSize <= 0 {
		return nil
	}

	if commits[0] == nil {
		return nil
	}

	tick0 := items.FloorTime(commits[0].Committer.When, tickSize)
	previousTick := 0

	for _, commit := range commits {
		if commit == nil {
			continue
		}

		tick := max(int(commit.Committer.When.Sub(tick0)/tickSize), previousTick)
		if tick >= TreeMergeMark {
			return fmt.Errorf(
				"%w: commit %s requires tick %d, maximum is %d",
				ErrPackedTickCapacity, commit.Hash.String(), tick, TreeMergeMark-1,
			)
		}

		previousTick = tick
	}

	return nil
}

func unpackPersonWithTick(value int) (core.AuthorId, core.TickNumber) {
	author, err := intToAuthorID(value >> TreeMaxBinPower)
	if err != nil {
		panic(err)
	}

	tick, err := intToTickNumber(value & TreeMergeMark)
	if err != nil {
		panic(err)
	}

	return author, tick
}

func (analyser *LineHistoryAnalyser) onNewTick() {
	if analyser.tick > analyser.previousTick {
		analyser.previousTick = analyser.tick
	}
}

func (analyser *LineHistoryAnalyser) updateChangeList(file *File, currentTime, previousTime, delta int) {
	prevAuthor, prevTick := unpackPersonWithTick(previousTime)

	newAuthor, curTick := unpackPersonWithTick(currentTime)
	if delta > 0 && newAuthor != prevAuthor {
		analyser.l.Errorf("insertion must have the same author (%d, %d)", prevAuthor, newAuthor)
		return
	}

	change := core.LineHistoryChange{
		FileId:     file.Id,
		CurrTick:   curTick,
		CurrAuthor: newAuthor,
		PrevTick:   prevTick,
		PrevAuthor: prevAuthor,
	}

	if len(analyser.changes) > 0 {
		last := &analyser.changes[len(analyser.changes)-1]
		if (delta <= 0 && last.Delta < 0) || (delta >= 0 && last.Delta > 0) {
			change.Delta = last.Delta

			if change == *last {
				last.Delta += delta
				return
			}
		}
	}

	change.Delta = delta
	analyser.changes = append(analyser.changes, change)
}

func (analyser *LineHistoryAnalyser) newFile(
	name string, author core.AuthorId, tick core.TickNumber, size int,
) *File {
	analyser.forgetFileName(name)

	fileId, found := analyser.abandonedFileID(name)
	if tick != TreeMergeMark || !found {
		fileId = analyser.fileIdCounter.next()
	}

	delete(analyser.fileAbandonedNames, fileId)
	analyser.fileNames[fileId] = name
	file := NewFile(
		fileId,
		packChangePersonWithTick(author, tick),
		size,
		analyser.fileAllocator,
		analyser.updateChangeList,
	)
	analyser.files[name] = file

	return file
}

func (analyser *LineHistoryAnalyser) abandonedFileID(name string) (FileId, bool) {
	var selected FileId
	found := false

	for id, abandonedName := range analyser.fileAbandonedNamesOfParent {
		if abandonedName == name && (!found || id > selected) {
			selected, found = id, true
		}
	}

	for id, abandonedName := range analyser.fileAbandonedNames {
		if abandonedName == name && (!found || id > selected) {
			selected, found = id, true
		}
	}

	return selected, found
}

func (analyser *LineHistoryAnalyser) forgetFileName(name string) {
	if file := analyser.files[name]; file != nil {
		analyser.addAbandonedName(file.Id, name)
		delete(analyser.files, name)
	}
}

func (analyser *LineHistoryAnalyser) handleInsertion(
	change *object.Change, author core.AuthorId, cache map[plumbing.Hash]*items.CachedBlob,
) error {
	blob := cache[change.To.TreeEntry.Hash]

	name := change.To.Name
	analyser.forgetFileName(name)

	lines, err := blob.CountLines()
	if errors.Is(err, items.ErrBinary) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("count lines in inserted file %q: %w", name, err)
	}

	file := analyser.files[name]
	if file != nil {
		return fmt.Errorf("%w: %s", errFileAlreadyExists, name)
	}

	analyser.newFile(name, author, analyser.tick, lines)

	return nil
}

func (analyser *LineHistoryAnalyser) handleDeletion(
	change *object.Change, author core.AuthorId, cache map[plumbing.Hash]*items.CachedBlob,
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
			return fmt.Errorf("%w: %s", errPreviousVersionBecameBinary, name)
		}

		lines = 0
		err = nil
	}

	if exists && err != nil {
		return fmt.Errorf("count lines in deleted file %q: %w", name, err)
	}

	if !exists {
		return nil
	}

	file.Update(packChangePersonWithTick(author, analyser.tick), 0, 0, lines)
	file.Delete()

	if analyser.tick != TreeMergeMark {
		analyser.changes = append(
			analyser.changes,
			core.NewLineHistoryDeletion(file.Id, author, analyser.tick),
		)
	}
	analyser.forgetFileName(name)

	return nil
}

func (analyser *LineHistoryAnalyser) handleModification(
	change *object.Change, author core.AuthorId, cache map[plumbing.Hash]*items.CachedBlob,
	diffs map[string]items.FileDiffData,
) error {
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

	fromBinary, toBinary, err := classifyLineCountErrors(change, errFrom, errTo)
	if err != nil {
		return err
	}

	handled, err := analyser.handleBinaryModification(
		change, author, cache, fromBinary, toBinary,
	)
	if handled {
		return err
	}

	thisDiffs := diffs[change.To.Name]
	if file.Len() != thisDiffs.OldLinesOfCode {
		analyser.l.Infof("====TREE====\n%s", file.Dump())

		return fmt.Errorf("%w for %s: %d != %d %s -> %s",
			errSourceIntegrityCheckFailed, change.To.Name, thisDiffs.OldLinesOfCode, file.Len(),
			change.From.TreeEntry.Hash.String(), change.To.TreeEntry.Hash.String())
	}

	state := lineHistoryDiffState{analyser: analyser, file: file, change: change, author: author}
	for _, edit := range thisDiffs.Diffs {
		err := state.process(edit)
		if err != nil {
			return err
		}
	}

	state.flush()

	if file.Len() != thisDiffs.NewLinesOfCode {
		return fmt.Errorf("%w for %s: %d != %d %s -> %s",
			errDestinationIntegrityCheckFailed, change.To.Name, thisDiffs.NewLinesOfCode, file.Len(),
			change.From.TreeEntry.Hash.String(), change.To.TreeEntry.Hash.String())
	}

	return nil
}

func (analyser *LineHistoryAnalyser) handleBinaryModification(
	change *object.Change, author core.AuthorId,
	cache map[plumbing.Hash]*items.CachedBlob, fromBinary, toBinary bool,
) (bool, error) {
	if fromBinary == toBinary {
		return fromBinary, nil
	}

	if fromBinary {
		// Binary contents are opaque. Reconstruct the visible text in the existing
		// zero-line placeholder so the path keeps its file identity.
		lines, err := cache[change.To.TreeEntry.Hash].CountLines()
		if err != nil {
			return true, fmt.Errorf("count lines in reconstructed file %q: %w", change.To.Name, err)
		}

		file := analyser.files[change.To.Name]
		if file == nil {
			return true, analyser.handleInsertion(change, author, cache)
		}

		file.Update(packChangePersonWithTick(author, analyser.tick), 0, lines, file.Len())

		return true, nil
	}

	// Keep a zero-line placeholder instead of routing this transition through
	// ordinary deletion. Ownership deltas remove the visible text, but no file
	// deletion sentinel is emitted and the ID remains live.
	file := analyser.files[change.To.Name]
	if file != nil {
		file.Update(packChangePersonWithTick(author, analyser.tick), 0, 0, file.Len())
	}

	return true, nil
}

func classifyLineCountErrors(
	change *object.Change, errFrom, errTo error,
) (bool, bool, error) {
	fromBinary := errors.Is(errFrom, items.ErrBinary)
	toBinary := errors.Is(errTo, items.ErrBinary)

	if errFrom != nil && !fromBinary {
		return false, false, fmt.Errorf(
			"count lines in previous version of %q: %w", change.From.Name, errFrom,
		)
	}

	if errTo != nil && !toBinary {
		return false, false, fmt.Errorf(
			"count lines in new version of %q: %w", change.To.Name, errTo,
		)
	}

	return fromBinary, toBinary, nil
}

type lineHistoryDiffState struct {
	analyser *LineHistoryAnalyser
	file     *File
	change   *object.Change
	author   core.AuthorId
	position int
	pending  diffmatchpatch.Diff
}

func (state *lineHistoryDiffState) process(edit diffmatchpatch.Diff) error {
	dumpBefore := ""
	if state.analyser.Debug {
		dumpBefore = state.file.Dump()
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
			state.debugError(length, dumpBefore)
			return errDiffInsertAfterInsert
		}

		state.file.Update(
			packChangePersonWithTick(state.author, state.analyser.tick), state.position,
			length, utf8.RuneCountInString(state.pending.Text),
		)

		if state.analyser.Debug {
			state.file.Validate()
		}

		state.position += length
		state.pending.Text = ""
	case diffmatchpatch.DiffDelete:
		if state.pending.Text != "" {
			state.debugError(length, dumpBefore)
			return errDiffDeleteAfterPending
		}

		state.pending = edit
	default:
		state.debugError(length, dumpBefore)
		return fmt.Errorf("%w: %d", errUnsupportedDiffOperation, edit.Type)
	}

	return nil
}

func (state *lineHistoryDiffState) flush() {
	if state.pending.Text == "" {
		return
	}

	length := utf8.RuneCountInString(state.pending.Text)
	if state.pending.Type == diffmatchpatch.DiffInsert {
		state.file.Update(
			packChangePersonWithTick(state.author, state.analyser.tick), state.position, length, 0,
		)
		state.position += length
	} else {
		state.file.Update(
			packChangePersonWithTick(state.author, state.analyser.tick), state.position, 0, length,
		)
	}

	if state.analyser.Debug {
		state.file.Validate()
	}

	state.pending.Text = ""
}

func (state *lineHistoryDiffState) debugError(length int, dumpBefore string) {
	state.analyser.l.Errorf("%s: internal diff error\n", state.change.To.Name)
	state.analyser.l.Errorf(
		"Update(%d, %d, %d (0), %d (0))\n", state.analyser.tick, state.position,
		length, utf8.RuneCountInString(state.pending.Text),
	)

	if dumpBefore != "" {
		state.analyser.l.Errorf("====TREE BEFORE====\n%s====END====\n", dumpBefore)
	}

	state.analyser.l.Errorf("====TREE AFTER====\n%s====END====\n", state.file.Dump())
}

func (analyser *LineHistoryAnalyser) handleRename(sourceName, targetName string) error {
	if sourceName == targetName {
		return nil
	}

	file, exists := analyser.files[sourceName]
	if !exists {
		return fmt.Errorf("%w: %s > %s", errRenamedFileNotFound, sourceName, targetName)
	}

	delete(analyser.files, sourceName)
	analyser.forgetFileName(targetName)
	analyser.fileNames[file.Id] = targetName
	analyser.files[targetName] = file

	return nil
}

var _ = core.RegisterPipelineItem(&LineHistoryAnalyser{})
