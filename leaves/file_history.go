package leaves

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
)

// FileHistoryAnalysis contains the intermediate state which is mutated by Consume(). It should implement
// LeafPipelineItem.
type FileHistoryAnalysis struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	files      map[string]*FileHistory
	lastCommit *object.Commit

	l core.Logger
}

// FileHistoryResult is returned by Finalize() and represents the analysis result.
type FileHistoryResult struct {
	Files map[string]FileHistory
}

// FileHistory is the gathered stats about a particular file.
type FileHistory struct {
	// Hashes is the list of commit hashes which changed this file.
	Hashes []plumbing.Hash
	// People is the mapping from developers to the number of lines they altered.
	People map[int]items.LineStats
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (history *FileHistoryAnalysis) Name() string {
	return "FileHistoryAnalysis"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (history *FileHistoryAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (history *FileHistoryAnalysis) Requires() []string {
	return []string{items.DependencyTreeChanges, items.DependencyLineStats, identity.DependencyAuthor}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (history *FileHistoryAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	return []core.ConfigurationOption{}
}

// Flag for the command line switch which enables this analysis.
func (history *FileHistoryAnalysis) Flag() string {
	return "file-history"
}

// Description returns the text which explains what the analysis is doing.
func (history *FileHistoryAnalysis) Description() string {
	return "Each file path is mapped to the list of commits which touch that file and the mapping " +
		"from involved developers to the corresponding line statistics: how many lines were added, " +
		"removed and changed throughout the whole history."
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (history *FileHistoryAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		history.l = l
	}

	return nil
}

func (*FileHistoryAnalysis) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (history *FileHistoryAnalysis) Initialize(repository *git.Repository) error {
	history.l = core.NewLogger()
	history.files = map[string]*FileHistory{}
	history.lastCommit = nil
	history.OneShotMergeProcessor.Initialize()

	return nil
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (history *FileHistoryAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	values, err := getFileHistoryDependencies(deps)
	if err != nil {
		return nil, err
	}

	history.lastCommit = values.commit
	commit := history.lastCommit.Hash

	for _, change := range values.changes {
		action, _ := change.Action()
		history.recordFileCommit(change, action, commit)
	}

	for changeEntry, stats := range values.lineStats {
		history.recordAuthorStats(changeEntry.Name, values.author, stats)
	}

	return noDependencies(), nil
}

type fileHistoryDependencies struct {
	commit    *object.Commit
	changes   object.Changes
	lineStats map[object.ChangeEntry]items.LineStats
	author    int
}

func getFileHistoryDependencies(deps map[string]any) (fileHistoryDependencies, error) {
	reader := factReader{facts: deps}
	values := fileHistoryDependencies{
		commit:    readFact[*object.Commit](&reader, core.DependencyCommit),
		changes:   readFact[object.Changes](&reader, items.DependencyTreeChanges),
		lineStats: readFact[map[object.ChangeEntry]items.LineStats](&reader, items.DependencyLineStats),
		author:    readFact[int](&reader, identity.DependencyAuthor),
	}

	return values, reader.err
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.

func (history *FileHistoryAnalysis) Finalize() any {
	files := map[string]FileHistory{}
	if history.lastCommit == nil {
		return FileHistoryResult{Files: files}
	}

	fileIter, err := history.lastCommit.Files()
	if err != nil {
		history.l.Errorf("Failed to iterate files of %s", history.lastCommit.Hash.String())

		return fmt.Errorf("list files for commit %s: %w", history.lastCommit.Hash.String(), err)
	}

	err = fileIter.ForEach(func(file *object.File) error {
		if fh := history.files[file.Name]; fh != nil {
			files[file.Name] = *fh
		}

		return nil
	})
	if err != nil {
		history.l.Errorf("Failed to iterate files of %s", history.lastCommit.Hash.String())

		return fmt.Errorf("iterate files for commit %s: %w", history.lastCommit.Hash.String(), err)
	}

	return FileHistoryResult{Files: files}
}

// Fork clones this PipelineItem.
func (history *FileHistoryAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(history, n)
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (history *FileHistoryAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	historyResult, err := requiredResult[FileHistoryResult](result)
	if err != nil {
		return fmt.Errorf("serialize file history: %w", err)
	}

	if binary {
		return history.serializeBinary(&historyResult, writer)
	}

	history.serializeText(&historyResult, writer)

	return nil
}

func (history *FileHistoryAnalysis) recordFileCommit(
	change *object.Change, action merkletrie.Action, commit plumbing.Hash,
) {
	switch action {
	case merkletrie.Insert:
		file := history.fileHistory(change.To.Name)
		file.Hashes = []plumbing.Hash{commit}
	case merkletrie.Delete:
		file := history.fileHistory(change.From.Name)
		file.Hashes = appendUniqueFileHistoryHashes(file.Hashes, commit)
	case merkletrie.Modify:
		var file *FileHistory
		if change.From.Name != change.To.Name {
			file = history.renameFileHistory(change.From.Name, change.To.Name)
		} else {
			file = history.fileHistory(change.To.Name)
		}

		file.Hashes = appendUniqueFileHistoryHashes(file.Hashes, commit)
	}
}

func (history *FileHistoryAnalysis) fileHistory(name string) *FileHistory {
	file := history.files[name]
	if file == nil {
		file = &FileHistory{}
		history.files[name] = file
	}

	return file
}

// renameFileHistory moves all history from source to destination. If destination already has
// history, its hashes stay first, the source hashes are appended, and per-author statistics are
// added together. This makes path collisions lossless and deterministic.
func (history *FileHistoryAnalysis) renameFileHistory(source, destination string) *FileHistory {
	sourceHistory := history.files[source]
	destinationHistory := history.files[destination]

	switch {
	case sourceHistory == nil && destinationHistory == nil:
		destinationHistory = &FileHistory{}
	case destinationHistory == nil:
		destinationHistory = sourceHistory
	case sourceHistory != nil && sourceHistory != destinationHistory:
		destinationHistory.Hashes = appendUniqueFileHistoryHashes(
			destinationHistory.Hashes, sourceHistory.Hashes...,
		)
		for author, stats := range sourceHistory.People {
			addAuthorStats(destinationHistory, author, stats)
		}
	}

	delete(history.files, source)
	history.files[destination] = destinationHistory

	return destinationHistory
}

func appendUniqueFileHistoryHashes(
	destination []plumbing.Hash, hashes ...plumbing.Hash,
) []plumbing.Hash {
	seen := make(map[plumbing.Hash]bool, len(destination)+len(hashes))
	for _, hash := range destination {
		seen[hash] = true
	}

	for _, hash := range hashes {
		if !seen[hash] {
			destination = append(destination, hash)
			seen[hash] = true
		}
	}

	return destination
}

func (history *FileHistoryAnalysis) recordAuthorStats(
	name string, author int, stats items.LineStats,
) {
	addAuthorStats(history.fileHistory(name), author, stats)
}

func addAuthorStats(file *FileHistory, author int, stats items.LineStats) {
	if file.People == nil {
		file.People = map[int]items.LineStats{}
	}

	oldStats := file.People[author]
	file.People[author] = items.LineStats{
		Added: oldStats.Added + stats.Added, Removed: oldStats.Removed + stats.Removed,
		Changed: oldStats.Changed + stats.Changed,
	}
}

func (history *FileHistoryAnalysis) serializeText(result *FileHistoryResult, writer io.Writer) {
	keys := make([]string, len(result.Files))

	i := 0
	for key := range result.Files {
		keys[i] = key
		i++
	}

	sort.Strings(keys)

	for _, key := range keys {
		fmt.Fprintf(writer, "  - %s:\n", key)
		file := result.Files[key]
		hashes := file.Hashes

		strhashes := make([]string, len(hashes))
		for i, hash := range hashes {
			strhashes[i] = "\"" + hash.String() + "\""
		}

		sort.Strings(strhashes)
		fmt.Fprintf(writer, "    commits: [%s]\n", strings.Join(strhashes, ","))

		strpeople := make([]string, 0, len(file.People))
		for key, val := range file.People {
			strpeople = append(strpeople, fmt.Sprintf("%d:[%d,%d,%d]", key, val.Added, val.Removed, val.Changed))
		}

		sort.Strings(strpeople)
		fmt.Fprintf(writer, "    people: {%s}\n", strings.Join(strpeople, ","))
	}
}

func (history *FileHistoryAnalysis) serializeBinary(result *FileHistoryResult, writer io.Writer) error {
	message := pb.FileHistoryResultMessage{
		Files: map[string]*pb.FileHistory{},
	}
	for key, vals := range result.Files {
		fileHistory := &pb.FileHistory{
			Commits:            make([]string, len(vals.Hashes)),
			ChangesByDeveloper: map[int32]*pb.LineStats{},
		}
		for i, hash := range vals.Hashes {
			fileHistory.Commits[i] = hash.String()
		}

		for key, val := range vals.People {
			developerID, err := intToProtoInt32(key, "file-history author")
			if err != nil {
				return err
			}

			stats, err := devLineStatsToProto(val.Added, val.Changed, val.Removed)
			if err != nil {
				return err
			}

			fileHistory.ChangesByDeveloper[developerID] = stats
		}

		message.Files[key] = fileHistory
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal file history result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write file history result: %w", err)
	}

	return nil
}

var _ = core.RegisterPipelineItem(&FileHistoryAnalysis{})
