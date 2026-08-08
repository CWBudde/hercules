package core

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"maps"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/pkg/errors"

	"github.com/cwbudde/hercules/internal/pb"
)

// ConfigurationOptionType represents the possible types of a ConfigurationOption's value.
type ConfigurationOptionType int

const (
	// BoolConfigurationOption reflects the boolean value type.
	BoolConfigurationOption ConfigurationOptionType = iota
	// IntConfigurationOption reflects the integer value type.
	IntConfigurationOption
	// StringConfigurationOption reflects the string value type.
	StringConfigurationOption
	// FloatConfigurationOption reflects a floating point value type.
	FloatConfigurationOption
	// StringsConfigurationOption reflects the array of strings value type.
	StringsConfigurationOption
	// PathConfigurationOption reflects the file system path value type.
	PathConfigurationOption
)

const (
	FeatureGitCommits = "git.commits"
	FeatureGitStub    = "git.stub"
)

const configurationStringType = "string"

var (
	// ErrNoCommits indicates that an explicit commit input or execution plan is empty.
	ErrNoCommits = errors.New("no commits to analyze")
	// ErrNoReferences indicates that a repository does not contain a usable commit reference.
	ErrNoReferences = errors.New("repository has no commit references")
	// ErrInvalidCommit indicates that an explicit commit input contains a nil or zero-hash commit.
	ErrInvalidCommit = errors.New("invalid commit")
	// ErrDuplicateCommits indicates that an explicit commit input repeats a commit hash.
	ErrDuplicateCommits = errors.New("duplicate commits")
	// ErrDisconnectedCommits indicates that commit input contains multiple disconnected components.
	ErrDisconnectedCommits = errors.New("disconnected commit components")

	errGraphEdgeNotFound           = errors.New("toposort: edge not found")
	errNegativeHibernationDistance = errors.New("--hibernation-distance cannot be negative")
	errPlanCommitCountMismatch     = errors.New("execution plan commit count mismatch")
	// The runtime GC percentage is process-global, so hibernating runs must restore it serially.
	//nolint:gochecknoglobals
	gcPercentMu sync.Mutex
)

// String() returns an empty string for the boolean type, "int" for integers and "string" for
// strings. It is used in the command line interface to show the argument's type.
func (opt ConfigurationOptionType) String() string {
	switch opt {
	case BoolConfigurationOption:
		return ""
	case IntConfigurationOption:
		return "int"
	case StringConfigurationOption:
		return configurationStringType
	case FloatConfigurationOption:
		return "float"
	case StringsConfigurationOption:
		return configurationStringType
	case PathConfigurationOption:
		return "path"
	}

	log.Panicf("Invalid ConfigurationOptionType value %d", opt)

	return ""
}

// ConfigurationOption allows for the unified, retrospective way to setup PipelineItem-s.
type ConfigurationOption struct {
	// Name identifies the configuration option in facts.
	Name string
	// Description represents the help text about the configuration option.
	Description string
	// Flag corresponds to the CLI token with "--" prepended.
	Flag string
	// Type specifies the kind of the configuration option's value.
	Type ConfigurationOptionType
	// Default is the initial value of the configuration option.
	Default any
	// Shared allows to share same option by multiple items. All cases must have same type, name and default.
	Shared bool
}

// FormatDefault converts the default value of ConfigurationOption to string.
// Used in the command line interface to show the argument's default value.
func (opt ConfigurationOption) FormatDefault() string {
	if opt.Type == StringsConfigurationOption {
		return fmt.Sprintf("\"%s\"", strings.Join(configurationDefault[[]string](opt), ","))
	}

	if opt.Type != StringConfigurationOption {
		return fmt.Sprint(opt.Default)
	}

	return fmt.Sprintf("\"%s\"", opt.Default)
}

func configurationDefault[T any](option ConfigurationOption) T {
	value, ok := option.Default.(T)
	if !ok {
		panic("configuration option has an invalid default type")
	}

	return value
}

// PipelineItem is the interface for all the units in the Git commits analysis pipeline.
type PipelineItem interface {
	// Name returns the name of the analysis.
	Name() string
	// Provides returns the list of keys of reusable calculated entities.
	// Other items may depend on them.
	Provides() []string
	// Requires returns the list of keys of needed entities which must be supplied in Consume().
	Requires() []string
	// ListConfigurationOptions returns the list of available options which can be consumed by Configure().
	ListConfigurationOptions() []ConfigurationOption
	// Configure replaces immutable configuration from facts in downstream direction.
	// It must not rely on state produced by an earlier run.
	Configure(facts map[string]any) error
	// ConfigureUpstream applies configuration facts in upstream direction after downstream
	// configuration has finished. It must not allocate branch-local run state.
	ConfigureUpstream(facts map[string]any) error
	// Initialize idempotently discards state from the previous run and prepares empty run state.
	// Configuration set by Configure survives. Consume requires a successful Initialize.
	Initialize(repository *git.Repository) error
	// Consume processes the next commit.
	// deps contains the required entities which match Depends(). Besides, it always includes
	// DependencyCommit and DependencyIndex.
	// Returns the calculated entities which match Provides().
	Consume(deps map[string]any) (map[string]any, error)
	// Fork clones the item the requested number of times. The data links between the clones
	// are up to the implementation. Needed to handle Git branches. See also Merge().
	// Returns a slice with `n` branch instances. It does not include the original item.
	// Immutable configuration must remain unchanged.
	Fork(n int) []PipelineItem
	// Merge combines several branches together. Each is supposed to have been created with Fork().
	// The result is stored in the called item, thus this function returns nothing.
	// Merge must update all the branches, not only self. When several branches merge, some of
	// them may continue to live, hence this requirement. Configuration remains unchanged.
	Merge(branches []PipelineItem)
}

// FeaturedPipelineItem enables switching the automatic insertion of pipeline items on or off.
type FeaturedPipelineItem interface {
	PipelineItem
	// Features returns the list of names which enable this item to be automatically inserted
	// in Pipeline.DeployItem().
	Features() []string
}

// DisposablePipelineItem enables resources cleanup after finishing running the pipeline.
type DisposablePipelineItem interface {
	PipelineItem
	// Dispose idempotently frees any previously allocated unmanaged resources without changing
	// configuration. No Consume calls are valid afterwards until another Initialize.
	// This method is invoked once for every unique instance created for a run, including branch
	// clones, on both successful and failed runs.
	Dispose()
}

// LeafPipelineItem corresponds to the top level pipeline items which produce the end results.
type LeafPipelineItem interface {
	PipelineItem
	// Flag returns the cmdline switch to run the analysis. Should be dash-lower-case
	// without the leading dashes.
	Flag() string
	// Description returns the text which explains what the analysis is doing.
	// Should start with a capital letter and end with a dot.
	Description() string
	// Finalize returns the result of the analysis.
	Finalize() any
	// Serialize encodes the object returned by Finalize() to YAML or Protocol Buffers.
	Serialize(result any, binary bool, writer io.Writer) error
}

// ResultMergeablePipelineItem specifies the methods to combine several analysis results together.
type ResultMergeablePipelineItem interface {
	LeafPipelineItem
	// Deserialize loads the result from Protocol Buffers blob.
	Deserialize(message []byte) (any, error)
	// MergeResults joins two results together. Common-s are specified as the global state.
	MergeResults(r1, r2 any, c1, c2 *CommonAnalysisResult) any
}

// RepositoryQualifiablePipelineItem is implemented by analyses whose results are keyed by a path -
// a file path or a directory prefix - which only identifies something within a single repository.
//
// Merging such results across repositories without a qualifier fuses unrelated paths: every
// repository's README.md becomes one file. QualifyPaths is therefore applied to each result before
// it enters a merge, so that the keys carry the repository they were observed in.
type RepositoryQualifiablePipelineItem interface {
	ResultMergeablePipelineItem
	// QualifyPaths returns the result with every path key rewritten by QualifyRepositoryPath.
	// The receiver's configuration is not consulted, so an unconfigured instance is enough.
	QualifyPaths(result any, repository string) any
}

// RepositoryPathSeparator separates the repository from the path in a qualified path key.
const RepositoryPathSeparator = ":"

// QualifyRepositoryPath prefixes a repository-local path with the repository which contains it.
// An empty repository leaves the path untouched, so results without a known origin keep their
// bare keys instead of gaining a meaningless prefix.
func QualifyRepositoryPath(repository, path string) string {
	if repository == "" {
		return path
	}

	return repository + RepositoryPathSeparator + path
}

// HibernateablePipelineItem is the interface to allow pipeline items to be frozen (compacted, unloaded)
// while they are not needed in the hosting branch.
type HibernateablePipelineItem interface {
	PipelineItem
	// Hibernate signals that the item is temporarily not needed and its run state can be compacted.
	// It must preserve configuration and logical run state, and repeated calls must be safe.
	Hibernate() error
	// Boot restores the exact pre-hibernation run state. Repeated calls must be safe.
	Boot() error
}

// CommonAnalysisResult holds the information which is always extracted at Pipeline.Run().
type CommonAnalysisResult struct {
	// BeginTime is the time of the first commit in the analysed sequence.
	BeginTime int64
	// EndTime is the time of the last commit in the analysed sequence.
	EndTime int64
	// CommitsNumber is the number of commits in the analysed sequence.
	CommitsNumber int
	// RunTime is the duration of Pipeline.Run().
	RunTime time.Duration
	// RunTimePerItem is the time elapsed by each PipelineItem.
	RunTimePerItem map[string]float64
}

// Copy produces a deep clone of the object.
func (car *CommonAnalysisResult) Copy() CommonAnalysisResult {
	result := *car

	result.RunTimePerItem = map[string]float64{}
	maps.Copy(result.RunTimePerItem, car.RunTimePerItem)

	return result
}

// BeginTimeAsTime converts the UNIX timestamp of the beginning to Go time.
func (car *CommonAnalysisResult) BeginTimeAsTime() time.Time {
	return time.Unix(car.BeginTime, 0)
}

// EndTimeAsTime converts the UNIX timestamp of the ending to Go time.
func (car *CommonAnalysisResult) EndTimeAsTime() time.Time {
	return time.Unix(car.EndTime, 0)
}

// Merge combines the CommonAnalysisResult with an other one.
// We choose the earlier BeginTime, the later EndTime, sum the number of commits and the
// elapsed run times.
func (car *CommonAnalysisResult) Merge(other *CommonAnalysisResult) {
	if car.EndTime == 0 || other.BeginTime == 0 {
		panic("Merging with an uninitialized CommonAnalysisResult")
	}

	if other.BeginTime < car.BeginTime {
		car.BeginTime = other.BeginTime
	}

	if other.EndTime > car.EndTime {
		car.EndTime = other.EndTime
	}

	car.CommitsNumber += other.CommitsNumber

	car.RunTime += other.RunTime
	for key, val := range other.RunTimePerItem {
		car.RunTimePerItem[key] += val
	}
}

// FillMetadata copies the data to a Protobuf message.
func (car *CommonAnalysisResult) FillMetadata(meta *pb.Metadata) *pb.Metadata {
	if car.CommitsNumber < math.MinInt32 || car.CommitsNumber > math.MaxInt32 {
		panic("commit count exceeds the protobuf int32 range")
	}

	meta.BeginUnixTime = car.BeginTime
	meta.EndUnixTime = car.EndTime
	meta.Commits = int32(car.CommitsNumber)
	meta.RunTime = car.RunTime.Nanoseconds() / 1e6
	meta.RunTimePerItem = car.RunTimePerItem

	return meta
}

// Metadata is defined in internal/pb/pb.pb.go - header of the binary file.
type Metadata = pb.Metadata

// MetadataToCommonAnalysisResult copies the data from a Protobuf message.
func MetadataToCommonAnalysisResult(meta *Metadata) *CommonAnalysisResult {
	return &CommonAnalysisResult{
		BeginTime:      meta.BeginUnixTime,
		EndTime:        meta.EndUnixTime,
		CommitsNumber:  int(meta.Commits),
		RunTime:        time.Duration(meta.RunTime * 1e6),
		RunTimePerItem: meta.RunTimePerItem,
	}
}

// Pipeline is the core Hercules entity which carries several PipelineItems and executes them.
// See the extended example of how a Pipeline works in doc.go.
type Pipeline struct {
	// OnProgress is the callback which is invoked in Analyse() to output it's
	// progress. The first argument is the number of complete steps, the
	// second is the total number of steps and the third is some description of the current action.
	OnProgress func(int, int, string)

	// HibernationDistance is the minimum number of actions between two sequential usages of
	// a branch to activate the hibernation optimization (cpu-memory trade-off). 0 disables.
	HibernationDistance int

	// DryRun indicates whether the items are not executed.
	DryRun bool

	// DumpPlan indicates whether to print the execution plan to stderr.
	DumpPlan bool

	// PrintActions indicates whether to print the taken actions during the execution.
	PrintActions bool

	// Repository points to the analysed Git repository struct from go-git.
	repository *git.Repository

	// Items are the registered building blocks in the pipeline. The order defines the
	// execution sequence.
	items []PipelineItem

	// Feature flags which enable the corresponding items.
	features map[string]bool

	// lifecycle contains state owned by one initialization/execution cycle.
	// It is deliberately separate from the pipeline topology and options above.
	lifecycle pipelineLifecycleState

	// The logger for printing output.
	l Logger

	output io.Writer
}

type pipelineLifecycleState struct {
	preparedRun          *preparedRun
	initializedResources []DisposablePipelineItem
}

type preparedRun struct {
	plan           []runAction
	commitCount    int
	mergeHashCount int
}

type pipelineRunState struct {
	pipeline       *Pipeline
	branches       map[int][]PipelineItem
	rootClone      []PipelineItem
	runTimePerItem map[string]float64
	mergeHashCount int
	commitIndex    int
	newestTime     int64
	disposables    []DisposablePipelineItem
	seenDisposable map[DisposablePipelineItem]struct{}
	disposed       bool

	// Hibernation is scheduled per branch index, but an item is free to share one instance
	// across branches (ForkSamePipelineItem). Tracking both is what keeps a branch from
	// hibernating an instance another, still-running branch is about to consume. See
	// changeHibernation.
	hibernatedBranches map[int]struct{}
	hibernatedItems    map[PipelineItem]struct{}
}

const (
	// ConfigPipelineDAGPath is the name of the Pipeline configuration option (Pipeline.Initialize())
	// which enables saving the items DAG to the specified file.
	ConfigPipelineDAGPath = "Pipeline.DAGPath"
	// ConfigPipelineDryRun is the name of the Pipeline configuration option (Pipeline.Initialize())
	// which disables Configure() and Initialize() invocation on each PipelineItem during the
	// Pipeline initialization.
	// Subsequent Run() calls are going to fail. Useful with ConfigPipelineDAGPath=true.
	ConfigPipelineDryRun = "Pipeline.DryRun"
	// ConfigPipelineCommits is the name of the Pipeline configuration option (Pipeline.Initialize())
	// which allows to specify the custom commit sequence. By default, Pipeline.Commits() is used.
	ConfigPipelineCommits = "Pipeline.Commits"
	// ConfigPipelineDumpPlan is the name of the Pipeline configuration option (Pipeline.Initialize())
	// which outputs the execution plan to stderr.
	ConfigPipelineDumpPlan = "Pipeline.DumpPlan"
	// ConfigPipelineHibernationDistance is the name of the Pipeline configuration option (Pipeline.Initialize())
	// which is the minimum number of actions between two sequential usages of
	// a branch to activate the hibernation optimization (cpu-memory trade-off). 0 disables.
	ConfigPipelineHibernationDistance = "Pipeline.HibernationDistance"
	// ConfigPipelinePrintActions is the name of the Pipeline configuration option (Pipeline.Initialize())
	// which enables printing the taken actions of the execution plan to stderr.
	ConfigPipelinePrintActions = "Pipeline.PrintActions"
	// DependencyCommit is the name of one of the three items in `deps` supplied to PipelineItem.Consume()
	// which always exists. It corresponds to the currently analyzed commit.
	DependencyCommit = "commit"
	// DependencyIndex is the name of one of the three items in `deps` supplied to PipelineItem.Consume()
	// which always exists. It corresponds to the currently analyzed commit's index.
	DependencyIndex = "index"
	// DependencyIsMerge is the name of one of the three items in `deps` supplied to PipelineItem.Consume()
	// which always exists. It indicates whether the analyzed commit is a merge commit.
	// Checking the number of parents is not correct - we remove the back edges during the DAG simplification.
	DependencyIsMerge = "is_merge"
	// DependencyIsMergeReplica indicates that this consumption of a merge commit exists only to
	// bring a parent branch's state in line with the merge, and that another branch is accounting
	// for it. Items which accumulate anything per commit must ignore these, or the merge is
	// counted once per parent. See planBuilder.appendMergeReplicas.
	DependencyIsMergeReplica = "is_merge_replica"
	// MessageFinalize is the status text reported before calling LeafPipelineItem.Finalize()-s.
	MessageFinalize = "finalize"

	DependencyNextMerge = "next_merge"
	FactMergeHashCount  = "merge_count"
	FeatureMergeTracks  = "merge_tracks"
)

// NewPipeline initializes a new instance of Pipeline struct.
func NewPipeline(repository *git.Repository) *Pipeline {
	return &Pipeline{
		repository: repository,
		items:      []PipelineItem{},
		features:   map[string]bool{},
		l:          NewLogger(),
		output:     os.Stderr,
	}
}

// GetFeature returns the state of the feature with the specified name (enabled/disabled) and
// whether it exists. See also: FeaturedPipelineItem.
func (pipeline *Pipeline) GetFeature(name string) (bool, bool) {
	val, exists := pipeline.features[name]
	return val, exists
}

// SetFeature sets the value of the feature with the specified name.
// See also: FeaturedPipelineItem.
func (pipeline *Pipeline) SetFeature(name string) {
	pipeline.features[name] = true
}

// SetFeaturesFromFlags enables the features which were specified through the command line flags
// which belong to the given PipelineItemRegistry instance.
// See also: AddItem().
func (pipeline *Pipeline) SetFeaturesFromFlags(registry ...*PipelineItemRegistry) {
	var ffr *PipelineItemRegistry

	switch len(registry) {
	case 0:
		ffr = Registry
	case 1:
		ffr = registry[0]
	default:
		panic("Zero or one registry is allowed to be passed.")
	}

	for _, feature := range ffr.featureFlags.Flags {
		pipeline.SetFeature(feature)
	}
}

// DeployItem inserts a PipelineItem into the pipeline. It also recursively creates all of it's
// dependencies (PipelineItem.Requires()). Returns the same item as specified in the arguments.
func (pipeline *Pipeline) DeployItem(item PipelineItem) PipelineItem {
	return pipeline.deployItem(item, false)
}

func (pipeline *Pipeline) DeployItemOnce(item PipelineItem) PipelineItem {
	return pipeline.deployItem(item, true)
}

// AddItem inserts a PipelineItem into the pipeline. It does not check any dependencies.
// See also: DeployItem().
func (pipeline *Pipeline) AddItem(item PipelineItem) PipelineItem {
	pipeline.items = append(pipeline.items, item)
	return item
}

// RemoveItem deletes a PipelineItem from the pipeline. It leaves all the rest of the items intact.
func (pipeline *Pipeline) RemoveItem(item PipelineItem) {
	for i, reg := range pipeline.items {
		if reg == item {
			pipeline.items = append(pipeline.items[:i], pipeline.items[i+1:]...)
			return
		}
	}
}

// Len returns the number of items in the pipeline.
func (pipeline *Pipeline) Len() int {
	return len(pipeline.items)
}

// Commits returns the list of commits from the history similar to `git log` over the HEAD.
// `firstParent` specifies whether to leave only the first parent after each merge
// (`git log --first-parent`) - effectively decreasing the accuracy but increasing performance.
func (pipeline *Pipeline) Commits(firstParent bool) ([]*object.Commit, error) {
	var result []*object.Commit
	repository := pipeline.repository

	heads, err := pipeline.HeadCommit()
	if err != nil {
		return nil, err
	}

	head := heads[0]
	if firstParent {
		// the first parent matches the head
		for commit := head; !errors.Is(err, io.EOF); commit, err = commit.Parents().Next() {
			if err != nil {
				panic(err)
			}

			result = append(result, commit)
		}
		// reverse the order
		for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
			result[i], result[j] = result[j], result[i]
		}

		return result, nil
	}

	cit, err := repository.Log(&git.LogOptions{From: head.Hash})
	if err != nil {
		return nil, errors.Wrap(err, "unable to collect the commit history")
	}
	defer cit.Close()

	err = cit.ForEach(func(commit *object.Commit) error {
		result = append(result, commit)
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "unable to iterate the commit history")
	}

	return result, nil
}

// HeadCommit returns the latest commit in the repository (HEAD).
func (pipeline *Pipeline) HeadCommit() ([]*object.Commit, error) {
	repository := pipeline.repository

	head, err := repository.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		head, err = pipeline.fallbackHeadReference()
	}

	if err != nil {
		return nil, errors.Wrap(err, "unable to find the head reference")
	}

	if head == nil {
		return nil, errors.Wrap(err, "unable to find the head reference")
	}

	commit, err := repository.CommitObject(head.Hash())
	if err != nil {
		return nil, errors.Wrap(err, "unable to load the head commit")
	}

	return []*object.Commit{commit}, nil
}

type sortablePipelineItems []PipelineItem

func (items sortablePipelineItems) Len() int {
	return len(items)
}

func (items sortablePipelineItems) Less(i, j int) bool {
	return items[i].Name() < items[j].Name()
}

func (items sortablePipelineItems) Swap(i, j int) {
	items[i], items[j] = items[j], items[i]
}

// Initialize prepares the pipeline for the execution (Run()). This function
// resolves the execution DAG, Configure()-s and Initialize()-s the items in it in the
// topological dependency order. `facts` are passed inside Configure(). They are mutable.
func (pipeline *Pipeline) Initialize(facts map[string]any) error {
	if _, exists := facts[ConfigPipelineCommits]; !exists {
		var err error

		facts[ConfigPipelineCommits], err = pipeline.Commits(false)
		if err != nil {
			pipeline.l.Errorf("failed to list the commits: %v", err)
			return err
		}
	}

	return pipeline.InitializeExt(facts, func(items []PipelineItem) PipelineItem {
		return items[0]
	}, false)
}

type DependencyPriorityFunc = func(items []PipelineItem) PipelineItem

func (pipeline *Pipeline) InitializeExt(facts map[string]any,
	priorityFn DependencyPriorityFunc, preparePlan bool,
) error {
	cleanReturn := false
	defer pipeline.logInitializationFailure(&cleanReturn)

	// Beginning a new cycle invalidates resources owned by the previous one,
	// even if validation or DAG resolution for the replacement later fails.
	pipeline.disposeInitializedResources()
	pipeline.lifecycle.preparedRun = nil

	err := prepareInitialization(pipeline, facts, priorityFn, preparePlan)
	if err != nil {
		return err
	}

	if pipeline.DryRun {
		cleanReturn = true
		return nil
	}

	initialized := false
	defer func() {
		if !initialized {
			pipeline.disposeItems(pipeline.items)
		}
	}()

	err = pipeline.configureItems(facts)
	if err != nil {
		cleanReturn = true
		return err
	}

	if preparePlan && pipeline.lifecycle.preparedRun == nil {
		mergeTracks, _ := pipeline.GetFeature(FeatureMergeTracks)

		err = pipeline.prepareRun(facts, mergeTracks)
		if err != nil {
			cleanReturn = true
			return err
		}
	}

	err = pipeline.initializeItems(facts)
	if err != nil {
		cleanReturn = true
		return err
	}

	initialized = true
	cleanReturn = true

	return nil
}

func prepareInitialization(
	pipeline *Pipeline,
	facts map[string]any, priorityFn DependencyPriorityFunc, preparePlan bool,
) error {
	err := pipeline.validateConfigurationFactTypes(facts)
	if err != nil {
		return err
	}

	err = pipeline.applyConfigurationFacts(facts)
	if err != nil {
		return err
	}

	dumpPath, _ := facts[ConfigPipelineDAGPath].(string)

	err = pipeline.resolve(dumpPath, priorityFn)
	if err != nil {
		return err
	}

	mergeTracks, _ := pipeline.GetFeature(FeatureMergeTracks)
	if !preparePlan && mergeTracks {
		return errors.New("merge tracks mode is not allowed")
	}

	if preparePlan {
		pipeline.lifecycle.preparedRun = nil
		if _, exists := facts[ConfigPipelineCommits]; exists {
			return pipeline.prepareRun(facts, mergeTracks)
		}

		return nil
	}

	if commitValue, exists := facts[ConfigPipelineCommits]; exists {
		commits, ok := commitValue.([]*object.Commit)
		if !ok {
			return fmt.Errorf("%w: %s has type %T",
				ErrNoCommits, ConfigPipelineCommits, commitValue)
		}

		return validateCommitInput(commits)
	}

	return nil
}

// Run method executes the pipeline.
//
// `commits` is a slice with the git commits to analyse. Multiple branches are supported.
//
// Returns the mapping from each LeafPipelineItem to the corresponding analysis result.
// There is always a "nil" record with CommonAnalysisResult.
func (pipeline *Pipeline) Run(commits []*object.Commit) (map[LeafPipelineItem]any, error) {
	plan, _, err := prepareRunPlan(commits, pipeline.HibernationDistance, false)
	if err != nil {
		return nil, err
	}

	return pipeline.runPlan(plan, len(commits), -1)
}

func (pipeline *Pipeline) RunPreparedPlan() (map[LeafPipelineItem]any, error) {
	prepared := pipeline.lifecycle.preparedRun
	pipeline.lifecycle.preparedRun = nil

	if prepared == nil {
		return nil, errors.New("run plan was not prepared")
	}

	return pipeline.runPlan(prepared.plan, prepared.commitCount, prepared.mergeHashCount)
}

func (pipeline *Pipeline) fallbackHeadReference() (*plumbing.Reference, error) {
	refs, err := pipeline.repository.References()
	if err != nil {
		return nil, errors.Wrap(err, "unable to list the references")
	}
	defer refs.Close()

	var preferred *plumbing.Reference
	var refnames []string
	refByName := map[string]*plumbing.Reference{}

	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Hash() == plumbing.ZeroHash {
			return nil
		}

		refname := ref.Name().String()
		refnames = append(refnames, refname)

		refByName[refname] = ref
		if strings.HasPrefix(refname, "refs/heads/HEAD/") {
			preferred = ref
			return storer.ErrStop
		}

		return nil
	})
	if err != nil && !errors.Is(err, storer.ErrStop) {
		return nil, errors.Wrap(err, "unable to iterate the references")
	}

	if preferred != nil {
		return preferred, nil
	}

	if len(refnames) == 0 {
		return nil, ErrNoReferences
	}

	sort.Strings(refnames)
	headName := refnames[len(refnames)-1]
	pipeline.l.Warnf("could not determine the HEAD, falling back to %s", headName)

	return refByName[headName], nil
}

func (pipeline *Pipeline) deployItem(item PipelineItem, once bool) PipelineItem {
	added := map[string]PipelineItem{}
	for _, existingItem := range pipeline.items {
		added[existingItem.Name()] = existingItem
	}

	if prevItem, present := added[item.Name()]; once && present {
		return prevItem
	}

	added[item.Name()] = item

	pipeline.enableItemFeatures(item)
	pipeline.AddItem(item)
	pipeline.deployRequiredItems([]PipelineItem{item}, added)

	return item
}

func (pipeline *Pipeline) enableItemFeatures(item PipelineItem) {
	fpi, ok := item.(FeaturedPipelineItem)
	if !ok {
		return
	}

	for _, feature := range fpi.Features() {
		pipeline.SetFeature(feature)
	}
}

func (pipeline *Pipeline) deployRequiredItems(
	queue []PipelineItem,
	added map[string]PipelineItem,
) {
	for len(queue) != 0 {
		head := queue[0]
		queue = queue[1:]

		for _, dep := range head.Requires() {
			for _, sibling := range Registry.Summon(dep) {
				if _, exists := added[sibling.Name()]; exists {
					continue
				}

				if !pipeline.itemFeaturesEnabled(sibling) {
					continue
				}

				added[sibling.Name()] = sibling
				queue = append(queue, sibling)
				pipeline.AddItem(sibling)
			}
		}
	}
}

func (pipeline *Pipeline) itemFeaturesEnabled(item PipelineItem) bool {
	fpi, matches := item.(FeaturedPipelineItem)
	if !matches {
		return true
	}

	// If this item supports features, check them against the activated features.
	for _, feature := range fpi.Features() {
		if !pipeline.features[feature] {
			return false
		}
	}

	return true
}

// LoadCommitsFromFile reads the file by the specified FS path and generates the sequence of commits
// by interpreting each line as a Git commit hash.
func LoadCommitsFromFile(path string, repository *git.Repository) ([]*object.Commit, error) {
	var file io.ReadCloser

	if path != "-" {
		var err error

		file, err = os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open commits file %q: %w", path, err)
		}

		defer func() { _ = file.Close() }()
	} else {
		file = os.Stdin
	}

	scanner := bufio.NewScanner(file)
	var commits []*object.Commit
	seen := map[plumbing.Hash]int{}
	lineNumber := 0

	for scanner.Scan() {
		lineNumber++

		text := strings.TrimSpace(scanner.Text())
		if !plumbing.IsHash(text) {
			return nil, fmt.Errorf("%w in commits file %q at line %d: malformed hash %q",
				ErrInvalidCommit, path, lineNumber, text)
		}

		hash := plumbing.NewHash(text)
		if firstLine, exists := seen[hash]; exists {
			return nil, &DuplicateCommitError{
				Hash:           hash,
				FirstPosition:  firstLine,
				SecondPosition: lineNumber,
			}
		}

		seen[hash] = lineNumber

		commit, err := repository.CommitObject(hash)
		if err != nil {
			return nil, fmt.Errorf("load commit %s: %w", hash, err)
		}

		commits = append(commits, commit)
	}

	scanErr := scanner.Err()
	if scanErr != nil {
		return nil, fmt.Errorf("scan commits file %q: %w", path, scanErr)
	}

	if len(commits) == 0 {
		return nil, fmt.Errorf("%w: commits file %q is empty", ErrNoCommits, path)
	}

	return commits, nil
}

// GetSensibleRemote extracts a remote URL of the repository to identify it.
func GetSensibleRemote(repository *git.Repository) string {
	remotes, err := repository.Remotes()
	if err == nil && len(remotes) > 0 {
		return remotes[0].Config().URLs[0]
	}

	return "<no remote>"
}
