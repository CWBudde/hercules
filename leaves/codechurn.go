package leaves

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/linehistory"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/yaml"
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
	codeChurns  []personChurnStats
	churnDeltas map[churnDeltaKey]churnDelta

	peopleResolver core.IdentityResolver
	fileResolver   core.FileIdResolver

	l core.Logger
}

type churnDeletedFileEntry struct {
	fileId    core.FileId
	deletedAt int
	entry     churnFileEntry
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

func (p *personChurnStats) getFileEntry(id core.FileId) (entry churnFileEntry) {
	if p.files != nil {
		entry = p.files[id]
		if entry.deleteHistory != nil {
			return
		}
	} else {
		p.files = map[core.FileId]churnFileEntry{}
	}
	entry.deleteHistory = map[core.AuthorId]sparseHistory{}
	return
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
	return BurndownSharedOptions[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (analyser *CodeChurnAnalysis) Configure(facts map[string]interface{}) error {
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

func (analyser *CodeChurnAnalysis) ConfigureUpstream(_ map[string]interface{}) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (analyser *CodeChurnAnalysis) Flag() string {
	return "codechurn"
}

// Description returns the text which explains what the analysis is doing.
func (analyser *CodeChurnAnalysis) Description() string {
	// TODO description
	return "Line burndown stats indicate the numbers of lines which were last edited within " +
		"specific time intervals through time. Search for \"git-of-theseus\" in the internet."
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
	analyser.churnDeltas = map[churnDeltaKey]churnDelta{}

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
func (analyser *CodeChurnAnalysis) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	changes := deps[linehistory.DependencyLineHistory].(core.LineHistoryChanges)
	analyser.fileResolver = changes.Resolver
	peopleCount := analyser.peopleResolver.MaxCount()

	for _, change := range changes.Changes {
		if change.IsDelete() {
			continue
		}
		if int(change.PrevAuthor) >= peopleCount && change.PrevAuthor != core.AuthorMissing {
			change.PrevAuthor = core.AuthorMissing
		}
		if int(change.CurrAuthor) >= peopleCount && change.CurrAuthor != core.AuthorMissing {
			change.CurrAuthor = core.AuthorMissing
		}

		analyser.updateAuthor(change)
	}

	return nil, nil
}

type churnDeltaKey struct {
	core.AuthorId
	core.FileId
}

type churnDelta struct {
	lastTouch core.TickNumber
	churnLines
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

	deleteHistory map[core.AuthorId]sparseHistory
}

func (analyser *CodeChurnAnalysis) updateAwareness(change core.LineHistoryChange, fileEntry *churnFileEntry) {
	lineDelta := int32(change.Delta)

	deltaKey := churnDeltaKey{change.PrevAuthor, change.FileId}
	delta, hasDelta := analyser.churnDeltas[deltaKey]

	if delta.lastTouch != change.CurrTick {
		if hasDelta {
			if change.PrevAuthor != change.CurrAuthor {
				delta.deletedByOthers -= lineDelta
				lineDelta = 0
			}
			awareness, memorability := analyser.calculateAwareness(*fileEntry, change, delta.lastTouch, delta.churnLines)
			fileEntry.awareness, fileEntry.memorability = float32(awareness), float32(memorability)
		}
		if lineDelta == 0 {
			delete(analyser.churnDeltas, deltaKey)
			return
		}

		delta = churnDelta{lastTouch: change.CurrTick}
	}

	if change.PrevAuthor != change.CurrAuthor {
		if lineDelta < 0 {
			delta.deletedByOthers -= lineDelta
		}
	} else {
		if lineDelta > 0 {
			delta.inserted += lineDelta
		} else {
			delta.deletedBySelf -= lineDelta
		}
	}
	analyser.churnDeltas[deltaKey] = delta
}

func (analyser *CodeChurnAnalysis) updateAuthor(change core.LineHistoryChange) {
	if change.PrevAuthor == core.AuthorMissing || change.Delta == 0 {
		return
	}

	fileEntry := analyser.codeChurns[change.PrevAuthor].getFileEntry(change.FileId)

	analyser.updateAwareness(change, &fileEntry)

	lineDelta := int32(change.Delta)
	fileEntry.ownedLines += lineDelta
	if change.Delta > 0 {
		// PrevAuthor == CurrAuthor
		fileEntry.insertedLines += lineDelta
	} else {
		history := fileEntry.deleteHistory[change.CurrAuthor]
		if history == nil {
			history = sparseHistory{}
			fileEntry.deleteHistory[change.CurrAuthor] = history
		}
		history.updateDelta(int(change.PrevTick), int(change.CurrTick), change.Delta)
	}

	analyser.codeChurns[change.PrevAuthor].files[change.FileId] = fileEntry
}

// Finalize returns the result of the analysis. Further calls to Consume() are not expected.
func (analyser *CodeChurnAnalysis) Finalize() interface{} {
	result := CodeChurnResult{
		Authors:     make([]CodeChurnAuthorResult, len(analyser.codeChurns)),
		people:      analyser.peopleResolver.CopyNames(false),
		tickSize:    analyser.tickSize,
		sampling:    analyser.Sampling,
		granularity: analyser.Granularity,
	}

	fmt.Fprintln(os.Stderr)
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
		fmt.Fprintf(os.Stderr, "%s (%d):\t\t%d\t%d\t%d = %d\n", name, pId, inserted, deletedBySelf, deletedByOthers,
			inserted+deletedBySelf+deletedByOthers)
	}
	fmt.Fprintln(os.Stderr)

	return result
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (analyser *CodeChurnAnalysis) Serialize(result interface{}, binary bool, writer io.Writer) error {
	churnResult, ok := result.(CodeChurnResult)
	if !ok {
		return fmt.Errorf("result is not a CodeChurnResult: '%v'", result)
	}
	if binary {
		return analyser.serializeBinary(&churnResult, writer)
	}
	analyser.serializeText(&churnResult, writer)
	return nil
}

// Deserialize converts the specified protobuf bytes to BurndownResult.
func (analyser *CodeChurnAnalysis) Deserialize(message []byte) (interface{}, error) {
	payload := pb.CodeChurnAnalysisResults{}
	if err := proto.Unmarshal(message, &payload); err != nil {
		return nil, err
	}

	result := CodeChurnResult{
		Authors:     make([]CodeChurnAuthorResult, len(payload.Authors)),
		people:      append([]string(nil), payload.People...),
		tickSize:    time.Duration(payload.TickSize),
		sampling:    int(payload.Sampling),
		granularity: int(payload.Granularity),
	}
	for authorID, author := range payload.Authors {
		files := make(map[string]CodeChurnFileResult, len(author.Files))
		for _, file := range author.Files {
			files[file.File] = CodeChurnFileResult{
				InsertedLines: file.InsertedLines,
				OwnedLines:    file.OwnedLines,
				Memorability:  file.Memorability,
				Awareness:     file.Awareness,
				DeleteHistory: deserializeDeleteHistory(file.DeleteHistory),
			}
		}
		result.Authors[authorID] = CodeChurnAuthorResult{Files: files}
	}
	return result, nil
}

// MergeResults combines two BurndownResult-s together.
func (analyser *CodeChurnAnalysis) MergeResults(
	r1, r2 interface{}, c1, c2 *core.CommonAnalysisResult,
) interface{} {
	cr1 := r1.(CodeChurnResult)
	cr2 := r2.(CodeChurnResult)
	if cr1.tickSize != cr2.tickSize {
		panic("cannot merge CodeChurn results with different tick sizes")
	}

	mergedPeople := append([]string(nil), cr1.people...)
	peopleIndex := make(map[string]int, len(mergedPeople))
	for i, name := range mergedPeople {
		peopleIndex[name] = i
	}
	for _, name := range cr2.people {
		if _, exists := peopleIndex[name]; exists {
			continue
		}
		peopleIndex[name] = len(mergedPeople)
		mergedPeople = append(mergedPeople, name)
	}

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

	mergeAuthor := func(result CodeChurnResult) {
		for authorID, author := range result.Authors {
			if authorID >= len(result.people) {
				continue
			}
			targetID := peopleIndex[result.people[authorID]]
			targetFiles := merged.Authors[targetID].Files
			for fileName, stats := range author.Files {
				current, exists := targetFiles[fileName]
				if !exists {
					targetFiles[fileName] = cloneCodeChurnFileResult(stats)
					continue
				}
				current.InsertedLines += stats.InsertedLines
				current.OwnedLines += stats.OwnedLines
				current.Memorability = maxFloat32(current.Memorability, stats.Memorability)
				current.Awareness = maxFloat32(current.Awareness, stats.Awareness)
				current.DeleteHistory = mergeDeleteHistory(current.DeleteHistory, stats.DeleteHistory)
				targetFiles[fileName] = current
			}
		}
	}

	mergeAuthor(cr1)
	mergeAuthor(cr2)
	return merged
}

func (analyser *CodeChurnAnalysis) serializeText(result *CodeChurnResult, writer io.Writer) {
	fmt.Fprintln(writer, "  people:")
	for _, person := range result.people {
		fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}
	fmt.Fprintln(writer, "  tick_size:", int(result.tickSize.Seconds()))
	fmt.Fprintln(writer, "  granularity:", result.granularity)
	fmt.Fprintln(writer, "  sampling:", result.sampling)
	fmt.Fprintln(writer, "  authors:")
	for authorID, author := range result.Authors {
		name := core.AuthorMissingName
		if authorID < len(result.people) {
			name = result.people[authorID]
		}
		fmt.Fprintf(writer, "    %d:\n", authorID)
		fmt.Fprintf(writer, "      name: %s\n", yaml.SafeString(name))
		fmt.Fprintln(writer, "      files:")
		fileNames := sortedCodeChurnFiles(author.Files)
		for _, fileName := range fileNames {
			stats := author.Files[fileName]
			fmt.Fprintf(writer, "        %s:\n", yaml.SafeString(fileName))
			fmt.Fprintf(writer, "          inserted_lines: %d\n", stats.InsertedLines)
			fmt.Fprintf(writer, "          owned_lines: %d\n", stats.OwnedLines)
			fmt.Fprintf(writer, "          memorability: %.6f\n", stats.Memorability)
			fmt.Fprintf(writer, "          awareness: %.6f\n", stats.Awareness)
		}
	}
}

func (analyser *CodeChurnAnalysis) serializeBinary(result *CodeChurnResult, writer io.Writer) error {
	message := pb.CodeChurnAnalysisResults{
		Granularity: int32(result.granularity),
		Sampling:    int32(result.sampling),
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
			pbAuthor.Files = append(pbAuthor.Files, &pb.CodeChurnFileStat{
				File:          fileName,
				InsertedLines: stats.InsertedLines,
				OwnedLines:    stats.OwnedLines,
				Memorability:  stats.Memorability,
				Awareness:     stats.Awareness,
				DeleteHistory: serializeDeleteHistory(stats.DeleteHistory),
			})
		}
		message.Authors[authorID] = pbAuthor
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
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
		for previousTick, delta := range entry.deltas {
			deltas[previousTick] = delta
		}
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

func serializeDeleteHistory(history map[int]sparseHistory) []*pb.CodeChurnDeleteHistory {
	if len(history) == 0 {
		return nil
	}
	authors := make([]int, 0, len(history))
	for author := range history {
		authors = append(authors, author)
	}
	sort.Ints(authors)

	result := make([]*pb.CodeChurnDeleteHistory, 0, len(authors))
	for _, author := range authors {
		currentTicks := make([]int, 0, len(history[author]))
		for currentTick := range history[author] {
			currentTicks = append(currentTicks, currentTick)
		}
		sort.Ints(currentTicks)
		for _, currentTick := range currentTicks {
			entry := history[author][currentTick]
			previousTicks := make([]int, 0, len(entry.deltas))
			for previousTick := range entry.deltas {
				previousTicks = append(previousTicks, previousTick)
			}
			sort.Ints(previousTicks)
			pbEntry := &pb.CodeChurnDeleteHistory{
				Author:      int32(author),
				CurrentTick: int32(currentTick),
				Entries:     make([]*pb.CodeChurnSparseHistoryEntry, 0, len(previousTicks)),
			}
			for _, previousTick := range previousTicks {
				pbEntry.Entries = append(pbEntry.Entries, &pb.CodeChurnSparseHistoryEntry{
					PreviousTick: int32(previousTick),
					Delta:        entry.deltas[previousTick],
				})
			}
			result = append(result, pbEntry)
		}
	}
	return result
}

func deserializeDeleteHistory(entries []*pb.CodeChurnDeleteHistory) map[int]sparseHistory {
	if len(entries) == 0 {
		return nil
	}
	result := map[int]sparseHistory{}
	for _, entry := range entries {
		author := int(entry.Author)
		history := result[author]
		if history == nil {
			history = sparseHistory{}
			result[author] = history
		}
		deltas := make(map[int]int64, len(entry.Entries))
		for _, delta := range entry.Entries {
			deltas[int(delta.PreviousTick)] = delta.Delta
		}
		history[int(entry.CurrentTick)] = sparseHistoryEntry{deltas: deltas}
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

func (analyser *CodeChurnAnalysis) memoryLoss(x float64) float64 {
	const halfLossPeriod = 30
	return 1.0 / (1.0 + math.Exp(x-halfLossPeriod))
}

func (analyser *CodeChurnAnalysis) calculateAwareness(entry churnFileEntry, change core.LineHistoryChange,
	lastTouch core.TickNumber, delta churnLines,
) (awareness, memorability float64) {
	const awarenessLowCut = 0.5
	const memorabilityMin = 0.5

	if entry.insertedLines == 0 {
		// initial
		return 0, memorabilityMin
	}
	awareness, memorability = float64(entry.awareness), float64(entry.memorability)
	if lastTouch >= change.CurrTick {
		return
	}

	ownedLines := 0.0
	if entry.ownedLines > 0 {
		ownedLines = float64(entry.ownedLines)
		awareness = math.Max(0, awareness*
			float64(entry.ownedLines-delta.deletedByOthers-delta.deletedBySelf)/ownedLines)
	}
	awareness += float64(delta.inserted)

	timeDelta := float64(int(change.CurrTick - lastTouch))
	reinforcementFactor := 1.0 // TODO reinforcementFactor

	memorability = math.Min(1, memorability*reinforcementFactor*(ownedLines+float64(delta.inserted))/
		(ownedLines+float64(delta.deletedByOthers)))
	// memorability is increased by delta.inserted + delta.deletedByOthers
	// memorability is reduced by delta.deletedByOthers

	if awareness > awarenessLowCut {
		memorability = math.Min(memorability, memorabilityMin)

		awareness = awareness * analyser.memoryLoss(timeDelta*(1+memorabilityMin-memorability))
		if awareness >= awarenessLowCut {
			return
		}
	}
	return 0, 0

	// memory halflife = min 30d max 180d
	// reinforcement period = memorability * 3 months

	// memory loss = 0.5 ^ (period / (reinforcement_period * memorability))
	// memory gain

	// negative Delta is better for memorability
	// frequent access - better
	// more owned lines - better
	// more deleted lines of others - better
	// more deleted lines of own - keepup
	// larger file - less awareness

	//	awareness = entry.awareness + float32(entry.ownedLines
}

func init() {
	core.Registry.Register(&CodeChurnAnalysis{})
}
