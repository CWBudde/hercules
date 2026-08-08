package hercules

import (
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/spf13/pflag"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/yaml"
	_ "github.com/cwbudde/hercules/leaves"          // add burndown and other analyses
	_ "github.com/cwbudde/hercules/leaves/research" // add "research" analyses
)

// ConfigurationOptionType represents the possible types of a ConfigurationOption's value.
type ConfigurationOptionType = core.ConfigurationOptionType

const (
	// BoolConfigurationOption reflects the boolean value type.
	BoolConfigurationOption = core.BoolConfigurationOption
	// IntConfigurationOption reflects the integer value type.
	IntConfigurationOption = core.IntConfigurationOption
	// StringConfigurationOption reflects the string value type.
	StringConfigurationOption = core.StringConfigurationOption
	// FloatConfigurationOption reflects a floating point value type.
	FloatConfigurationOption = core.FloatConfigurationOption
	// StringsConfigurationOption reflects the array of strings value type.
	StringsConfigurationOption = core.StringsConfigurationOption
	// PathConfigurationOption reflects a filesystem path value type.
	PathConfigurationOption = core.PathConfigurationOption
	// MessageFinalize is the status text reported before calling LeafPipelineItem.Finalize()-s.
	MessageFinalize = core.MessageFinalize
)

// ConfigurationOption allows for the unified, retrospective way to setup PipelineItem-s.
type ConfigurationOption = core.ConfigurationOption

// PipelineItem is the interface for all the units in the Git commits analysis pipeline.
type PipelineItem = core.PipelineItem

// FeaturedPipelineItem enables switching the automatic insertion of pipeline items on or off.
type FeaturedPipelineItem = core.FeaturedPipelineItem

// DisposablePipelineItem owns resources which are released after a pipeline run.
type DisposablePipelineItem = core.DisposablePipelineItem

// HibernateablePipelineItem can compact and restore branch-local run state.
type HibernateablePipelineItem = core.HibernateablePipelineItem

// LeafPipelineItem corresponds to the top level pipeline items which produce the end results.
type LeafPipelineItem = core.LeafPipelineItem

// ResultMergeablePipelineItem specifies the methods to combine several analysis results together.
type ResultMergeablePipelineItem = core.ResultMergeablePipelineItem

// RepositoryQualifiablePipelineItem produces results keyed by repository-local paths.
type RepositoryQualifiablePipelineItem = core.RepositoryQualifiablePipelineItem

// RepositoryPathSeparator separates the repository from the path in a qualified path key.
const RepositoryPathSeparator = core.RepositoryPathSeparator

// QualifyRepositoryPath prefixes a repository-local path with the repository which contains it.
// Implementations of RepositoryQualifiablePipelineItem must build their keys with it rather than
// concatenating the separator themselves.
func QualifyRepositoryPath(repository, path string) string {
	return core.QualifyRepositoryPath(repository, path)
}

// CommonAnalysisResult holds the information which is always extracted at Pipeline.Run().
type CommonAnalysisResult = core.CommonAnalysisResult

// NoopMerger provides an empty Merge() method suitable for PipelineItem.
type NoopMerger = core.NoopMerger

// OneShotMergeProcessor provides the convenience method to consume merges only once.
type OneShotMergeProcessor = core.OneShotMergeProcessor

// CommitComponent describes one connected component in explicit commit input.
type CommitComponent = core.CommitComponent

// DuplicateCommitError describes a repeated hash in explicit commit input.
type DuplicateCommitError = core.DuplicateCommitError

// DisconnectedCommitsError describes disconnected explicit commit input.
type DisconnectedCommitsError = core.DisconnectedCommitsError

var (
	// ErrNoCommits indicates that an explicit commit input or execution plan is empty.
	ErrNoCommits = core.ErrNoCommits
	// ErrNoReferences indicates that a repository does not contain a usable commit reference.
	ErrNoReferences = core.ErrNoReferences
	// ErrInvalidCommit indicates that explicit input contains a nil or zero-hash commit.
	ErrInvalidCommit = core.ErrInvalidCommit
	// ErrDuplicateCommits indicates that explicit input repeats a commit hash.
	ErrDuplicateCommits = core.ErrDuplicateCommits
	// ErrDisconnectedCommits indicates that explicit input has disconnected components.
	ErrDisconnectedCommits = core.ErrDisconnectedCommits
	// ErrInvalidFactType indicates that a configuration or shared fact has an unexpected type.
	ErrInvalidFactType = core.ErrInvalidFactType
	// ErrFactMissing indicates that a required fact is absent.
	ErrFactMissing = core.ErrFactMissing
)

// MetadataToCommonAnalysisResult copies the data from a Protobuf message.
func MetadataToCommonAnalysisResult(meta *core.Metadata) *CommonAnalysisResult {
	return core.MetadataToCommonAnalysisResult(meta)
}

// Pipeline is the core Hercules entity which carries several PipelineItems and executes them.
// See the extended example of how a Pipeline works in doc.go.
type Pipeline = core.Pipeline

const (
	// ConfigPipelineDAGPath is the name of the Pipeline configuration option (Pipeline.Initialize())
	// which enables saving the items DAG to the specified file.
	ConfigPipelineDAGPath = core.ConfigPipelineDAGPath
	// ConfigPipelineDumpPlan is the name of the Pipeline configuration option (Pipeline.Initialize())
	// which outputs the execution plan to stderr.
	ConfigPipelineDumpPlan = core.ConfigPipelineDumpPlan
	// ConfigPipelineDryRun is the name of the Pipeline configuration option (Pipeline.Initialize())
	// which disables Configure() and Initialize() invocation on each PipelineItem during the
	// Pipeline initialization.
	// Subsequent Run() calls are going to fail. Useful with ConfigPipelineDAGPath=true.
	ConfigPipelineDryRun = core.ConfigPipelineDryRun
	// ConfigPipelineCommits is the name of the Pipeline configuration option (Pipeline.Initialize())
	// which allows to specify the custom commit sequence. By default, Pipeline.Commits() is used.
	ConfigPipelineCommits = core.ConfigPipelineCommits
	// ConfigTickSize is the number of hours per 'tick'.
	ConfigTickSize = plumbing.ConfigTicksSinceStartTickSize
	// ConfigLogger is used to set the logger in all pipeline items.
	ConfigLogger = core.ConfigLogger
)

// NewPipeline initializes a new instance of Pipeline struct.
func NewPipeline(repository *git.Repository) *Pipeline {
	return core.NewPipeline(repository)
}

// LoadCommitsFromFile reads the file by the specified FS path and generates the sequence of commits
// by interpreting each line as a Git commit hash.
func LoadCommitsFromFile(path string, repository *git.Repository) ([]*object.Commit, error) {
	commits, err := core.LoadCommitsFromFile(path, repository)
	if err != nil {
		return nil, fmt.Errorf("load commits from file: %w", err)
	}

	return commits, nil
}

// ForkSamePipelineItem clones items by referencing the same origin.
func ForkSamePipelineItem(origin PipelineItem, n int) []PipelineItem {
	return core.ForkSamePipelineItem(origin, n)
}

// ForkCopyPipelineItem clones items by copying them by value from the origin.
func ForkCopyPipelineItem(origin PipelineItem, n int) []PipelineItem {
	return core.ForkCopyPipelineItem(origin, n)
}

// PipelineItemRegistry contains all the known PipelineItem-s.
type PipelineItemRegistry = core.PipelineItemRegistry

// FlagConfiguration retains typed flag storage until it is snapshotted after parsing.
type FlagConfiguration = core.FlagConfiguration

// Registry contains all known pipeline item types.
var Registry = core.Registry

// FactTypeError describes a type mismatch in a configuration or shared fact.
type FactTypeError = core.FactTypeError

// IdentityResolver provides typed access to configured author identities.
type IdentityResolver = core.IdentityResolver

// FileIdResolver provides typed access to configured file identities.
type FileIdResolver = core.FileIdResolver

const (
	// DependencyCommit is the name of one of the three items in `deps` supplied to PipelineItem.Consume()
	// which always exists. It corresponds to the currently analyzed commit.
	DependencyCommit = core.DependencyCommit
	// DependencyIndex is the name of one of the three items in `deps` supplied to PipelineItem.Consume()
	// which always exists. It corresponds to the currently analyzed commit's index.
	DependencyIndex = core.DependencyIndex
	// DependencyIsMerge is the name of one of the three items in `deps` supplied to PipelineItem.Consume()
	// which always exists. It indicates whether the analyzed commit is a merge commit.
	// Checking the number of parents is not correct - we remove the back edges during the DAG simplification.
	DependencyIsMerge = core.DependencyIsMerge
	// DependencyAuthor is the name of the dependency provided by identity.PeopleDetector.
	DependencyAuthor = identity.DependencyAuthor
	// DependencyBlobCache identifies the dependency provided by BlobCache.
	DependencyBlobCache = plumbing.DependencyBlobCache
	// DependencyTick is the name of the dependency which TicksSinceStart provides - the number
	// of ticks since the first commit in the analysed sequence.
	DependencyTick = plumbing.DependencyTick
	// DependencyFileDiff is the name of the dependency provided by FileDiff.
	DependencyFileDiff = plumbing.DependencyFileDiff
	// DependencyTreeChanges is the name of the dependency provided by TreeDiff.
	DependencyTreeChanges = plumbing.DependencyTreeChanges
	// FactCommitsByTick contains the mapping between tick indices and the corresponding commits.
	FactCommitsByTick = plumbing.FactCommitsByTick
	// FactIdentityDetectorReversedPeopleDict is the name of the fact which is inserted in
	// identity.PeopleDetector.Configure(). It corresponds to identity.PeopleDetector.ReversedPeopleDict -
	// the mapping from the author indices to the main signature.
	FactIdentityDetectorReversedPeopleDict = identity.FactIdentityDetectorReversedPeopleDict
	// FactIdentityResolver identifies the typed author identity resolver.
	FactIdentityResolver = core.FactIdentityResolver
	// FactLineHistoryResolver identifies the typed file identity resolver.
	FactLineHistoryResolver = core.FactLineHistoryResolver
)

// FactValue reads an optional fact with an exact type check.
func FactValue[T any](facts map[string]any, key string) (T, bool, error) {
	return core.FactValue[T](facts, key)
}

// RequiredFactValue reads a required fact with an exact type check.
func RequiredFactValue[T any](facts map[string]any, key string) (T, error) {
	return core.RequiredFactValue[T](facts, key)
}

// FileDiffData is the type of the dependency provided by plumbing.FileDiff.
type FileDiffData = plumbing.FileDiffData

// CachedBlob allows to explicitly cache the binary data associated with the Blob object.
// Such structs are returned by DependencyBlobCache.
type CachedBlob = plumbing.CachedBlob

// SafeYamlString escapes the string so that it can be reliably used in YAML.
func SafeYamlString(str string) string {
	return yaml.SafeString(str)
}

// PathifyFlagValue changes the type of a string command line argument to "path".
func PathifyFlagValue(flag *pflag.Flag) {
	core.PathifyFlagValue(flag)
}

// EnablePathFlagTypeMasquerade changes the type of all "path" command line arguments from "string"
// to "path". This operation cannot be canceled and is intended to be used for better --help output.
func EnablePathFlagTypeMasquerade() {
	core.EnablePathFlagTypeMasquerade()
}

// Logger is the Hercules logging interface.
type Logger core.Logger

// NewLogger returns an instance of the default Hercules logger.
func NewLogger() core.Logger { return core.NewLogger() }
