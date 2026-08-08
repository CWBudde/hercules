package leaves

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/yaml"
)

var (
	errCodeChurnCounterOverflow  = errors.New("code churn counter overflow")
	errUnexpectedCodeChurnResult = errors.New("result is not a CodeChurnResult")
)

// CodeChurnAnalysis allows to gather the code churn statistics for a Git repository.
// It is a LeafPipelineItem.
type CodeChurnAnalysis struct {
	core.NoopMerger

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

	// Repository points to the analysed Git repository struct from go-git.
	repository *git.Repository

	// TickSize indicates the size of each time granule: day, hour, week, etc.
	tickSize time.Duration

	// code churns indexed by people
	codeChurns []personChurnStats
	lastTick   core.TickNumber
	hasTick    bool

	peopleResolver core.IdentityResolver
	fileResolver   core.FileIdResolver

	l core.Logger
}

type personChurnStats struct {
	files map[core.FileId]churnFileEntry
}

// CodeChurnResult is returned by CodeChurnAnalysis.Finalize().
type CodeChurnResult struct {
	Authors []CodeChurnAuthorResult

	people      []string
	tickSize    time.Duration
	sampling    int
	granularity int
}

// CodeChurnAuthorResult contains per-file churn stats for a single author.
type CodeChurnAuthorResult struct {
	Files map[string]CodeChurnFileResult
}

// CodeChurnFileResult contains churn stats for a single file.
type CodeChurnFileResult struct {
	InsertedLines int32
	OwnedLines    int32
	Memorability  float32
	Awareness     float32
	DeleteHistory map[int]sparseHistory
}

func (p *personChurnStats) getFileEntry(id core.FileId) churnFileEntry {
	var entry churnFileEntry
	if p.files != nil {
		entry = p.files[id]
		if entry.deleteHistory != nil {
			return entry
		}
	} else {
		p.files = map[core.FileId]churnFileEntry{}
	}

	entry.deleteHistory = map[core.AuthorId]sparseHistory{}

	return entry
}

func (result CodeChurnResult) GetIdentities() []string {
	return append([]string(nil), result.people...)
}

func (result CodeChurnResult) GetTickSize() time.Duration {
	return result.tickSize
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (analyser *CodeChurnAnalysis) Name() string {
	return "CodeChurn"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (analyser *CodeChurnAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (analyser *CodeChurnAnalysis) Requires() []string {
	return []string{linehistory.DependencyLineHistory, identity.DependencyAuthor}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (analyser *CodeChurnAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	return burndownSharedOptions()
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (analyser *CodeChurnAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		analyser.l = l
	} else {
		analyser.l = core.NewLogger()
	}

	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		analyser.tickSize = val
	}

	if val, exists := facts[ConfigBurndownGranularity].(int); exists {
		analyser.Granularity = val
	}

	if val, exists := facts[ConfigBurndownSampling].(int); exists {
		analyser.Sampling = val
	}

	if val, exists := facts[ConfigBurndownTrackFiles].(bool); exists {
		analyser.TrackFiles = val
	}

	if val, ok := facts[core.FactIdentityResolver].(core.IdentityResolver); ok {
		analyser.peopleResolver = val
	}

	return nil
}

func (analyser *CodeChurnAnalysis) ConfigureUpstream(_ map[string]any) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (analyser *CodeChurnAnalysis) Flag() string {
	return "codechurn"
}

// Description returns the text which explains what the analysis is doing.
func (analyser *CodeChurnAnalysis) Description() string {
	return "Experimental per-author, per-file code churn: lifetime inserted and currently owned " +
		"line counts, bounded awareness and memorability estimates, and deletion history."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (analyser *CodeChurnAnalysis) Initialize(repository *git.Repository) error {
	analyser.l = core.NewLogger()
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

	if analyser.peopleResolver == nil {
		analyser.peopleResolver = core.NewIdentityResolver(nil, nil)
	}

	peopleCount := analyser.peopleResolver.MaxCount()

	analyser.codeChurns = make([]personChurnStats, peopleCount)
	analyser.lastTick = 0
	analyser.hasTick = false

	return nil
}

func (analyser *CodeChurnAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(analyser, n)
}

// Consume runs this PipelineItem on the next commits data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (analyser *CodeChurnAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	changes, err := requiredFact[core.LineHistoryChanges](deps, linehistory.DependencyLineHistory)
	if err != nil {
		return nil, err
	}

	analyser.fileResolver = changes.Resolver
	peopleCount := analyser.peopleResolver.MaxCount()

	for _, change := range changes.Changes {
		err := analyser.consumeCodeChurnChange(change, peopleCount)
		if err != nil {
			return nil, err
		}
	}

	return noDependencies(), nil
}

func (analyser *CodeChurnAnalysis) consumeCodeChurnChange(
	change core.LineHistoryChange,
	peopleCount int,
) error {
	// File-deletion sentinels do not affect ownership, but their tick still
	// advances the repository-wide decay horizon used by Finalize.
	analyser.observeTick(change.CurrTick)

	if change.IsDelete() {
		return nil
	}

	lineDelta, err := intToProtoInt32(change.Delta, "code churn line delta")
	if err != nil {
		return err
	}

	if lineDelta == math.MinInt32 {
		return fmt.Errorf(
			"%w: deletion magnitude %d cannot be represented",
			errCodeChurnCounterOverflow, change.Delta,
		)
	}

	if int(change.PrevAuthor) >= peopleCount && change.PrevAuthor != core.AuthorMissing {
		change.PrevAuthor = core.AuthorMissing
	}

	if int(change.CurrAuthor) >= peopleCount && change.CurrAuthor != core.AuthorMissing {
		change.CurrAuthor = core.AuthorMissing
	}

	return analyser.updateAuthor(change, lineDelta)
}

type churnLines struct {
	inserted        int32
	deletedBySelf   int32
	deletedByOthers int32
	//	deletedAtOthers int32
}

type churnFileEntry struct {
	insertedLines int32
	ownedLines    int32
	memorability  float32
	awareness     float32
	lastScoreTick core.TickNumber
	hasScore      bool

	deleteHistory map[core.AuthorId]sparseHistory
}

// Finalize returns the result of the analysis. Further calls to Consume() are not expected.
func (analyser *CodeChurnAnalysis) Finalize() any {
	err := analyser.consumePendingLineHistory()
	if err != nil {
		return err
	}

	analyser.decayAllKnowledgeToLastTick()

	result := CodeChurnResult{
		Authors:     make([]CodeChurnAuthorResult, len(analyser.codeChurns)),
		people:      analyser.peopleResolver.CopyNames(false),
		tickSize:    analyser.tickSize,
		sampling:    analyser.Sampling,
		granularity: analyser.Granularity,
	}

	_, _ = fmt.Fprintln(os.Stderr)

	for pId, person := range analyser.codeChurns {
		inserted := int32(0)
		deletedBySelf := int32(0)
		deletedByOthers := int32(0)
		authorFiles := make(map[string]CodeChurnFileResult, len(person.files))

		for fileID, entry := range person.files {
			inserted += entry.insertedLines

			fileName := ""
			if analyser.fileResolver != nil {
				fileName = analyser.fileResolver.NameOf(fileID)
			}

			if fileName == "" {
				fileName = fmt.Sprintf("#%d", fileID)
			}

			authorFiles[fileName] = CodeChurnFileResult{
				InsertedLines: entry.insertedLines,
				OwnedLines:    entry.ownedLines,
				Memorability:  entry.memorability,
				Awareness:     entry.awareness,
				DeleteHistory: convertDeleteHistory(entry.deleteHistory),
			}
		}

		result.Authors[pId] = CodeChurnAuthorResult{Files: authorFiles}

		name := analyser.peopleResolver.FriendlyNameOf(core.AuthorId(pId))
		_, _ = fmt.Fprintf(os.Stderr, "%s (%d):\t\t%d\t%d\t%d = %d\n", name, pId, inserted, deletedBySelf, deletedByOthers,
			inserted+deletedBySelf+deletedByOthers)
	}

	_, _ = fmt.Fprintln(os.Stderr)

	return result
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (analyser *CodeChurnAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	churnResult, ok := result.(CodeChurnResult)
	if !ok {
		return fmt.Errorf("%w: '%v'", errUnexpectedCodeChurnResult, result)
	}

	if binary {
		return analyser.serializeBinary(&churnResult, writer)
	}

	analyser.serializeText(&churnResult, writer)

	return nil
}

// Deserialize converts the specified protobuf bytes to BurndownResult.
func (analyser *CodeChurnAnalysis) Deserialize(message []byte) (any, error) {
	payload := pb.CodeChurnAnalysisResults{}

	err := unmarshalAnalysis(message, &payload)
	if err != nil {
		return nil, fmt.Errorf("unmarshal code churn result: %w", err)
	}

	result := CodeChurnResult{
		Authors:     make([]CodeChurnAuthorResult, len(payload.GetAuthors())),
		people:      append([]string(nil), payload.GetPeople()...),
		tickSize:    time.Duration(payload.GetTickSize()),
		sampling:    int(payload.GetSampling()),
		granularity: int(payload.GetGranularity()),
	}
	for authorID, author := range payload.GetAuthors() {
		files := make(map[string]CodeChurnFileResult, len(author.GetFiles()))
		for _, file := range author.GetFiles() {
			files[file.GetFile()] = CodeChurnFileResult{
				InsertedLines: file.GetInsertedLines(),
				OwnedLines:    file.GetOwnedLines(),
				Memorability:  file.GetMemorability(),
				Awareness:     file.GetAwareness(),
				DeleteHistory: deserializeDeleteHistory(file.GetDeleteHistory()),
			}
		}

		result.Authors[authorID] = CodeChurnAuthorResult{Files: files}
	}

	return result, nil
}

// QualifyPaths prefixes every per-author file key with the repository it was observed in, so that
// mergeCodeChurnAuthors does not add up two repositories' files of the same name.
func (analyser *CodeChurnAnalysis) QualifyPaths(result any, repository string) any {
	churnResult, err := requiredResult[CodeChurnResult](result)
	if err != nil {
		return fmt.Errorf("qualify code churn paths: %w", err)
	}

	authors := make([]CodeChurnAuthorResult, len(churnResult.Authors))
	for index, author := range churnResult.Authors {
		if author.Files == nil {
			authors[index] = author

			continue
		}

		files := make(map[string]CodeChurnFileResult, len(author.Files))
		for path, file := range author.Files {
			files[core.QualifyRepositoryPath(repository, path)] = file
		}

		authors[index] = CodeChurnAuthorResult{Files: files}
	}

	churnResult.Authors = authors

	return churnResult
}

// MergeResults combines two BurndownResult-s together.
func (analyser *CodeChurnAnalysis) MergeResults(
	result1, result2 any, common1, common2 *core.CommonAnalysisResult,
) any {
	cr1, err := requiredResult[CodeChurnResult](result1)
	if err != nil {
		return fmt.Errorf("merge code churn first result: %w", err)
	}

	cr2, err := requiredResult[CodeChurnResult](result2)
	if err != nil {
		return fmt.Errorf("merge code churn second result: %w", err)
	}

	if cr1.tickSize != cr2.tickSize {
		panic("cannot merge CodeChurn results with different tick sizes")
	}

	mergedPeople, peopleIndex := mergeCodeChurnPeople(cr1.people, cr2.people)

	merged := CodeChurnResult{
		Authors:     make([]CodeChurnAuthorResult, len(mergedPeople)),
		people:      mergedPeople,
		tickSize:    cr1.tickSize,
		sampling:    maxInt(cr1.sampling, cr2.sampling),
		granularity: maxInt(cr1.granularity, cr2.granularity),
	}
	for i := range merged.Authors {
		merged.Authors[i].Files = map[string]CodeChurnFileResult{}
	}

	err = mergeCodeChurnAuthors(&merged, cr1, peopleIndex)
	if err != nil {
		return fmt.Errorf("merge code churn first authors: %w", err)
	}

	err = mergeCodeChurnAuthors(&merged, cr2, peopleIndex)
	if err != nil {
		return fmt.Errorf("merge code churn second authors: %w", err)
	}

	return merged
}

// consumePendingLineHistory accounts for merge-resolution deltas that were still buffered when
// the last commit was consumed - the case where the analysed HEAD is itself a merge commit.
func (analyser *CodeChurnAnalysis) consumePendingLineHistory() error {
	if analyser.fileResolver == nil {
		return nil
	}

	pending := linehistory.PendingChanges(analyser.fileResolver)
	if len(pending) == 0 {
		return nil
	}

	peopleCount := analyser.peopleResolver.MaxCount()
	for _, change := range pending {
		err := analyser.consumeCodeChurnChange(change, peopleCount)
		if err != nil {
			return err
		}
	}

	return nil
}

func (analyser *CodeChurnAnalysis) observeTick(tick core.TickNumber) {
	if !analyser.hasTick || tick > analyser.lastTick {
		analyser.lastTick = tick
		analyser.hasTick = true
	}
}

func churnLinesForChange(change core.LineHistoryChange, lineDelta int32) churnLines {
	var delta churnLines

	switch {
	case change.PrevAuthor != change.CurrAuthor && lineDelta < 0:
		delta.deletedByOthers -= lineDelta
	case change.PrevAuthor == change.CurrAuthor && lineDelta > 0:
		delta.inserted += lineDelta
	case change.PrevAuthor == change.CurrAuthor:
		delta.deletedBySelf -= lineDelta
	}

	return delta
}

func (analyser *CodeChurnAnalysis) updateAuthor(
	change core.LineHistoryChange, lineDelta int32,
) error {
	if change.PrevAuthor == core.AuthorMissing || change.Delta == 0 {
		return nil
	}

	fileEntry := analyser.codeChurns[change.PrevAuthor].getFileEntry(change.FileId)

	ownedLines, err := addCodeChurnInt32(fileEntry.ownedLines, lineDelta)
	if err != nil {
		return fmt.Errorf("update owned lines: %w", err)
	}

	if change.Delta > 0 {
		// PrevAuthor == CurrAuthor
		insertedLines, err := addCodeChurnInt32(fileEntry.insertedLines, lineDelta)
		if err != nil {
			return fmt.Errorf("update inserted lines: %w", err)
		}

		fileEntry.insertedLines = insertedLines
	}

	analyser.updateKnowledge(&fileEntry, change.CurrTick, churnLinesForChange(change, lineDelta))
	fileEntry.ownedLines = ownedLines

	if change.Delta < 0 {
		history := fileEntry.deleteHistory[change.CurrAuthor]
		if history == nil {
			history = sparseHistory{}
			fileEntry.deleteHistory[change.CurrAuthor] = history
		}

		history.updateDelta(int(change.PrevTick), int(change.CurrTick), change.Delta)
	}

	analyser.codeChurns[change.PrevAuthor].files[change.FileId] = fileEntry

	return nil
}

func addCodeChurnInt32(left, right int32) (int32, error) {
	sum := int64(left) + int64(right)
	if sum < math.MinInt32 || sum > math.MaxInt32 {
		return 0, fmt.Errorf(
			"%w: %d + %d is outside int32 bounds",
			errCodeChurnCounterOverflow, left, right,
		)
	}

	return int32(sum), nil
}

func mergeCodeChurnPeople(first, second []string) ([]string, map[string]int) {
	people := append([]string(nil), first...)

	index := make(map[string]int, len(people))
	for i, name := range people {
		index[name] = i
	}

	for _, name := range second {
		if _, exists := index[name]; !exists {
			index[name] = len(people)
			people = append(people, name)
		}
	}

	return people, index
}

func mergeCodeChurnAuthors(
	target *CodeChurnResult, source CodeChurnResult, peopleIndex map[string]int,
) error {
	for authorID, author := range source.Authors {
		if authorID >= len(source.people) {
			continue
		}

		targetFiles := target.Authors[peopleIndex[source.people[authorID]]].Files
		for fileName, stats := range author.Files {
			current, exists := targetFiles[fileName]
			if !exists {
				targetFiles[fileName] = cloneCodeChurnFileResult(stats)
				continue
			}

			insertedLines, err := addCodeChurnInt32(current.InsertedLines, stats.InsertedLines)
			if err != nil {
				return fmt.Errorf(
					"merge author %q file %q inserted lines: %w",
					source.people[authorID], fileName, err,
				)
			}

			ownedLines, err := addCodeChurnInt32(current.OwnedLines, stats.OwnedLines)
			if err != nil {
				return fmt.Errorf(
					"merge author %q file %q owned lines: %w",
					source.people[authorID], fileName, err,
				)
			}

			current.InsertedLines = insertedLines
			current.OwnedLines = ownedLines
			current.Memorability = maxFloat32(current.Memorability, stats.Memorability)
			current.Awareness = maxFloat32(current.Awareness, stats.Awareness)
			current.DeleteHistory = mergeDeleteHistory(current.DeleteHistory, stats.DeleteHistory)
			targetFiles[fileName] = current
		}
	}

	return nil
}

func (analyser *CodeChurnAnalysis) serializeText(result *CodeChurnResult, writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "  people:")

	for _, person := range result.people {
		_, _ = fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}

	_, _ = fmt.Fprintln(writer, "  tick_size:", int(result.tickSize.Seconds()))
	_, _ = fmt.Fprintln(writer, "  granularity:", result.granularity)
	_, _ = fmt.Fprintln(writer, "  sampling:", result.sampling)
	_, _ = fmt.Fprintln(writer, "  authors:")

	for authorID, author := range result.Authors {
		name := core.AuthorMissingName
		if authorID < len(result.people) {
			name = result.people[authorID]
		}

		_, _ = fmt.Fprintf(writer, "    %d:\n", authorID)
		_, _ = fmt.Fprintf(writer, "      name: %s\n", yaml.SafeString(name))
		_, _ = fmt.Fprintln(writer, "      files:")

		fileNames := sortedCodeChurnFiles(author.Files)
		for _, fileName := range fileNames {
			stats := author.Files[fileName]
			_, _ = fmt.Fprintf(writer, "        %s:\n", yaml.SafeString(fileName))
			_, _ = fmt.Fprintf(writer, "          inserted_lines: %d\n", stats.InsertedLines)
			_, _ = fmt.Fprintf(writer, "          owned_lines: %d\n", stats.OwnedLines)
			_, _ = fmt.Fprintf(writer, "          memorability: %.6f\n", stats.Memorability)
			_, _ = fmt.Fprintf(writer, "          awareness: %.6f\n", stats.Awareness)
			serializeCodeChurnDeleteHistoryText(stats.DeleteHistory, writer)
		}
	}
}

func serializeCodeChurnDeleteHistoryText(history map[int]sparseHistory, writer io.Writer) {
	if len(history) == 0 {
		_, _ = fmt.Fprintln(writer, "          delete_history: {}")

		return
	}

	_, _ = fmt.Fprintln(writer, "          delete_history:")

	for _, author := range sortedCodeChurnIntKeys(history) {
		_, _ = fmt.Fprintf(writer, "            %d:\n", author)

		for _, currentTick := range sortedCodeChurnIntKeys(history[author]) {
			_, _ = fmt.Fprintf(writer, "              %d:\n", currentTick)

			for _, previousTick := range sortedCodeChurnIntKeys(history[author][currentTick].deltas) {
				_, _ = fmt.Fprintf(
					writer,
					"                %d: %d\n",
					previousTick,
					history[author][currentTick].deltas[previousTick],
				)
			}
		}
	}
}

func (analyser *CodeChurnAnalysis) serializeBinary(result *CodeChurnResult, writer io.Writer) error {
	granularity, err := intToProtoInt32(result.granularity, "code churn granularity")
	if err != nil {
		return err
	}

	sampling, err := intToProtoInt32(result.sampling, "code churn sampling")
	if err != nil {
		return err
	}

	message := pb.CodeChurnAnalysisResults{
		Granularity: granularity,
		Sampling:    sampling,
		TickSize:    int64(result.tickSize),
		People:      append([]string(nil), result.people...),
		Authors:     make([]*pb.CodeChurnAuthorStat, len(result.Authors)),
	}

	for authorID, author := range result.Authors {
		fileNames := sortedCodeChurnFiles(author.Files)

		pbAuthor := &pb.CodeChurnAuthorStat{
			Files: make([]*pb.CodeChurnFileStat, 0, len(fileNames)),
		}
		for _, fileName := range fileNames {
			stats := author.Files[fileName]

			deleteHistory, err := serializeDeleteHistory(stats.DeleteHistory)
			if err != nil {
				return err
			}

			pbAuthor.Files = append(pbAuthor.Files, &pb.CodeChurnFileStat{
				File:          fileName,
				InsertedLines: stats.InsertedLines,
				OwnedLines:    stats.OwnedLines,
				Memorability:  stats.Memorability,
				Awareness:     stats.Awareness,
				DeleteHistory: deleteHistory,
			})
		}

		message.Authors[authorID] = pbAuthor
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal code churn result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write code churn result: %w", err)
	}

	return nil
}

func convertDeleteHistory(history map[core.AuthorId]sparseHistory) map[int]sparseHistory {
	if len(history) == 0 {
		return nil
	}

	result := make(map[int]sparseHistory, len(history))
	for author, entries := range history {
		result[int(author)] = cloneSparseHistory(entries)
	}

	return result
}

func cloneSparseHistory(history sparseHistory) sparseHistory {
	if len(history) == 0 {
		return nil
	}

	result := make(sparseHistory, len(history))
	for currentTick, entry := range history {
		deltas := make(map[int]int64, len(entry.deltas))
		maps.Copy(deltas, entry.deltas)

		result[currentTick] = sparseHistoryEntry{deltas: deltas}
	}

	return result
}

func cloneCodeChurnFileResult(stats CodeChurnFileResult) CodeChurnFileResult {
	return CodeChurnFileResult{
		InsertedLines: stats.InsertedLines,
		OwnedLines:    stats.OwnedLines,
		Memorability:  stats.Memorability,
		Awareness:     stats.Awareness,
		DeleteHistory: mergeDeleteHistory(nil, stats.DeleteHistory),
	}
}

func mergeDeleteHistory(left, right map[int]sparseHistory) map[int]sparseHistory {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}

	result := make(map[int]sparseHistory, len(left)+len(right))
	for author, history := range left {
		result[author] = cloneSparseHistory(history)
	}

	for author, history := range right {
		target := result[author]
		if target == nil {
			result[author] = cloneSparseHistory(history)
			continue
		}

		for currentTick, entry := range history {
			targetEntry := target[currentTick]
			if targetEntry.deltas == nil {
				targetEntry = newSparseHistoryEntry()
			}

			for previousTick, delta := range entry.deltas {
				targetEntry.deltas[previousTick] += delta
			}

			target[currentTick] = targetEntry
		}
	}

	return result
}

func serializeDeleteHistory(history map[int]sparseHistory) ([]*pb.CodeChurnDeleteHistory, error) {
	if len(history) == 0 {
		return nil, nil
	}

	authors := sortedCodeChurnIntKeys(history)

	result := make([]*pb.CodeChurnDeleteHistory, 0, len(authors))
	for _, author := range authors {
		for _, currentTick := range sortedCodeChurnIntKeys(history[author]) {
			entry, err := serializeDeleteHistoryEntry(author, currentTick, history[author][currentTick])
			if err != nil {
				return nil, err
			}

			result = append(result, entry)
		}
	}

	return result, nil
}

func sortedCodeChurnIntKeys[V any](values map[int]V) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	sort.Ints(keys)

	return keys
}

func serializeDeleteHistoryEntry(
	author int,
	currentTick int,
	entry sparseHistoryEntry,
) (*pb.CodeChurnDeleteHistory, error) {
	authorID, err := intToProtoInt32(author, "code churn delete-history author")
	if err != nil {
		return nil, err
	}

	pbCurrentTick, err := intToProtoInt32(currentTick, "code churn delete-history current tick")
	if err != nil {
		return nil, err
	}

	result := &pb.CodeChurnDeleteHistory{
		Author:      authorID,
		CurrentTick: pbCurrentTick,
		Entries:     make([]*pb.CodeChurnSparseHistoryEntry, 0, len(entry.deltas)),
	}
	for _, previousTick := range sortedCodeChurnIntKeys(entry.deltas) {
		pbPreviousTick, conversionErr := intToProtoInt32(
			previousTick, "code churn delete-history previous tick",
		)
		if conversionErr != nil {
			return nil, conversionErr
		}

		result.Entries = append(result.Entries, &pb.CodeChurnSparseHistoryEntry{
			PreviousTick: pbPreviousTick,
			Delta:        entry.deltas[previousTick],
		})
	}

	return result, nil
}

func deserializeDeleteHistory(entries []*pb.CodeChurnDeleteHistory) map[int]sparseHistory {
	if len(entries) == 0 {
		return nil
	}

	result := map[int]sparseHistory{}

	for _, entry := range entries {
		author := int(entry.GetAuthor())

		history := result[author]
		if history == nil {
			history = sparseHistory{}
			result[author] = history
		}

		deltas := make(map[int]int64, len(entry.GetEntries()))
		for _, delta := range entry.GetEntries() {
			deltas[int(delta.GetPreviousTick())] = delta.GetDelta()
		}

		history[int(entry.GetCurrentTick())] = sparseHistoryEntry{deltas: deltas}
	}

	return result
}

func sortedCodeChurnFiles(files map[string]CodeChurnFileResult) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}

	return right
}

func maxFloat32(left, right float32) float32 {
	if left > right {
		return left
	}

	return right
}

const (
	// codeChurnAwarenessHalfLifeTicks controls short-term familiarity decay.
	codeChurnAwarenessHalfLifeTicks = 30.0
	// codeChurnMemorabilityHalfLifeTicks controls long-term familiarity decay.
	codeChurnMemorabilityHalfLifeTicks = 180.0
)

func decayCodeChurnScore(score float64, elapsed core.TickNumber, halfLife float64) float64 {
	if elapsed <= 0 {
		return clampCodeChurnScore(score)
	}

	return clampCodeChurnScore(score * math.Exp2(-float64(elapsed)/halfLife))
}

func clampCodeChurnScore(score float64) float64 {
	return math.Max(0, math.Min(1, score))
}

func codeChurnLineShare(lines int32, population float64) float64 {
	if lines <= 0 {
		return 0
	}

	return math.Min(1, float64(lines)/math.Max(1, population))
}

func decayFileKnowledge(entry *churnFileEntry, tick core.TickNumber) {
	if !entry.hasScore || tick <= entry.lastScoreTick {
		return
	}

	elapsed := tick - entry.lastScoreTick
	entry.awareness = float32(decayCodeChurnScore(
		float64(entry.awareness), elapsed, codeChurnAwarenessHalfLifeTicks,
	))
	entry.memorability = float32(decayCodeChurnScore(
		float64(entry.memorability), elapsed, codeChurnMemorabilityHalfLifeTicks,
	))
	entry.lastScoreTick = tick
}

// updateKnowledge applies the experimental Code Churn equations. Scores first decay to tick.
// Insertions and self-deletions reinforce familiarity by their share of the author's line
// population; deletions by another author then reduce both scores by the removed ownership share.
func (analyser *CodeChurnAnalysis) updateKnowledge(
	entry *churnFileEntry, tick core.TickNumber, delta churnLines,
) {
	decayFileKnowledge(entry, tick)

	ownedBefore := math.Max(0, float64(entry.ownedLines))
	reinforcedLines := delta.inserted + delta.deletedBySelf
	reinforcement := codeChurnLineShare(reinforcedLines, ownedBefore+float64(delta.inserted))
	disruption := codeChurnLineShare(delta.deletedByOthers, ownedBefore)

	awareness := float64(entry.awareness)
	memorability := float64(entry.memorability)
	awareness = (awareness + (1-awareness)*reinforcement) * (1 - disruption)
	memorability = (memorability + (1-memorability)*reinforcement) * (1 - disruption)

	entry.awareness = float32(clampCodeChurnScore(awareness))

	entry.memorability = float32(clampCodeChurnScore(memorability))

	if !entry.hasScore || tick > entry.lastScoreTick {
		entry.lastScoreTick = tick
	}

	entry.hasScore = true
}

func (analyser *CodeChurnAnalysis) decayAllKnowledgeToLastTick() {
	if !analyser.hasTick {
		return
	}

	for authorID := range analyser.codeChurns {
		for fileID, entry := range analyser.codeChurns[authorID].files {
			decayFileKnowledge(&entry, analyser.lastTick)
			analyser.codeChurns[authorID].files[fileID] = entry
		}
	}
}

var _ = core.RegisterPipelineItem(&CodeChurnAnalysis{})
