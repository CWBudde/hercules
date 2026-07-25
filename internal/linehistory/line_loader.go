package linehistory

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v2"

	"github.com/cwbudde/hercules/internal/core"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
)

// LineHistoryLoader allows to gather per-line history and statistics for a Git repository.
// It is a PipelineItem.
type LineHistoryLoader struct {
	files   map[FileId]fileInfo
	authors []string
	commits []commitInfo

	nextCommit int

	l core.Logger
}

type fileInfo struct {
	Name string
}

type commitInfo struct {
	Hash    plumbing.Hash
	Tick    core.TickNumber
	Author  core.AuthorId
	Changes []core.LineHistoryChange
}

var (
	errUnexpectedLineHistoryFieldCount = errors.New("unexpected number of fields in line history change")
	errInvalidLineHistoryYAML          = errors.New("line history YAML does not contain a valid LineDumper section")
	errLineHistoryIntegerRange         = errors.New("line history integer is out of range")
)

func (v fileInfo) ForEach(func(line, value int)) {
	panic("not implemented")
}

var _ core.FileIdResolver = loadedFileIdResolver{}

type loadedFileIdResolver struct {
	analyser *LineHistoryLoader
}

func (v loadedFileIdResolver) NameOf(id FileId) string {
	if v.analyser == nil {
		return ""
	}

	return v.analyser.files[id].Name
}

func (v loadedFileIdResolver) MergedWith(id FileId) (FileId, string, bool) {
	if v.analyser == nil {
		return 0, "", false
	}

	if f, ok := v.analyser.files[id]; ok {
		return id, f.Name, true
	}

	return 0, "", false
}

func (v loadedFileIdResolver) ForEachFile(callback func(id FileId, name string)) bool {
	if v.analyser == nil {
		return false
	}

	for id, file := range v.analyser.files {
		callback(id, file.Name)
	}

	return true
}

func (v loadedFileIdResolver) ScanFile(
	id FileId,
	callback func(line int, tick core.TickNumber, author core.AuthorId),
) bool {
	if v.analyser == nil {
		return false
	}

	file, ok := v.analyser.files[id]
	if !ok {
		return false
	}

	file.ForEach(func(line, value int) {
		author, tick := unpackPersonWithTick(value)
		callback(line, tick, author)
	})

	return true
}

var _ core.IdentityResolver = authorResolver{}

type authorResolver struct {
	identities *LineHistoryLoader
}

func (v authorResolver) MaxCount() int {
	if v.identities == nil {
		return 0
	}

	return len(v.identities.authors)
}

func (v authorResolver) Count() int {
	if v.identities == nil {
		return 0
	}

	return len(v.identities.authors)
}

func (v authorResolver) PrivateNameOf(id core.AuthorId) string {
	return v.FriendlyNameOf(id)
}

func (v authorResolver) FriendlyNameOf(id core.AuthorId) string {
	if id == core.AuthorMissing || id < 0 || v.identities == nil || int(id) >= len(v.identities.authors) {
		return core.AuthorMissingName
	}

	return v.identities.authors[id]
}

func (v authorResolver) ForEachIdentity(callback func(core.AuthorId, string)) bool {
	if v.identities == nil {
		return false
	}

	for id, name := range v.identities.authors {
		callback(core.AuthorId(id), name)
	}

	return true
}

func (v authorResolver) CopyNames(bool) []string {
	if v.identities == nil {
		return nil
	}

	return append([]string(nil), v.identities.authors...)
}

const (
	ConfigLinesLoadFrom = "LineHistory.LoadFrom"
)

func (analyser *LineHistoryLoader) Name() string {
	return "LineHistoryLoader"
}

func (analyser *LineHistoryLoader) Provides() []string {
	return []string{DependencyLineHistory, items.DependencyTick, identity.DependencyAuthor}
}

func (analyser *LineHistoryLoader) Requires() []string {
	return []string{}
}

func (*LineHistoryLoader) Features() []string {
	return []string{core.FeatureGitStub}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (analyser *LineHistoryLoader) ListConfigurationOptions() []core.ConfigurationOption {
	return []core.ConfigurationOption{
		{
			Name:        ConfigLinesLoadFrom,
			Description: "Temporary directory where to save the hibernated RBTree allocators.",
			Flag:        "history-line-load",
			Type:        core.PathConfigurationOption,
			Default:     "",
		},
	}
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (analyser *LineHistoryLoader) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		analyser.l = l
	} else {
		analyser.l = core.NewLogger()
	}

	if val, exists := facts[ConfigLinesLoadFrom].(string); exists {
		err := analyser.loadChangesFrom(val)
		if err != nil {
			return err
		}
	}

	facts[core.FactLineHistoryResolver] = loadedFileIdResolver{analyser}
	facts[core.FactIdentityResolver] = authorResolver{analyser}

	facts[core.ConfigPipelineCommits] = analyser.buildCommits()

	return nil
}

func (analyser *LineHistoryLoader) ConfigureUpstream(_ map[string]any) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (analyser *LineHistoryLoader) Initialize(*git.Repository) error {
	analyser.l = core.NewLogger()
	analyser.files = map[FileId]fileInfo{}
	analyser.nextCommit = 0

	return nil
}

func (analyser *LineHistoryLoader) Consume(map[string]any) (map[string]any, error) {
	var commit commitInfo
	if analyser.nextCommit < len(analyser.commits) {
		commit = analyser.commits[analyser.nextCommit]
		analyser.nextCommit++
	} else {
		commit.Author = core.AuthorMissing
	}

	result := map[string]any{
		DependencyLineHistory: core.LineHistoryChanges{
			Changes:  commit.Changes,
			Resolver: loadedFileIdResolver{analyser},
		},
		items.DependencyTick:      int(commit.Tick),
		identity.DependencyAuthor: int(commit.Author),
	}

	return result, nil
}

func (analyser *LineHistoryLoader) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(analyser, n)
}

func (analyser *LineHistoryLoader) Merge([]core.PipelineItem) {
	analyser.l.Critical("cant be merged")
}

func (analyser *LineHistoryLoader) loadChangesFrom(name string) error {
	input, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("open line history file %q: %w", name, err)
	}
	defer func() { _ = input.Close() }()

	return analyser.loadChangesFromYaml(yaml.NewDecoder(input))
}

var regexSplitBySpace = regexp.MustCompile(`\s+`)

func (analyser *LineHistoryLoader) loadChangesFromYaml(decoder *yaml.Decoder) error {
	var document yaml.MapSlice

	err := decoder.Decode(&document)
	if err != nil {
		return fmt.Errorf("decode line history YAML: %w", err)
	}

	lineDumper := yamlMapValue(document, "LineDumper")
	dumper, ok := lineDumper.(yaml.MapSlice)

	if !ok {
		return errInvalidLineHistoryYAML
	}

	commitsValue := yamlMapValue(dumper, "commits")
	commits, commitsOK := commitsValue.(yaml.MapSlice)

	authorsValue := yamlMapValue(dumper, "author_sequence")
	authors, authorsOK := authorsValue.([]any)

	filesValue := yamlMapValue(dumper, "file_sequence")
	files, filesOK := filesValue.(yaml.MapSlice)

	if !commitsOK || !authorsOK || !filesOK {
		return errInvalidLineHistoryYAML
	}

	analyser.authors = make([]string, 0, len(authors))
	for _, author := range authors {
		if value, ok := author.(string); ok {
			analyser.authors = append(analyser.authors, value)
		}
	}

	analyser.files = make(map[FileId]fileInfo, len(files))
	for _, item := range files {
		id, idOK := item.Key.(int)
		name, nameOK := item.Value.(string)

		if idOK && nameOK {
			if id < math.MinInt32 || id > math.MaxInt32 {
				return fmt.Errorf("%w: file ID %d", errLineHistoryIntegerRange, id)
			}

			analyser.files[FileId(id)] = fileInfo{Name: name}
		}
	}

	analyser.commits = make([]commitInfo, 0, len(commits))
	for _, yamlCommit := range commits {
		info, err := parseLineHistoryCommit(yamlCommit)
		if err != nil {
			return err
		}

		analyser.commits = append(analyser.commits, info)
	}

	return nil
}

func yamlMapValue(items yaml.MapSlice, key string) any {
	for _, item := range items {
		if item.Key == key {
			return item.Value
		}
	}

	return nil
}

func parseLineHistoryCommit(yamlCommit yaml.MapItem) (commitInfo, error) {
	hash, hashOK := yamlCommit.Key.(string)

	changes, changesOK := yamlCommit.Value.(string)
	if !hashOK || !changesOK {
		return commitInfo{}, fmt.Errorf("%w: invalid commit entry", errInvalidLineHistoryYAML)
	}

	info := commitInfo{Hash: plumbing.NewHash(hash)}

	scanner := bufio.NewScanner(strings.NewReader(changes))
	for scanner.Scan() {
		change, err := parseLineHistoryChange(scanner.Text())
		if err != nil {
			return commitInfo{}, err
		}

		info.Changes = append(info.Changes, change)
	}

	err := scanner.Err()
	if err != nil {
		return commitInfo{}, fmt.Errorf("scan line history changes: %w", err)
	}

	if len(info.Changes) == 0 {
		return commitInfo{}, fmt.Errorf("%w: commit %s has no changes", errInvalidLineHistoryYAML, hash)
	}

	info.Tick = info.Changes[0].CurrTick
	info.Author = info.Changes[0].CurrAuthor

	return info, nil
}

func parseLineHistoryChange(line string) (core.LineHistoryChange, error) {
	chunks := regexSplitBySpace.Split(line, -1)
	if len(chunks) != 6 {
		return core.LineHistoryChange{}, fmt.Errorf(
			"%w: got %d from %q", errUnexpectedLineHistoryFieldCount, len(chunks), line,
		)
	}

	values := make([]int, len(chunks))
	for chunkIndex, raw := range chunks {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return core.LineHistoryChange{}, fmt.Errorf(
				"unable to parse %q from %q: %w", raw, line, err,
			)
		}

		values[chunkIndex] = value
	}

	fileID, err := lineHistoryFileID(values[0])
	if err != nil {
		return core.LineHistoryChange{}, err
	}

	prevAuthor, err := intToAuthorID(values[1])
	if err != nil {
		return core.LineHistoryChange{}, err
	}

	prevTick, err := intToTickNumber(values[2])
	if err != nil {
		return core.LineHistoryChange{}, err
	}

	currAuthor, err := intToAuthorID(values[3])
	if err != nil {
		return core.LineHistoryChange{}, err
	}

	currTick, err := intToTickNumber(values[4])
	if err != nil {
		return core.LineHistoryChange{}, err
	}

	return core.LineHistoryChange{
		FileId: fileID, PrevAuthor: prevAuthor, PrevTick: prevTick,
		CurrAuthor: currAuthor, CurrTick: currTick, Delta: values[5],
	}, nil
}

func lineHistoryFileID(value int) (core.FileId, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("%w: file ID %d", errLineHistoryIntegerRange, value)
	}

	return core.FileId(value), nil
}

func (analyser *LineHistoryLoader) buildCommits() []*object.Commit {
	result := make([]*object.Commit, 0, len(analyser.commits))

	var parentHash []plumbing.Hash
	for _, commit := range analyser.commits {
		result = append(result, &object.Commit{
			Hash:         commit.Hash,
			Author:       object.Signature{},
			Committer:    object.Signature{},
			PGPSignature: "",
			Message:      "",
			TreeHash:     plumbing.Hash{},
			ParentHashes: parentHash,
		})
		parentHash = []plumbing.Hash{commit.Hash}
	}

	return result
}

var _ = core.RegisterPipelineItem(&LineHistoryLoader{})
