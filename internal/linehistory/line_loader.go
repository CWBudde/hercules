package linehistory

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"slices"
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
	Name  string
	Lines []lineRun
}

type lineRun struct {
	Author core.AuthorId
	Tick   core.TickNumber
	Count  int
}

type lineOwner struct {
	Author core.AuthorId
	Tick   core.TickNumber
}

type replayFile struct {
	owners map[lineOwner]int
	order  []lineOwner
}

type commitInfo struct {
	Hash    plumbing.Hash
	Tick    core.TickNumber
	Author  core.AuthorId
	Changes []core.LineHistoryChange
}

var (
	// ErrLineHistoryMalformed classifies structurally invalid LineDumper YAML.
	ErrLineHistoryMalformed = errors.New("line history YAML is malformed")
	// ErrLineHistoryRange classifies file, tick, author, and count values outside supported ranges.
	ErrLineHistoryRange = errors.New("line history value is out of range")
	// ErrLineHistoryState classifies a valid-looking change stream that cannot reconstruct file state.
	ErrLineHistoryState = errors.New("line history changes do not form a valid file state")

	errUnexpectedLineHistoryFieldCount = fmt.Errorf(
		"%w: unexpected number of fields in line history change",
		ErrLineHistoryMalformed,
	)
)

func (v fileInfo) ForEach(callback func(line int, tick core.TickNumber, author core.AuthorId)) {
	line := 0

	for _, run := range v.Lines {
		for range run.Count {
			callback(line, run.Tick, run.Author)
			line++
		}
	}
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

	ids := make([]FileId, 0, len(v.analyser.files))
	for id := range v.analyser.files {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	for _, id := range ids {
		file := v.analyser.files[id]
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

	file.ForEach(callback)

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
			Description: "Path to a YAML LineDumper export to replay.",
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
	if analyser.l == nil {
		analyser.l = core.NewLogger()
	}

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
	document, err := decodeLineHistoryDocument(decoder)
	if err != nil {
		return err
	}

	loadedAuthors, loadedFiles, loadedCommits, err := loadLineHistoryDocument(document)
	if err != nil {
		return err
	}

	err = reconstructFileStates(loadedFiles, loadedCommits)
	if err != nil {
		return err
	}

	analyser.authors = loadedAuthors
	analyser.files = loadedFiles
	analyser.commits = loadedCommits

	return nil
}

func decodeLineHistoryDocument(decoder *yaml.Decoder) (yaml.MapSlice, error) {
	var document yaml.MapSlice

	err := decoder.Decode(&document)
	if err != nil {
		return nil, fmt.Errorf("%w: decode YAML: %w", ErrLineHistoryMalformed, err)
	}

	var trailing any
	switch err := decoder.Decode(&trailing); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return nil, fmt.Errorf("%w: decode trailing YAML: %w", ErrLineHistoryMalformed, err)
	default:
		return nil, fmt.Errorf("%w: multiple YAML documents", ErrLineHistoryMalformed)
	}

	return document, nil
}

func loadLineHistoryDocument(document yaml.MapSlice) ([]string, map[FileId]fileInfo, []commitInfo, error) {
	dumper, ok := yamlMapValue(document, "LineDumper").(yaml.MapSlice)
	if !ok {
		return nil, nil, nil, ErrLineHistoryMalformed
	}

	commits, commitsOK := yamlMapValue(dumper, "commits").(yaml.MapSlice)
	authors, authorsOK := yamlMapValue(dumper, "author_sequence").([]any)

	files, filesOK := yamlMapValue(dumper, "file_sequence").(yaml.MapSlice)
	if !commitsOK || !authorsOK || !filesOK {
		return nil, nil, nil, ErrLineHistoryMalformed
	}

	loadedAuthors, err := loadAuthors(authors)
	if err != nil {
		return nil, nil, nil, err
	}

	loadedFiles, err := loadFiles(files)
	if err != nil {
		return nil, nil, nil, err
	}

	loadedCommits, err := loadCommits(commits, loadedAuthors)
	if err != nil {
		return nil, nil, nil, err
	}

	return loadedAuthors, loadedFiles, loadedCommits, nil
}

func loadAuthors(authors []any) ([]string, error) {
	result := make([]string, len(authors))
	for index, author := range authors {
		value, ok := author.(string)
		if !ok {
			return nil, fmt.Errorf("%w: author %d has type %T", ErrLineHistoryMalformed, index, author)
		}

		result[index] = value
	}

	return result, nil
}

func loadFiles(files yaml.MapSlice) (map[FileId]fileInfo, error) {
	result := make(map[FileId]fileInfo, len(files))
	for _, item := range files {
		id, idOK := item.Key.(int)
		name, nameOK := item.Value.(string)

		if !idOK || !nameOK {
			return nil, fmt.Errorf(
				"%w: invalid file entry with key type %T and value type %T",
				ErrLineHistoryMalformed, item.Key, item.Value,
			)
		}

		if id <= 0 || id > math.MaxInt32 {
			return nil, fmt.Errorf("%w: file ID %d", ErrLineHistoryRange, id)
		}

		if name == "" {
			return nil, fmt.Errorf("%w: file ID %d has an empty name", ErrLineHistoryMalformed, id)
		}

		fileID := FileId(id)
		if _, exists := result[fileID]; exists {
			return nil, fmt.Errorf("%w: duplicate file ID %d", ErrLineHistoryMalformed, id)
		}

		result[fileID] = fileInfo{Name: name}
	}

	return result, nil
}

func loadCommits(commits yaml.MapSlice, authors []string) ([]commitInfo, error) {
	result := make([]commitInfo, 0, len(commits))
	seen := make(map[plumbing.Hash]int, len(commits))

	for index, yamlCommit := range commits {
		info, err := parseLineHistoryCommit(yamlCommit, len(authors))
		if err != nil {
			return nil, fmt.Errorf("commit %d: %w", index, err)
		}

		if previous, exists := seen[info.Hash]; exists {
			return nil, fmt.Errorf(
				"%w: duplicate commit %s at positions %d and %d",
				ErrLineHistoryMalformed, info.Hash, previous, index,
			)
		}

		seen[info.Hash] = index

		result = append(result, info)
	}

	return result, nil
}

func yamlMapValue(items yaml.MapSlice, key string) any {
	for _, item := range items {
		if item.Key == key {
			return item.Value
		}
	}

	return nil
}

func parseLineHistoryCommit(yamlCommit yaml.MapItem, authorCount int) (commitInfo, error) {
	hash, changes, err := lineHistoryCommitFields(yamlCommit)
	if err != nil {
		return commitInfo{}, err
	}

	info := commitInfo{Hash: plumbing.NewHash(hash)}

	info.Changes, err = parseLineHistoryChanges(changes, authorCount)
	if err != nil {
		return commitInfo{}, err
	}

	if len(info.Changes) == 0 {
		return commitInfo{}, fmt.Errorf("%w: commit %s has no changes", ErrLineHistoryMalformed, hash)
	}

	info.Tick = info.Changes[0].CurrTick
	info.Author = info.Changes[0].CurrAuthor

	err = validateCommitOwnership(info)
	if err != nil {
		return commitInfo{}, err
	}

	return info, nil
}

func lineHistoryCommitFields(yamlCommit yaml.MapItem) (string, string, error) {
	hash, hashOK := yamlCommit.Key.(string)

	changes, changesOK := yamlCommit.Value.(string)
	if !hashOK || !changesOK {
		return "", "", fmt.Errorf("%w: invalid commit entry", ErrLineHistoryMalformed)
	}

	if !plumbing.IsHash(hash) {
		return "", "", fmt.Errorf("%w: invalid commit hash %q", ErrLineHistoryMalformed, hash)
	}

	return hash, changes, nil
}

func parseLineHistoryChanges(changes string, authorCount int) ([]core.LineHistoryChange, error) {
	var result []core.LineHistoryChange
	scanner := bufio.NewScanner(strings.NewReader(changes))

	for scanner.Scan() {
		change, err := parseLineHistoryChange(scanner.Text())
		if err != nil {
			return nil, err
		}

		err = validateLineHistoryChange(change, authorCount)
		if err != nil {
			return nil, err
		}

		result = append(result, change)
	}

	err := scanner.Err()
	if err != nil {
		return nil, fmt.Errorf("%w: scan changes: %w", ErrLineHistoryMalformed, err)
	}

	return result, nil
}

func validateCommitOwnership(info commitInfo) error {
	for index, change := range info.Changes[1:] {
		if change.CurrTick == info.Tick && change.CurrAuthor == info.Author {
			continue
		}

		return fmt.Errorf(
			"%w: change %d has author/tick %d/%d, expected %d/%d",
			ErrLineHistoryMalformed, index+1, change.CurrAuthor, change.CurrTick,
			info.Author, info.Tick,
		)
	}

	return nil
}

func validateLineHistoryChange(change core.LineHistoryChange, authorCount int) error {
	if change.FileId <= 0 {
		return fmt.Errorf("%w: file ID %d", ErrLineHistoryRange, change.FileId)
	}

	if change.CurrTick < 0 || change.CurrTick >= TreeMergeMark {
		return fmt.Errorf("%w: current tick %d", ErrLineHistoryRange, change.CurrTick)
	}

	if change.IsDelete() {
		if !validLoadedAuthor(change.CurrAuthor, authorCount) {
			return fmt.Errorf("%w: current author %d", ErrLineHistoryRange, change.CurrAuthor)
		}

		return nil
	}

	return validateRegularLineHistoryChange(change, authorCount)
}

func validateRegularLineHistoryChange(change core.LineHistoryChange, authorCount int) error {
	if change.Delta == math.MinInt {
		return fmt.Errorf("%w: invalid deletion marker", ErrLineHistoryMalformed)
	}

	if change.Delta == 0 {
		return fmt.Errorf("%w: zero line delta", ErrLineHistoryMalformed)
	}

	if change.PrevTick < 0 || change.PrevTick >= TreeMergeMark {
		return fmt.Errorf("%w: previous tick %d", ErrLineHistoryRange, change.PrevTick)
	}

	if !validLoadedAuthor(change.PrevAuthor, authorCount) {
		return fmt.Errorf("%w: previous author %d", ErrLineHistoryRange, change.PrevAuthor)
	}

	if !validLoadedAuthor(change.CurrAuthor, authorCount) {
		return fmt.Errorf("%w: current author %d", ErrLineHistoryRange, change.CurrAuthor)
	}

	if change.Delta > 0 &&
		(change.PrevAuthor != change.CurrAuthor || change.PrevTick != change.CurrTick) {
		return fmt.Errorf(
			"%w: inserted lines have different previous and current ownership",
			ErrLineHistoryState,
		)
	}

	return nil
}

func validLoadedAuthor(author core.AuthorId, count int) bool {
	return author == core.AuthorMissing || author >= 0 && int(author) < count
}

func reconstructFileStates(files map[FileId]fileInfo, commits []commitInfo) error {
	replayed := map[FileId]*replayFile{}

	err := replayLineHistoryCommits(replayed, commits)
	if err != nil {
		return err
	}

	materializeReplayFiles(files, replayed)

	return nil
}

func replayLineHistoryCommits(replayed map[FileId]*replayFile, commits []commitInfo) error {
	for commitIndex, commit := range commits {
		for changeIndex, change := range commit.Changes {
			err := replayLineHistoryChange(replayed, change)
			if err != nil {
				return fmt.Errorf(
					"commit %d (%s), change %d: %w",
					commitIndex, commit.Hash, changeIndex, err,
				)
			}
		}
	}

	return nil
}

func materializeReplayFiles(files map[FileId]fileInfo, replayed map[FileId]*replayFile) {
	for id, file := range files {
		state := replayed[id]
		if state != nil {
			file.Lines = make([]lineRun, 0, len(state.order))
			for _, owner := range state.order {
				if count := state.owners[owner]; count > 0 {
					file.Lines = append(file.Lines, lineRun{
						Author: owner.Author,
						Tick:   owner.Tick,
						Count:  count,
					})
				}
			}
		}

		files[id] = file
	}
}

func replayLineHistoryChange(files map[FileId]*replayFile, change core.LineHistoryChange) error {
	if change.IsDelete() {
		delete(files, change.FileId)

		return nil
	}

	file := files[change.FileId]
	if file == nil {
		file = &replayFile{owners: map[lineOwner]int{}}
		files[change.FileId] = file
	}

	if change.Delta > 0 {
		owner := lineOwner{Author: change.CurrAuthor, Tick: change.CurrTick}
		if _, exists := file.owners[owner]; !exists {
			file.order = append(file.order, owner)
		}

		if file.owners[owner] > math.MaxInt-change.Delta {
			return fmt.Errorf("%w: file %d line count overflow", ErrLineHistoryState, change.FileId)
		}

		file.owners[owner] += change.Delta

		return nil
	}

	owner := lineOwner{Author: change.PrevAuthor, Tick: change.PrevTick}
	removed := -change.Delta
	available := file.owners[owner]

	if available < removed {
		return fmt.Errorf(
			"%w: file %d removes %d lines owned by author %d at tick %d, only %d exist",
			ErrLineHistoryState, change.FileId, removed,
			change.PrevAuthor, change.PrevTick, available,
		)
	}

	file.owners[owner] = available - removed

	return nil
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
				"%w: unable to parse %q from %q: %w", ErrLineHistoryMalformed, raw, line, err,
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
		return 0, fmt.Errorf("%w: file ID %d", ErrLineHistoryRange, value)
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
