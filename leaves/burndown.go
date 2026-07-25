package leaves

import (
	"bytes"
	"compress/flate"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/burndown"
	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/join"
	"github.com/cwbudde/hercules/internal/linehistory"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/yaml"
)

// BurndownAnalysis allows to gather the line burndown statistics for a Git repository.
// It is a LeafPipelineItem.
// Reference: https://erikbern.com/2016/12/05/the-half-life-of-code.html
type BurndownAnalysis struct {
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
	// repositoryName is the name/path of the repository from metadata.
	// Used to track individual repository contributions during merge operations.
	repositoryName string
	// globalHistory is the daily deltas of daily line counts.
	// E.g. tick 0: tick 0 +50 lines
	//      tick 10: tick 0 -10 lines; tick 10 +20 lines
	//      tick 12: tick 0 -5 lines; tick 10 -3 lines; tick 12 +10 lines
	// map [0] [0] = 50
	// map[10] [0] = -10
	// map[10][10] = 20
	// map[12] [0] = -5
	// map[12][10] = -3
	// map[12][12] = 10
	globalHistory sparseHistory
	// fileHistories is the daily deltas of each file's daily line counts.
	fileHistories map[core.FileId]sparseHistory
	// peopleHistories is the daily deltas of each person's daily line counts.
	peopleHistories []sparseHistory
	// matrix is the mutual deletions and self insertions.
	matrix []map[core.AuthorId]int64

	// TickSize indicates the size of each time granule: day, hour, week, etc.
	tickSize time.Duration

	peopleResolver  core.IdentityResolver
	primaryResolver core.FileIdResolver
	fileResolver    core.FileIdResolver

	// HibernationToDisk saves hibernated data to disk rather than keeping in memory.
	HibernationToDisk bool
	// HibernationDirectory is the temp directory for hibernated data files.
	HibernationDirectory string

	hibernatedData     []byte
	hibernatedFileName string

	l core.Logger
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (analyser *BurndownAnalysis) Name() string {
	return "Burndown"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (analyser *BurndownAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (analyser *BurndownAnalysis) Requires() []string {
	return []string{linehistory.DependencyLineHistory, identity.DependencyAuthor}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (analyser *BurndownAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	opts := make([]core.ConfigurationOption, len(BurndownSharedOptions), len(BurndownSharedOptions)+2)
	copy(opts, BurndownSharedOptions[:])
	opts = append(opts, core.ConfigurationOption{
		Name:        ConfigBurndownHibernationDisk,
		Description: "Save hibernated burndown data to disk rather than keep in memory.",
		Flag:        "burndown-hibernation-disk",
		Type:        core.BoolConfigurationOption,
		Default:     false,
	}, core.ConfigurationOption{
		Name:        ConfigBurndownHibernationDir,
		Description: "Temporary directory for hibernated burndown data files.",
		Flag:        "burndown-hibernation-dir",
		Type:        core.PathConfigurationOption,
		Default:     "",
	})

	return opts
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (analyser *BurndownAnalysis) Configure(facts map[string]any) error {
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

	if people, ok := facts[ConfigBurndownTrackPeople].(bool); people {
		if val, ok := facts[core.FactIdentityResolver].(core.IdentityResolver); ok {
			analyser.peopleResolver = val
		}
	} else if ok {
		analyser.peopleResolver = nil
	}

	if resolver, exists := facts[core.FactLineHistoryResolver].(core.FileIdResolver); exists {
		analyser.primaryResolver = resolver
	}

	analyser.fileResolver = analyser.primaryResolver

	if val, exists := facts[ConfigBurndownHibernationDisk].(bool); exists {
		analyser.HibernationToDisk = val
	}

	if val, exists := facts[ConfigBurndownHibernationDir].(string); exists {
		analyser.HibernationDirectory = val
	}

	return nil
}

func (analyser *BurndownAnalysis) ConfigureUpstream(_ map[string]any) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (analyser *BurndownAnalysis) Flag() string {
	return "burndown"
}

// Description returns the text which explains what the analysis is doing.
func (analyser *BurndownAnalysis) Description() string {
	return "Line burndown stats indicate the numbers of lines which were last edited within " +
		"specific time intervals through time. Search for \"git-of-theseus\" in the internet."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (analyser *BurndownAnalysis) Initialize(repository *git.Repository) error {
	analyser.l = core.NewLogger()
	// Force the safer defaults; mismatched sampling/granularity caused crashes in burndown.
	analyser.Granularity = DefaultBurndownGranularity

	analyser.Sampling = DefaultBurndownGranularity
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
	analyser.globalHistory = sparseHistory{}
	analyser.fileHistories = map[core.FileId]sparseHistory{}

	if analyser.peopleResolver == nil {
		analyser.peopleResolver = core.NewIdentityResolver(nil, nil)
	}

	peopleCount := analyser.peopleResolver.MaxCount()
	analyser.peopleHistories = make([]sparseHistory, peopleCount)
	analyser.matrix = make([]map[core.AuthorId]int64, peopleCount)

	return nil
}

func (analyser *BurndownAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(analyser, n)
}

func (analyser *BurndownAnalysis) Merge([]core.PipelineItem) {
	// for _, branch := range branches {
	//	clone := branch.(*BurndownAnalysis)
	//	for id, fileHistory := range clone.fileHistories
	//}
}

// Consume runs this PipelineItem on the next commits data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (analyser *BurndownAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	changes := deps[linehistory.DependencyLineHistory].(core.LineHistoryChanges)
	if analyser.primaryResolver == nil {
		analyser.primaryResolver = changes.Resolver
	}

	analyser.fileResolver = changes.Resolver
	peopleCount := analyser.peopleResolver.MaxCount()

	for _, change := range changes.Changes {
		if change.IsDelete() {
			if analyser.TrackFiles {
				analyser.updateFileDelete(change)
			}

			continue
		}

		if int(change.PrevAuthor) >= peopleCount && change.PrevAuthor != core.AuthorMissing {
			change.PrevAuthor = core.AuthorMissing
		}

		if int(change.CurrAuthor) >= peopleCount && change.CurrAuthor != core.AuthorMissing {
			change.CurrAuthor = core.AuthorMissing
		}

		analyser.updateGlobal(change)

		if analyser.TrackFiles {
			analyser.updateFile(change)
		}

		analyser.updateAuthor(change)
		analyser.updateChurnMatrix(change)
	}

	analyser.fileResolver = analyser.primaryResolver

	return nil, nil
}

func (analyser *BurndownAnalysis) updateGlobal(change core.LineHistoryChange) {
	analyser.globalHistory.updateDelta(int(change.PrevTick), int(change.CurrTick), change.Delta)
}

// updateFile is bound to the specific `history` in the closure.
func (analyser *BurndownAnalysis) updateFile(change core.LineHistoryChange) {
	history := analyser.fileHistories[change.FileId]
	if history == nil {
		// can be not nil if the file was created in a future branch
		history = sparseHistory{}
		analyser.fileHistories[change.FileId] = history
	}

	history.updateDelta(int(change.PrevTick), int(change.CurrTick), change.Delta)
}

func (analyser *BurndownAnalysis) updateFileDelete(change core.LineHistoryChange) {
	delete(analyser.fileHistories, change.FileId)
}

func (analyser *BurndownAnalysis) updateAuthor(change core.LineHistoryChange) {
	if change.PrevAuthor == core.AuthorMissing {
		return
	}

	history := analyser.peopleHistories[change.PrevAuthor]
	if history == nil {
		history = sparseHistory{}
		analyser.peopleHistories[change.PrevAuthor] = history
	}

	history.updateDelta(int(change.PrevTick), int(change.CurrTick), change.Delta)
}

func (analyser *BurndownAnalysis) updateChurnMatrix(change core.LineHistoryChange) {
	if change.PrevAuthor == core.AuthorMissing {
		return
	}

	newAuthor := change.CurrAuthor
	if change.Delta > 0 {
		newAuthor = authorSelf
	}

	row := analyser.matrix[change.PrevAuthor]
	if row == nil {
		row = map[core.AuthorId]int64{}
		analyser.matrix[change.PrevAuthor] = row
	}

	row[newAuthor] += int64(change.Delta)
}

// burndownState holds the serializable state for hibernation.
type burndownState struct {
	GlobalHistory   map[int]map[int]int64
	FileHistories   map[core.FileId]map[int]map[int]int64
	PeopleHistories []map[int]map[int]int64
	Matrix          []map[core.AuthorId]int64
}

func sparseHistoryToMap(sh sparseHistory) map[int]map[int]int64 {
	if sh == nil {
		return nil
	}

	m := make(map[int]map[int]int64, len(sh))
	for k, v := range sh {
		m[k] = v.deltas
	}

	return m
}

func mapToSparseHistory(m map[int]map[int]int64) sparseHistory {
	if m == nil {
		return nil
	}

	sh := make(sparseHistory, len(m))
	for k, v := range m {
		sh[k] = sparseHistoryEntry{deltas: v}
	}

	return sh
}

// Hibernate compresses the burndown analysis state to save memory.
func (analyser *BurndownAnalysis) Hibernate() error {
	state := analyser.hibernationState()

	data, err := compressBurndownState(state)
	if err != nil {
		return err
	}

	if analyser.HibernationToDisk {
		err := analyser.persistHibernation(data)
		if err != nil {
			return err
		}
	} else {
		analyser.hibernatedData = data
	}

	analyser.globalHistory = nil
	analyser.fileHistories = nil
	analyser.peopleHistories = nil
	analyser.matrix = nil

	return nil
}

func (analyser *BurndownAnalysis) hibernationState() burndownState {
	state := burndownState{
		GlobalHistory: sparseHistoryToMap(analyser.globalHistory),
		Matrix:        analyser.matrix,
	}
	if analyser.fileHistories != nil {
		state.FileHistories = make(map[core.FileId]map[int]map[int]int64, len(analyser.fileHistories))
		for k, v := range analyser.fileHistories {
			state.FileHistories[k] = sparseHistoryToMap(v)
		}
	}

	if analyser.peopleHistories != nil {
		state.PeopleHistories = make([]map[int]map[int]int64, len(analyser.peopleHistories))
		for i, v := range analyser.peopleHistories {
			state.PeopleHistories[i] = sparseHistoryToMap(v)
		}
	}

	return state
}

func compressBurndownState(state burndownState) ([]byte, error) {
	var buf bytes.Buffer

	fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}

	err = gob.NewEncoder(fw).Encode(state)
	if err != nil {
		fw.Close()
		return nil, err
	}

	err = fw.Close()
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (analyser *BurndownAnalysis) persistHibernation(data []byte) error {
	file, err := os.CreateTemp(analyser.HibernationDirectory, "*-hercules-burndown.bin")
	if err != nil {
		return err
	}

	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	_, err = file.Write(data)
	if err != nil {
		cleanup()
		return err
	}

	err = file.Close()
	if err != nil {
		_ = os.Remove(file.Name())
		return err
	}

	analyser.hibernatedFileName = file.Name()

	return nil
}

// Boot restores the burndown analysis state from hibernation.
func (analyser *BurndownAnalysis) Boot() error {
	var data []byte

	if analyser.hibernatedFileName != "" {
		var err error

		data, err = os.ReadFile(analyser.hibernatedFileName)
		if err != nil {
			return err
		}

		if err := os.Remove(analyser.hibernatedFileName); err != nil {
			return err
		}

		analyser.hibernatedFileName = ""
	} else {
		data = analyser.hibernatedData
		analyser.hibernatedData = nil
	}

	if len(data) == 0 {
		return errors.New("burndown: Boot() called without prior Hibernate()")
	}

	fr := flate.NewReader(bytes.NewReader(data))
	defer fr.Close()

	var state burndownState

	err := gob.NewDecoder(fr).Decode(&state)
	if err != nil {
		return err
	}

	analyser.globalHistory = mapToSparseHistory(state.GlobalHistory)
	analyser.matrix = state.Matrix

	if state.FileHistories != nil {
		analyser.fileHistories = make(map[core.FileId]sparseHistory, len(state.FileHistories))
		for k, v := range state.FileHistories {
			analyser.fileHistories[k] = mapToSparseHistory(v)
		}
	}

	if state.PeopleHistories != nil {
		analyser.peopleHistories = make([]sparseHistory, len(state.PeopleHistories))
		for i, v := range state.PeopleHistories {
			analyser.peopleHistories[i] = mapToSparseHistory(v)
		}
	}

	return nil
}

// Finalize returns the result of the analysis. Further calls to Consume() are not expected.
func (analyser *BurndownAnalysis) Finalize() any {
	globalHistory, lastTick := analyser.groupSparseHistory(analyser.globalHistory, -1)
	fileHistories := analyser.finalizeFileHistories(lastTick)
	fileOwnership := map[string]map[int]int{}
	peopleNumber := analyser.peopleResolver.Count()

	peopleHistories := make([]burndown.DenseHistory, peopleNumber)
	if peopleNumber > 0 {
		analyser.collectFileOwnership(fileOwnership)
		peopleHistories = analyser.finalizePeopleHistories(globalHistory, lastTick, peopleNumber)
	}

	result := BurndownResult{
		GlobalHistory:      globalHistory,
		FileHistories:      fileHistories,
		FileOwnership:      fileOwnership,
		PeopleHistories:    peopleHistories,
		PeopleMatrix:       analyser.finalizePeopleMatrix(peopleNumber),
		tickSize:           analyser.tickSize,
		reversedPeopleDict: analyser.peopleResolver.CopyNames(false),
		sampling:           analyser.Sampling,
		granularity:        analyser.Granularity,
	}

	if analyser.repositoryName != "" {
		result.ReversedRepositoryDict = []string{analyser.repositoryName}
		result.RepositoryHistories = []burndown.DenseHistory{globalHistory}
	}

	return result
}

func (analyser *BurndownAnalysis) finalizeFileHistories(lastTick int) map[string]burndown.DenseHistory {
	result := map[string]burndown.DenseHistory{}

	for fileID, history := range analyser.fileHistories {
		if len(history) > 0 {
			if name := analyser.fileResolver.NameOf(fileID); name != "" {
				result[name], _ = analyser.groupSparseHistory(history, lastTick)
			}
		}
	}

	return result
}

func (analyser *BurndownAnalysis) finalizePeopleHistories(
	global burndown.DenseHistory, lastTick, peopleNumber int,
) []burndown.DenseHistory {
	result := make([]burndown.DenseHistory, peopleNumber)
	for i := range result {
		if history := analyser.peopleHistories[i]; len(history) > 0 {
			result[i], _ = analyser.groupSparseHistory(history, lastTick)
		} else {
			result[i] = make(burndown.DenseHistory, len(global))
			for j, row := range global {
				result[i][j] = make([]int64, len(row))
			}
		}
	}

	return result
}

func (analyser *BurndownAnalysis) finalizePeopleMatrix(peopleNumber int) burndown.DenseHistory {
	if len(analyser.matrix) == 0 {
		return nil
	}

	result := make(burndown.DenseHistory, peopleNumber)
	for i := range result {
		result[i] = make([]int64, peopleNumber+2)
		for key, value := range analyser.matrix[i] {
			switch key {
			case core.AuthorMissing:
				key = -1
			case authorSelf:
				key = -2
			}

			result[i][key+2] = value
		}
	}

	return result
}

func (analyser *BurndownAnalysis) collectFileOwnership(fileOwnership map[string]map[int]int) {
	analyser.fileResolver.ForEachFile(func(fileId core.FileId, fileName string) {
		previousLine := 0
		previousAuthor := core.AuthorMissing
		ownership := map[int]int{}

		if analyser.fileResolver.ScanFile(fileId,
			func(line int, tick core.TickNumber, author core.AuthorId) {
				length := line - previousLine
				if length > 0 {
					ownership[previousAuthor] += length
				}

				previousLine = line

				if author >= core.AuthorMissing {
					previousAuthor = -1
				} else {
					previousAuthor = int(author)
				}
			}) {
			fileOwnership[fileName] = ownership
		}
	})
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (analyser *BurndownAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	burndownResult, ok := result.(BurndownResult)
	if !ok {
		return fmt.Errorf("result is not a burndown result: '%v'", result)
	}

	if binary {
		return analyser.serializeBinary(&burndownResult, writer)
	}

	analyser.serializeText(&burndownResult, writer)

	return nil
}

// Deserialize converts the specified protobuf bytes to BurndownResult.
func (analyser *BurndownAnalysis) Deserialize(message []byte) (any, error) {
	msg := pb.BurndownAnalysisResults{}

	err := proto.Unmarshal(message, &msg)
	if err != nil {
		return nil, err
	}

	result := BurndownResult{
		GlobalHistory: denseBurndownMatrix(msg.GetProject()),
		FileHistories: map[string]burndown.DenseHistory{},
		FileOwnership: map[string]map[int]int{},
		tickSize:      time.Duration(msg.GetTickSize()),

		granularity: int(msg.GetGranularity()),
		sampling:    int(msg.GetSampling()),
	}
	for i, mat := range msg.GetFiles() {
		result.FileHistories[mat.GetName()] = denseBurndownMatrix(mat)
		result.FileOwnership[mat.GetName()] = burndownOwnership(msg.GetFilesOwnership()[i])
	}

	result.reversedPeopleDict = make([]string, len(msg.GetPeople()))

	result.PeopleHistories = make([]burndown.DenseHistory, len(msg.GetPeople()))
	for i, mat := range msg.GetPeople() {
		result.PeopleHistories[i] = denseBurndownMatrix(mat)
		result.reversedPeopleDict[i] = mat.GetName()
	}

	result.PeopleMatrix = denseInteractionMatrix(msg.GetPeopleInteraction())

	result.ReversedRepositoryDict = msg.GetRepositorySequence()
	if len(msg.GetRepositories()) > 0 {
		result.RepositoryHistories = make([]burndown.DenseHistory, len(msg.GetRepositories()))
		for i, mat := range msg.GetRepositories() {
			result.RepositoryHistories[i] = denseBurndownMatrix(mat)
		}
	}

	return result, nil
}

func denseBurndownMatrix(matrix *pb.BurndownSparseMatrix) burndown.DenseHistory {
	result := make(burndown.DenseHistory, matrix.GetNumberOfRows())
	for i := range matrix.GetNumberOfRows() {
		result[i] = make([]int64, matrix.GetNumberOfColumns())
		for j, value := range matrix.GetRows()[i].GetColumns() {
			result[i][j] = int64(value)
		}
	}

	return result
}

func burndownOwnership(source *pb.FilesOwnership) map[int]int {
	result := map[int]int{}
	for author, lines := range source.GetValue() {
		result[int(author)] = int(lines)
	}

	return result
}

func denseInteractionMatrix(matrix *pb.CompressedSparseRowMatrix) burndown.DenseHistory {
	if matrix == nil {
		return nil
	}

	result := make(burndown.DenseHistory, matrix.GetNumberOfRows())
	for i := range result {
		result[i] = make([]int64, matrix.GetNumberOfColumns())
		for j := int(matrix.GetIndptr()[i]); j < int(matrix.GetIndptr()[i+1]); j++ {
			result[i][matrix.GetIndices()[j]] = matrix.GetData()[j]
		}
	}

	return result
}

// MergeResults combines two BurndownResult-s together.
func (analyser *BurndownAnalysis) MergeResults(
	r1, r2 any, c1, c2 *core.CommonAnalysisResult,
) any {
	bar1 := r1.(BurndownResult)

	bar2 := r2.(BurndownResult)
	if bar1.tickSize != bar2.tickSize {
		return fmt.Errorf("mismatching tick sizes (r1: %d, r2: %d) received",
			bar1.tickSize, bar2.tickSize)
	}

	merged := BurndownResult{
		tickSize: bar1.tickSize,
	}
	merged.sampling = min(bar1.sampling, bar2.sampling)

	merged.granularity = min(bar1.granularity, bar2.granularity)
	var people map[string]join.JoinedIndex
	people, merged.reversedPeopleDict = join.PeopleIdentities(
		bar1.reversedPeopleDict, bar2.reversedPeopleDict,
	)
	coordinator := burndownMergeCoordinator{
		first: bar1, second: bar2, commonFirst: c1, commonSecond: c2,
		sem: make(chan int, 5),
	}

	if len(bar1.GlobalHistory) > 0 || len(bar2.GlobalHistory) > 0 {
		coordinator.merge(&merged.GlobalHistory, bar1.GlobalHistory, bar2.GlobalHistory)
	}

	coordinator.mergePeople(&merged, people)

	var repositories map[string]join.JoinedIndex

	repositories, merged.ReversedRepositoryDict = join.RepositoryIdentities(
		bar1.ReversedRepositoryDict, bar2.ReversedRepositoryDict,
	)
	coordinator.mergeRepositories(&merged, repositories)

	coordinator.wg.Wait()

	return merged
}

type burndownMergeCoordinator struct {
	first, second             BurndownResult
	commonFirst, commonSecond *core.CommonAnalysisResult
	wg                        sync.WaitGroup
	sem                       chan int
}

func (coordinator *burndownMergeCoordinator) mergePeople(
	merged *BurndownResult, people map[string]join.JoinedIndex,
) {
	if len(merged.reversedPeopleDict) == 0 {
		return
	}

	if len(coordinator.first.PeopleHistories) > 0 || len(coordinator.second.PeopleHistories) > 0 {
		merged.PeopleHistories = make([]burndown.DenseHistory, len(merged.reversedPeopleDict))
		for index, key := range merged.reversedPeopleDict {
			first, second := joinedBurndownHistories(
				people[key], coordinator.first.PeopleHistories, coordinator.second.PeopleHistories,
			)
			coordinator.merge(&merged.PeopleHistories[index], first, second)
		}
	}

	coordinator.run(func() {
		merged.PeopleMatrix = mergePeopleInteraction(
			coordinator.first, coordinator.second, merged.reversedPeopleDict, people,
		)
	})
}

func (coordinator *burndownMergeCoordinator) mergeRepositories(
	merged *BurndownResult, repositories map[string]join.JoinedIndex,
) {
	if len(merged.ReversedRepositoryDict) == 0 {
		return
	}

	merged.RepositoryHistories = make([]burndown.DenseHistory, len(merged.ReversedRepositoryDict))
	for index, key := range merged.ReversedRepositoryDict {
		first, second := joinedBurndownHistories(
			repositories[key],
			coordinator.first.RepositoryHistories,
			coordinator.second.RepositoryHistories,
		)
		coordinator.merge(&merged.RepositoryHistories[index], first, second)
	}
}

func (coordinator *burndownMergeCoordinator) run(operation func()) {
	coordinator.wg.Add(1)

	coordinator.sem <- 1
	go func() {
		defer coordinator.wg.Done()
		defer func() { <-coordinator.sem }()

		operation()
	}()
}

func (coordinator *burndownMergeCoordinator) merge(
	target *burndown.DenseHistory, first, second burndown.DenseHistory,
) {
	coordinator.run(func() {
		*target = burndown.MergeBurndownMatrices(
			first, second,
			coordinator.first.granularity, coordinator.first.sampling,
			coordinator.second.granularity, coordinator.second.sampling,
			coordinator.first.tickSize, coordinator.commonFirst, coordinator.commonSecond,
		)
	})
}

func joinedBurndownHistories(
	index join.JoinedIndex, first, second []burndown.DenseHistory,
) (burndown.DenseHistory, burndown.DenseHistory) {
	var firstHistory, secondHistory burndown.DenseHistory
	if index.First >= 0 {
		firstHistory = first[index.First]
	}

	if index.Second >= 0 {
		secondHistory = second[index.Second]
	}

	return firstHistory, secondHistory
}

func mergePeopleInteraction(
	first, second BurndownResult, mergedPeople []string, people map[string]join.JoinedIndex,
) burndown.DenseHistory {
	if len(second.PeopleMatrix) == 0 {
		matrix := first.PeopleMatrix
		for i := range matrix {
			matrix[i] = append(matrix[i], make([]int64, len(mergedPeople)-len(first.reversedPeopleDict))...)
		}

		for len(matrix) < len(mergedPeople) && len(first.PeopleMatrix) > 0 {
			matrix = append(matrix, make([]int64, len(mergedPeople)+2))
		}

		return matrix
	}

	matrix := make(burndown.DenseHistory, len(mergedPeople))
	for i := range matrix {
		matrix[i] = make([]int64, len(mergedPeople)+2)
	}

	addPeopleInteraction(matrix, first, people, false)
	addPeopleInteraction(matrix, second, people, true)

	return matrix
}

func addPeopleInteraction(
	target burndown.DenseHistory, source BurndownResult,
	people map[string]join.JoinedIndex, add bool,
) {
	for i, name := range source.reversedPeopleDict {
		mergedIndex := people[name].Final

		for column, value := range source.PeopleMatrix[i] {
			targetColumn := column
			if column >= 2 {
				targetColumn = 2 + people[source.reversedPeopleDict[column-2]].Final
			}

			if add {
				target[mergedIndex][targetColumn] += value
			} else {
				target[mergedIndex][targetColumn] = value
			}
		}
	}
}

func (analyser *BurndownAnalysis) serializeText(result *BurndownResult, writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "  granularity:", result.granularity)
	_, _ = fmt.Fprintln(writer, "  sampling:", result.sampling)
	_, _ = fmt.Fprintln(writer, "  tick_size:", int(result.tickSize.Seconds()))
	yaml.PrintMatrix(writer, result.GlobalHistory, 2, "project", true)

	if len(result.FileHistories) > 0 {
		writeBurndownFiles(writer, result)
	}

	if len(result.PeopleHistories) > 0 {
		writeBurndownPeople(writer, result)
	}

	if len(result.RepositoryHistories) > 0 {
		writeBurndownRepositories(writer, result)
	}
}

func writeBurndownFiles(writer io.Writer, result *BurndownResult) {
	_, _ = fmt.Fprintln(writer, "  files:")
	for _, key := range sortedKeys(result.FileHistories) {
		yaml.PrintMatrix(writer, result.FileHistories[key], 4, key, true)
	}

	_, _ = fmt.Fprintln(writer, "  files_ownership:")

	for _, key := range sortedKeys(result.FileOwnership) {
		owned := result.FileOwnership[key]

		developers := make([]int, 0, len(owned))
		for developer := range owned {
			developers = append(developers, developer)
		}

		sort.Slice(developers, func(i, j int) bool {
			return owned[developers[i]] > owned[developers[j]]
		})

		for i, developer := range developers {
			indent := "  "
			if i == 0 {
				indent = "- "
			}

			_, _ = fmt.Fprintf(writer, "    %s%d: %d\n", indent, developer, owned[developer])
		}
	}
}

func writeBurndownPeople(writer io.Writer, result *BurndownResult) {
	_, _ = fmt.Fprintln(writer, "  people_sequence:")
	for i := range result.PeopleHistories {
		_, _ = fmt.Fprintln(writer, "    - "+yaml.SafeString(result.reversedPeopleDict[i]))
	}

	_, _ = fmt.Fprintln(writer, "  people:")
	for i, history := range result.PeopleHistories {
		yaml.PrintMatrix(writer, history, 4, result.reversedPeopleDict[i], true)
	}

	_, _ = fmt.Fprintln(writer, "  people_interaction: |-")
	yaml.PrintMatrix(writer, result.PeopleMatrix, 4, "", false)
}

func writeBurndownRepositories(writer io.Writer, result *BurndownResult) {
	_, _ = fmt.Fprintln(writer, "  repository_sequence:")
	for i := range result.RepositoryHistories {
		_, _ = fmt.Fprintln(writer, "    - "+yaml.SafeString(result.ReversedRepositoryDict[i]))
	}

	_, _ = fmt.Fprintln(writer, "  repositories:")
	for i, history := range result.RepositoryHistories {
		yaml.PrintMatrix(writer, history, 4, result.ReversedRepositoryDict[i], true)
	}
}

func (analyser *BurndownAnalysis) serializeBinary(result *BurndownResult, writer io.Writer) error {
	message := pb.BurndownAnalysisResults{
		Granularity: int32(result.granularity),
		Sampling:    int32(result.sampling),
		TickSize:    int64(result.tickSize),
	}
	if len(result.GlobalHistory) > 0 {
		message.Project = pb.ToBurndownSparseMatrix(result.GlobalHistory, "project")
	}

	if len(result.FileHistories) > 0 {
		message.Files, message.FilesOwnership = burndownFilesToProto(result)
	}

	if len(result.PeopleHistories) > 0 {
		message.People = namedBurndownMatrices(result.PeopleHistories, result.reversedPeopleDict)
	}

	if result.PeopleMatrix != nil {
		message.PeopleInteraction = pb.DenseToCompressedSparseRowMatrix(result.PeopleMatrix)
	}

	if len(result.RepositoryHistories) > 0 {
		message.RepositorySequence = result.ReversedRepositoryDict
		message.Repositories = namedBurndownMatrices(
			result.RepositoryHistories, result.ReversedRepositoryDict,
		)
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}

	_, err = writer.Write(serialized)

	return err
}

func burndownFilesToProto(
	result *BurndownResult,
) ([]*pb.BurndownSparseMatrix, []*pb.FilesOwnership) {
	keys := sortedKeys(result.FileHistories)
	files := make([]*pb.BurndownSparseMatrix, len(keys))

	ownership := make([]*pb.FilesOwnership, len(keys))
	for i, key := range keys {
		files[i] = pb.ToBurndownSparseMatrix(result.FileHistories[key], key)

		values := map[int32]int32{}
		for developer, lines := range result.FileOwnership[key] {
			values[int32(developer)] = int32(lines)
		}

		ownership[i] = &pb.FilesOwnership{Value: values}
	}

	return files, ownership
}

func namedBurndownMatrices(
	histories []burndown.DenseHistory, names []string,
) []*pb.BurndownSparseMatrix {
	result := make([]*pb.BurndownSparseMatrix, len(histories))
	for i, history := range histories {
		if len(history) > 0 {
			result[i] = pb.ToBurndownSparseMatrix(history, names[i])
		}
	}

	return result
}

func (analyser *BurndownAnalysis) groupSparseHistory(
	history sparseHistory, lastTick int,
) (burndown.DenseHistory, int) {
	if len(history) == 0 {
		panic("empty history")
	}

	var ticks []int
	for tick := range history {
		ticks = append(ticks, tick)
	}

	sort.Ints(ticks)

	if lastTick >= 0 {
		if ticks[len(ticks)-1] < lastTick {
			ticks = append(ticks, lastTick)
		} else if ticks[len(ticks)-1] > lastTick {
			panic("ticks corruption")
		}
	} else {
		lastTick = ticks[len(ticks)-1]
	}
	// [y][x]
	// y - sampling
	// x - granularity
	samples := lastTick/analyser.Sampling + 1
	bands := lastTick/analyser.Granularity + 1

	result := make(burndown.DenseHistory, samples)
	for i := range samples {
		result[i] = make([]int64, bands)
	}

	prevSi := 0

	for _, tick := range ticks {
		si := tick / analyser.Sampling
		if si > prevSi {
			state := result[prevSi]
			for i := prevSi + 1; i <= si; i++ {
				copy(result[i], state)
			}

			prevSi = si
		}

		sample := result[si]
		for t, value := range history[tick].deltas {
			sample[t/analyser.Granularity] += value
		}
	}

	return result, lastTick
}

func init() {
	core.Registry.RegisterPreferred(&BurndownAnalysis{}, true)
}
