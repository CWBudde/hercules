package plumbing

import (
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/src-d/enry/v2"

	"github.com/cwbudde/hercules/internal/core"
)

// TreeDiff generates the list of changes for a commit. A change can be either one or two blobs
// under the same path: "before" and "after". If "before" is nil, the change is an addition.
// If "after" is nil, the change is a removal. Otherwise, it is a modification.
// TreeDiff is a PipelineItem.
type TreeDiff struct {
	core.NoopMerger

	SkipFiles  []string
	NameFilter *regexp.Regexp
	// Languages is the set of allowed languages. The values must be lower case. The default
	// (empty) set disables the language filter.
	Languages map[string]bool

	previousTree   *object.Tree
	previousCommit plumbing.Hash
	repository     *git.Repository
	skipVendored   bool

	l core.Logger
}

const (
	// DependencyTreeChanges is the name of the dependency provided by TreeDiff.
	DependencyTreeChanges = "changes"
	// ConfigTreeDiffEnableBlacklist is the name of the configuration option
	// (TreeDiff.Configure()) which allows to skip blacklisted directories.
	ConfigTreeDiffEnableBlacklist = "TreeDiff.EnableBlacklist"
	// ConfigTreeDiffBlacklistedPrefixes s the name of the configuration option
	// (TreeDiff.Configure()) which allows to set blacklisted path prefixes -
	// directories or complete file names.
	ConfigTreeDiffBlacklistedPrefixes = "TreeDiff.BlacklistedPrefixes"
	// ConfigTreeDiffLanguages is the name of the configuration option (TreeDiff.Configure())
	// which sets the list of programming languages to analyze. Language names are at
	// https://doc.bblf.sh/languages.html Names are joined with a comma ",".
	// "all" is the special name which disables this filter.
	ConfigTreeDiffLanguages = "TreeDiff.LanguagesDetection"
	// allLanguages denotes passing all files in.
	allLanguages = "all"

	// ConfigTreeDiffFilterRegexp is the name of the configuration option
	// (TreeDiff.Configure()) which makes FileDiff consider only those files which have names matching this regexp.
	ConfigTreeDiffFilterRegexp = "TreeDiff.FilteredRegexes"
)

var (
	errCommitDoesNotContinue      = errors.New("commit does not continue from previous commit")
	errInvalidTreeDiffConfigValue = errors.New("invalid tree diff configuration value")
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (treediff *TreeDiff) Name() string {
	return "TreeDiff"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (treediff *TreeDiff) Provides() []string {
	return []string{DependencyTreeChanges}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (treediff *TreeDiff) Requires() []string {
	return []string{}
}

func (*TreeDiff) Features() []string {
	return []string{core.FeatureGitCommits}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (treediff *TreeDiff) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name: ConfigTreeDiffEnableBlacklist,
			Description: "Skip blacklisted directories and vendored files (according to " +
				"src-d/enry.IsVendor).",
			Flag:    "skip-blacklist",
			Type:    core.BoolConfigurationOption,
			Default: false,
		}, {
			Name: ConfigTreeDiffBlacklistedPrefixes,
			Description: "List of blacklisted path prefixes (e.g. directories or specific files). " +
				"Values are in the UNIX format (\"path/to/x\"). Values should *not* start with \"/\". " +
				"Separated with commas \",\".",
			Flag: "blacklisted-prefixes",
			Type: core.StringsConfigurationOption,
			Default: []string{
				"vendor/",
				"vendors/",
				"package-lock.json",
				"Gopkg.lock",
			},
		}, {
			Name: ConfigTreeDiffLanguages,
			Description: fmt.Sprintf(
				"List of programming languages to analyze. Separated by comma \",\". "+
					"The names are the keys in https://github.com/github/linguist/blob/master/lib/linguist/languages.yml "+
					"\"%s\" is the special name which disables this filter and lets all the files through.",
				allLanguages,
			),
			Flag:    "languages",
			Type:    core.StringsConfigurationOption,
			Default: []string{allLanguages},
		}, {
			Name:        ConfigTreeDiffFilterRegexp,
			Description: "Whitelist regexp to determine which files to analyze.",
			Flag:        "whitelist",
			Type:        core.StringConfigurationOption,
			Default:     "",
		},
	}

	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (treediff *TreeDiff) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		treediff.l = l
	}

	if val, exists := facts[ConfigTreeDiffEnableBlacklist].(bool); exists && val {
		prefixes, ok := facts[ConfigTreeDiffBlacklistedPrefixes].([]string)
		if !ok {
			return fmt.Errorf(
				"%w: configuration %q has type %T, expected []string",
				errInvalidTreeDiffConfigValue,
				ConfigTreeDiffBlacklistedPrefixes,
				facts[ConfigTreeDiffBlacklistedPrefixes],
			)
		}

		treediff.SkipFiles = prefixes
		treediff.skipVendored = true
	}

	if val, exists := facts[ConfigTreeDiffLanguages].([]string); exists {
		treediff.Languages = map[string]bool{}
		for _, lang := range val {
			treediff.Languages[strings.ToLower(strings.TrimSpace(lang))] = true
		}
	} else if treediff.Languages == nil {
		treediff.Languages = map[string]bool{}
		treediff.Languages[allLanguages] = true
	}

	if val, exists := facts[ConfigTreeDiffFilterRegexp].(string); exists {
		nameFilter, err := regexp.Compile(val)
		if err != nil {
			return fmt.Errorf(
				"%w: configuration %q contains invalid regular expression %q: %w",
				errInvalidTreeDiffConfigValue,
				ConfigTreeDiffFilterRegexp,
				val,
				err,
			)
		}

		treediff.NameFilter = nameFilter
	}

	return nil
}

func (*TreeDiff) ConfigureUpstream(map[string]any) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (treediff *TreeDiff) Initialize(repository *git.Repository) error {
	treediff.l = core.NewLogger()
	treediff.previousTree = nil
	treediff.previousCommit = plumbing.ZeroHash

	treediff.repository = repository
	if treediff.Languages == nil {
		treediff.Languages = map[string]bool{}
		treediff.Languages[allLanguages] = true
	}

	return nil
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (treediff *TreeDiff) Consume(deps map[string]any) (map[string]any, error) {
	commit, err := dependencyValue[*object.Commit](deps, core.DependencyCommit)
	if err != nil {
		return nil, err
	}

	if !commitContinuesFrom(commit, treediff.previousCommit) {
		err := fmt.Errorf(
			"%w: %s > %s",
			errCommitDoesNotContinue,
			treediff.previousCommit.String(),
			commit.Hash.String(),
		)
		treediff.l.Critical(err)

		return nil, err
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("load tree for commit %s: %w", commit.Hash, err)
	}

	var diffs object.Changes
	if treediff.previousTree != nil {
		diffs, err = object.DiffTree(treediff.previousTree, tree)
	} else {
		diffs, err = treediff.initialTreeChanges(tree)
	}

	if err != nil {
		return nil, err
	}

	diffs, err = treediff.filterDiffs(diffs)
	if err != nil {
		return nil, err
	}

	treediff.previousTree = tree
	treediff.previousCommit = commit.Hash

	return map[string]any{DependencyTreeChanges: diffs}, nil
}

func commitContinuesFrom(commit *object.Commit, previous plumbing.Hash) bool {
	if previous == plumbing.ZeroHash {
		return true
	}

	return slices.Contains(commit.ParentHashes, previous)
}

// Fork clones this PipelineItem.
func (treediff *TreeDiff) Fork(n int) []core.PipelineItem {
	return core.ForkCopyPipelineItem(treediff, n)
}

func (treediff *TreeDiff) initialTreeChanges(tree *object.Tree) (object.Changes, error) {
	var changes object.Changes

	fileIter := tree.Files()
	defer fileIter.Close()

	for {
		file, err := fileIter.Next()
		if errors.Is(err, io.EOF) {
			return changes, nil
		}

		if err != nil {
			return nil, fmt.Errorf("iterate files in initial tree %s: %w", tree.Hash, err)
		}

		entry := object.ChangeEntry{Name: file.Name, Tree: tree, TreeEntry: object.TreeEntry{
			Name: file.Name, Mode: file.Mode, Hash: file.Hash,
		}}
		changes = append(changes, &object.Change{To: entry})
	}
}

func (treediff *TreeDiff) filterDiffs(diffs object.Changes) (object.Changes, error) {
	filteredDiffs := make(object.Changes, 0, len(diffs))

	for _, change := range diffs {
		filtered, include, err := treediff.filterDiff(change)
		if err != nil {
			return nil, err
		}

		if include {
			filteredDiffs = append(filteredDiffs, &filtered)
		}
	}

	return filteredDiffs, nil
}

func (treediff *TreeDiff) filterDiff(change *object.Change) (object.Change, bool, error) {
	action, err := change.Action()
	if err != nil {
		return object.Change{}, false, fmt.Errorf("classify tree change before filtering: %w", err)
	}

	fromPasses, toPasses, err := treediff.changeEntriesPassFilters(change, action)
	if err != nil {
		return object.Change{}, false, err
	}

	filtered, include := filteredChangeTransition(change, fromPasses, toPasses)

	return filtered, include, nil
}

func (treediff *TreeDiff) changeEntriesPassFilters(
	change *object.Change, action merkletrie.Action,
) (bool, bool, error) {
	fromPasses := false

	if action != merkletrie.Insert {
		pass, err := treediff.entryPassesFilters(change.From)
		if err != nil {
			return false, false, fmt.Errorf("filter old path %q: %w", change.From.Name, err)
		}

		fromPasses = pass
	}

	toPasses := false

	if action != merkletrie.Delete {
		pass, err := treediff.entryPassesFilters(change.To)
		if err != nil {
			return false, false, fmt.Errorf("filter new path %q: %w", change.To.Name, err)
		}

		toPasses = pass
	}

	return fromPasses, toPasses, nil
}

func filteredChangeTransition(
	change *object.Change, fromPasses, toPasses bool,
) (object.Change, bool) {
	switch {
	case fromPasses && toPasses:
		return *change, true
	case fromPasses:
		return object.Change{From: change.From}, true
	case toPasses:
		return object.Change{To: change.To}, true
	default:
		return object.Change{}, false
	}
}

func (treediff *TreeDiff) entryPassesFilters(entry object.ChangeEntry) (bool, error) {
	if treediff.isSkippedPath(entry.Name) || !treediff.matchesNameFilter(entry.Name) {
		return false, nil
	}

	return treediff.checkLanguage(entry.Name, entry.TreeEntry.Hash)
}

func (treediff *TreeDiff) isSkippedPath(name string) bool {
	if !treediff.skipVendored && len(treediff.SkipFiles) == 0 {
		return false
	}

	if enry.IsVendor(name) {
		return true
	}

	for _, skippedPath := range treediff.SkipFiles {
		if strings.HasPrefix(name, skippedPath) {
			return true
		}
	}

	return false
}

func (treediff *TreeDiff) matchesNameFilter(name string) bool {
	if treediff.NameFilter == nil {
		return true
	}

	return treediff.NameFilter.MatchString(name)
}

// checkLanguage returns whether the blob corresponds to the list of required languages.
func (treediff *TreeDiff) checkLanguage(name string, blobHash plumbing.Hash) (bool, error) {
	if treediff.Languages[allLanguages] {
		return true, nil
	}

	blob, err := treediff.repository.BlobObject(blobHash)
	if err != nil {
		return false, fmt.Errorf("load blob %s to detect language of %q: %w", blobHash, name, err)
	}

	reader, err := blob.Reader()
	if err != nil {
		return false, fmt.Errorf("open blob %s to detect language of %q: %w", blobHash, name, err)
	}
	defer reader.Close()

	buffer := make([]byte, 1024)

	bytesRead, err := reader.Read(buffer)
	if err != nil && (blob.Size != 0 || !errors.Is(err, io.EOF)) {
		return false, fmt.Errorf("read blob %s to detect language of %q: %w", blobHash, name, err)
	}

	if bytesRead < len(buffer) {
		buffer = buffer[:bytesRead]
	}

	lang := strings.ToLower(normalizeLanguage(name, enry.GetLanguage(path.Base(name), buffer)))

	return treediff.Languages[lang], nil
}

var _ = core.RegisterPipelineItem(&TreeDiff{})
