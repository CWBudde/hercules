package identity

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/pkg/errors"

	"github.com/cwbudde/hercules/internal/core"
)

var (
	errInvalidNextMergeDependency = errors.New("invalid next merge dependency")
	errMergeAuthorRange           = errors.New("merge author ID is out of range")
)

// StoryDetector determines the author of a commit. Same person can commit under different
// signatures, and we apply some heuristics to merge those together.
// It is a PipelineItem.
type StoryDetector struct {
	core.NoopMerger

	// PeopleDict maps email || name  -> developer id
	MergeHashDict map[plumbing.Hash]int
	// ReversedPeopleDict maps developer id -> description
	MergeNames []string

	mergeNameCount  int
	expandMergeDict bool

	l core.Logger
}

const (
	// FactStoryDetectorMergeDict is the name of the fact which is inserted in
	// StoryDetector.Configure(). It corresponds to StoryDetector.ReversedPeopleDict -
	// the mapping from the author indices to the main signature.
	FactStoryDetectorMergeDict = "StoryDetector.MergeDict"
	// ConfigStoryDetectorMergeDictPath is the name of the configuration option
	// (StoryDetector.Configure()) which allows to set the external PeopleDict mapping from a file.
	ConfigStoryDetectorMergeDictPath = "StoryDetector.MergeDictPath"
)

var _ core.IdentityResolver = storyResolver{}

type storyResolver struct {
	identities *StoryDetector
}

func (v storyResolver) MaxCount() int {
	if v.identities == nil {
		return 0
	}

	return v.identities.mergeNameCount
}

func (v storyResolver) Count() int {
	if v.identities == nil {
		return 0
	}

	return len(v.identities.MergeNames)
}

func (v storyResolver) PrivateNameOf(id core.AuthorId) string {
	return v.FriendlyNameOf(id)
}

func (v storyResolver) FriendlyNameOf(id core.AuthorId) string {
	if id == core.AuthorMissing || id < 0 || v.identities == nil || int(id) >= len(v.identities.MergeNames) {
		return core.AuthorMissingName
	}

	return v.identities.MergeNames[id]
}

func (v storyResolver) ForEachIdentity(callback func(core.AuthorId, string)) bool {
	if v.identities == nil {
		return false
	}

	for id, name := range v.identities.MergeNames {
		callback(core.AuthorId(id), name)
	}

	return true
}

func (v storyResolver) CopyNames(bool) []string {
	if v.identities == nil {
		return nil
	}

	return append([]string(nil), v.identities.MergeNames...)
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (detector *StoryDetector) Name() string {
	return "StoryDetector"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (detector *StoryDetector) Provides() []string {
	return []string{DependencyAuthor}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (detector *StoryDetector) Requires() []string {
	return []string{}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (detector *StoryDetector) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name:        ConfigStoryDetectorMergeDictPath,
			Description: "Path to the file with developer -> name|email associations.",
			Flag:        "story-dict",
			Type:        core.PathConfigurationOption,
			Default:     "",
		},
	}

	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (detector *StoryDetector) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		detector.l = l
	} else {
		detector.l = core.NewLogger()
	}

	detector.expandMergeDict = false
	err := configureMergeTracks(detector, facts)
	if err != nil {
		return err
	}

	var resolver core.IdentityResolver = storyResolver{detector}
	facts[core.FactIdentityResolver] = resolver

	return nil
}

func configureMergeTracks(detector *StoryDetector, facts map[string]any) error {
	if val, exists := facts[FactStoryDetectorMergeDict].(map[plumbing.Hash]string); exists {
		detector.MergeHashDict, detector.MergeNames = splitMergeDict(val)
		detector.mergeNameCount = len(detector.MergeNames)

		return nil
	}

	if dictPath, ok := facts[ConfigStoryDetectorMergeDictPath].(string); ok && dictPath != "" {
		err := detector.LoadMergeDict(dictPath)
		if err != nil {
			return errors.Errorf("failed to load %s: %v", dictPath, err)
		}

		detector.mergeNameCount = len(detector.MergeNames)

		return nil
	}

	if mergeCount, ok := facts[core.FactMergeHashCount].(int); ok {
		detector.MergeHashDict = make(map[plumbing.Hash]int, mergeCount)
		detector.mergeNameCount = mergeCount
		detector.expandMergeDict = true

		return nil
	}

	return errors.Errorf("merge tracks are not available")
}

func splitMergeDict(dict map[plumbing.Hash]string) (map[plumbing.Hash]int, []string) {
	uniqueNames := map[string]int{}

	hashDict := make(map[plumbing.Hash]int, len(dict))
	for hash, name := range dict {
		id, ok := uniqueNames[name]
		if !ok {
			id = len(uniqueNames)
			uniqueNames[name] = id
		}

		hashDict[hash] = id
	}

	names := make([]string, len(uniqueNames))
	for name, id := range uniqueNames {
		names[id] = name
	}

	return hashDict, names
}

func (*StoryDetector) ConfigureUpstream(map[string]any) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (detector *StoryDetector) Initialize(*git.Repository) error {
	detector.l = core.NewLogger()
	return nil
}

func (detector *StoryDetector) Features() []string {
	return []string{core.FeatureMergeTracks, core.FeatureGitCommits}
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (detector *StoryDetector) Consume(deps map[string]any) (map[string]any, error) {
	commit, ok := deps[core.DependencyNextMerge].(*object.Commit)
	if !ok {
		return nil, errInvalidNextMergeDependency
	}

	author, err := detector.putAuthorId(commit)
	if err != nil {
		return nil, err
	}

	return map[string]any{DependencyAuthor: int(author)}, nil
}

// Fork clones this PipelineItem.
func (detector *StoryDetector) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(detector, n)
}

// LoadMergeDict loads author signatures from a text file.
// The format is one signature per line, and the signature consists of several
// keys separated by "|". The first key is the main one and used to reference all the rest.
func (detector *StoryDetector) LoadMergeDict(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open merge dictionary: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	dict := make(map[plumbing.Hash]int)
	var reverseDict []string

	for scanner.Scan() {
		id := len(reverseDict)

		name, err := parseMergeDictLine(scanner.Text(), id, dict, reverseDict)
		if err != nil {
			return err
		}

		reverseDict = append(reverseDict, name)
	}

	detector.MergeHashDict = dict
	detector.MergeNames = reverseDict

	return nil
}

func parseMergeDictLine(
	line string,
	id int,
	dict map[plumbing.Hash]int,
	names []string,
) (string, error) {
	values := strings.Split(line, "|")

	nameIndex, err := addMergeHashes(values, id, dict, names)
	if err != nil {
		return "", err
	}

	if nameIndex < len(values) {
		return values[nameIndex], nil
	}

	return fmt.Sprintf("Merge #%d", id), nil
}

func addMergeHashes(
	values []string,
	id int,
	dict map[plumbing.Hash]int,
	names []string,
) (int, error) {
	for valueIndex, value := range values {
		key, err := decodeMergeHash(value)
		if err != nil {
			if valueIndex == len(values)-1 {
				return valueIndex, nil
			}

			return 0, err
		}

		if existingID, found := dict[key]; found {
			return 0, errors.Errorf("ambigous hash: %s = (%d) %s", value, existingID, names[existingID])
		}

		dict[key] = id
	}

	return len(values), nil
}

func decodeMergeHash(value string) (plumbing.Hash, error) {
	var key plumbing.Hash

	n, err := hex.Decode(key[:], []byte(value))
	if err == nil && n != len(key) {
		err = errors.Errorf("hash must be of %d bytes: %s", len(key), value)
	}

	return key, err
}

func (detector *StoryDetector) putAuthorId(nextMerge *object.Commit) (core.AuthorId, error) {
	if nextMerge == nil {
		return core.AuthorMissing, nil
	}

	if author, ok := detector.MergeHashDict[nextMerge.Hash]; ok {
		if author < math.MinInt32 || author > math.MaxInt32 {
			return core.AuthorMissing, fmt.Errorf("%w: %d", errMergeAuthorRange, author)
		}

		return core.AuthorId(author), nil
	}

	if !detector.expandMergeDict {
		return core.AuthorMissing, nil
	}

	if len(detector.MergeNames) >= detector.mergeNameCount {
		return core.AuthorMissing, errors.New("number of merge hashes exceeded")
	}

	mergeIndex := len(detector.MergeNames)
	if mergeIndex > math.MaxInt32 {
		return core.AuthorMissing, fmt.Errorf("%w: %d", errMergeAuthorRange, mergeIndex)
	}

	name := detector.makeMergeName(mergeIndex, nextMerge)
	detector.MergeHashDict[nextMerge.Hash] = mergeIndex
	detector.MergeNames = append(detector.MergeNames, name)

	return core.AuthorId(mergeIndex), nil
}

func (detector *StoryDetector) makeMergeName(index int, merge *object.Commit) string {
	return fmt.Sprintf("Merge #%d (%s) at %s",
		index, merge.Hash.String()[:7], merge.Author.When.Format(time.RFC822Z))
}

var _ = core.RegisterPipelineItem(&StoryDetector{})
