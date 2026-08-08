package leaves

import (
	"fmt"
	"io"
	"maps"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/yaml"
)

// KnowledgeDiffusionAnalysis tracks unique editors per file over time to
// identify single-contributor risk areas and knowledge silos. For each file
// it records which authors have ever touched it, when each author first
// edited it, and how many distinct editors were active recently.
type KnowledgeDiffusionAnalysis struct {
	core.NoopMerger

	// WindowMonths is the sliding window in months for "recent" editor counting (default 6).
	WindowMonths int

	// fileAuthors: file -> author -> authorFileInfo (first/last tick).
	fileAuthors map[string]map[int]*authorFileInfo
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict.
	reversedPeopleDict []string
	// tickSize references TicksSinceStart.TickSize.
	tickSize time.Duration
	// lastTick tracks the most recent tick seen.
	lastTick int

	l core.Logger
}

// authorFileInfo stores the first and last tick an author edited a file.
type authorFileInfo struct {
	FirstTick int
	LastTick  int
}

const (
	// ConfigKnowledgeDiffusionWindowMonths is the name of the option to configure the recent-editor window.
	ConfigKnowledgeDiffusionWindowMonths = "KnowledgeDiffusion.WindowMonths"
)

// KnowledgeDiffusionFileResult stores per-file knowledge diffusion data.
type KnowledgeDiffusionFileResult struct {
	// UniqueEditorsCount is the total number of distinct authors who ever touched this file.
	UniqueEditorsCount int
	// UniqueEditorsOverTime maps tick -> cumulative unique editor count at that tick.
	UniqueEditorsOverTime map[int]int
	// RecentEditorsCount is the number of editors active within the recent window.
	RecentEditorsCount int
	// Authors is the sorted list of author indices who touched this file.
	Authors []int
}

// KnowledgeDiffusionResult is returned by KnowledgeDiffusionAnalysis.Finalize().
type KnowledgeDiffusionResult struct {
	// Files maps file path to per-file diffusion data.
	Files map[string]*KnowledgeDiffusionFileResult
	// Distribution is a histogram: editor_count -> number_of_files.
	Distribution map[int]int
	// WindowMonths used for recent-editor computation.
	WindowMonths int
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict.
	reversedPeopleDict []string
	// tickSize is the duration of each tick.
	tickSize time.Duration
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (kd *KnowledgeDiffusionAnalysis) Name() string {
	return "KnowledgeDiffusion"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (kd *KnowledgeDiffusionAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (kd *KnowledgeDiffusionAnalysis) Requires() []string {
	return []string{
		identity.DependencyAuthor,
		items.DependencyTreeChanges,
		items.DependencyTick,
	}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (kd *KnowledgeDiffusionAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{{
		Name:        ConfigKnowledgeDiffusionWindowMonths,
		Description: "Sliding window in months for counting recent editors (default 6).",
		Flag:        "knowledge-diffusion-window",
		Type:        core.IntConfigurationOption,
		Default:     6,
	}}

	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (kd *KnowledgeDiffusionAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		kd.l = l
	}

	if val, exists := facts[ConfigKnowledgeDiffusionWindowMonths].(int); exists {
		kd.WindowMonths = val
	}

	if val, exists := facts[identity.FactIdentityDetectorReversedPeopleDict].([]string); exists {
		kd.reversedPeopleDict = val
	}

	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		kd.tickSize = val
	}

	return nil
}

// ConfigureUpstream configures the upstream dependencies.
func (*KnowledgeDiffusionAnalysis) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (kd *KnowledgeDiffusionAnalysis) Flag() string {
	return "knowledge-diffusion"
}

// Description returns the text which explains what the analysis is doing.
func (kd *KnowledgeDiffusionAnalysis) Description() string {
	return "Tracks unique editors per file over time to identify knowledge silos and single-contributor risk areas."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume() calls.
func (kd *KnowledgeDiffusionAnalysis) Initialize(repository *git.Repository) error {
	kd.l = core.NewLogger()
	kd.fileAuthors = map[string]map[int]*authorFileInfo{}

	kd.lastTick = -1
	if kd.WindowMonths <= 0 {
		kd.WindowMonths = 6
	}

	return nil
}

// Consume runs this PipelineItem on the next commit data.
// For each changed file, it records the author as an editor.
func (kd *KnowledgeDiffusionAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	// A merge commit is replayed on every parent branch; its tree changes would otherwise be
	// folded into the diffusion matrix once per parent.
	if core.IsMergeReplica(deps) {
		return noDependencies(), nil
	}

	reader := factReader{facts: deps}
	changes := readFact[object.Changes](&reader, items.DependencyTreeChanges)
	author := readFact[int](&reader, identity.DependencyAuthor)
	tick := readFact[int](&reader, items.DependencyTick)

	if reader.err != nil {
		return nil, reader.err
	}

	for _, change := range changes {
		action, _ := change.Action()
		switch action {
		case merkletrie.Delete:
			// Deleted files: keep history but don't add new editors.
			continue
		case merkletrie.Insert:
			kd.recordEdit(change.To.Name, author, tick)
		case merkletrie.Modify:
			// Handle renames: carry history from old name to new name.
			if change.From.Name != change.To.Name {
				if old, ok := kd.fileAuthors[change.From.Name]; ok {
					kd.fileAuthors[change.To.Name] = old
					delete(kd.fileAuthors, change.From.Name)
				}
			}

			kd.recordEdit(change.To.Name, author, tick)
		}
	}

	kd.lastTick = tick

	return noDependencies(), nil
}

// Finalize returns the result of the analysis.

func (kd *KnowledgeDiffusionAnalysis) Finalize() any {
	files := make(map[string]*KnowledgeDiffusionFileResult, len(kd.fileAuthors))
	distribution := map[int]int{}
	windowTicks := kd.windowTicks()
	cutoffTick := kd.lastTick - windowTicks

	for fileName, authors := range kd.fileAuthors {
		// Build unique editors over time from first-edit ticks.
		firstTicks := make([]int, 0, len(authors))
		for _, info := range authors {
			firstTicks = append(firstTicks, info.FirstTick)
		}

		sort.Ints(firstTicks)

		editorsOverTime := make(map[int]int, len(firstTicks))

		count := 0
		for _, tick := range firstTicks {
			count++
			editorsOverTime[tick] = count
		}

		// Count recent editors.
		recentCount := 0

		authorIndices := make([]int, 0, len(authors))
		for authorID, info := range authors {
			authorIndices = append(authorIndices, authorID)

			if info.LastTick >= cutoffTick {
				recentCount++
			}
		}

		sort.Ints(authorIndices)

		result := &KnowledgeDiffusionFileResult{
			UniqueEditorsCount:    len(authors),
			UniqueEditorsOverTime: editorsOverTime,
			RecentEditorsCount:    recentCount,
			Authors:               authorIndices,
		}
		files[fileName] = result
		distribution[result.UniqueEditorsCount]++
	}

	return KnowledgeDiffusionResult{
		Files:              files,
		Distribution:       distribution,
		WindowMonths:       kd.WindowMonths,
		reversedPeopleDict: kd.reversedPeopleDict,
		tickSize:           kd.tickSize,
	}
}

// Fork clones this pipeline item.
func (kd *KnowledgeDiffusionAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(kd, n)
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
func (kd *KnowledgeDiffusionAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	kdResult, err := requiredResult[KnowledgeDiffusionResult](result)
	if err != nil {
		return err
	}

	if binary {
		return kd.serializeBinary(&kdResult, writer)
	}

	kd.serializeText(&kdResult, writer)

	return nil
}

// Deserialize loads the result from Protocol Buffers blob.
func (kd *KnowledgeDiffusionAnalysis) Deserialize(pbmessage []byte) (any, error) {
	message := pb.KnowledgeDiffusionResults{}

	err := unmarshalAnalysis(pbmessage, &message)
	if err != nil {
		return nil, fmt.Errorf("unmarshal knowledge diffusion result: %w", err)
	}

	files := make(map[string]*KnowledgeDiffusionFileResult, len(message.GetFiles()))
	distribution := make(map[int]int, len(message.GetDistribution()))

	for fileName, pbFile := range message.GetFiles() {
		editorsOverTime := make(map[int]int, len(pbFile.GetUniqueEditorsOverTime()))
		for tick, count := range pbFile.GetUniqueEditorsOverTime() {
			editorsOverTime[int(tick)] = int(count)
		}

		authors := make([]int, len(pbFile.GetAuthors()))
		for i, a := range pbFile.GetAuthors() {
			authors[i] = int(a)
		}

		files[fileName] = &KnowledgeDiffusionFileResult{
			UniqueEditorsCount:    int(pbFile.GetUniqueEditorsCount()),
			UniqueEditorsOverTime: editorsOverTime,
			RecentEditorsCount:    int(pbFile.GetRecentEditorsCount()),
			Authors:               authors,
		}
	}

	for editorCount, fileCount := range message.GetDistribution() {
		distribution[int(editorCount)] = int(fileCount)
	}

	result := KnowledgeDiffusionResult{
		Files:              files,
		Distribution:       distribution,
		WindowMonths:       int(message.GetWindowMonths()),
		reversedPeopleDict: message.GetDevIndex(),
		tickSize:           time.Duration(message.GetTickSize()),
	}

	return result, nil
}

// QualifyPaths prefixes every file key with the repository it was observed in. Distribution is a
// histogram over editor counts and carries no paths, so it is left alone.
func (kd *KnowledgeDiffusionAnalysis) QualifyPaths(result any, repository string) any {
	diffusionResult, err := requiredResult[KnowledgeDiffusionResult](result)
	if err != nil {
		return fmt.Errorf("qualify knowledge diffusion paths: %w", err)
	}

	files := make(map[string]*KnowledgeDiffusionFileResult, len(diffusionResult.Files))
	for path, file := range diffusionResult.Files {
		files[core.QualifyRepositoryPath(repository, path)] = file
	}

	diffusionResult.Files = files

	return diffusionResult
}

// MergeResults combines two KnowledgeDiffusionResult-s together.
func (kd *KnowledgeDiffusionAnalysis) MergeResults(
	firstResult, secondResult any, _, _ *core.CommonAnalysisResult,
) any {
	kdr1, err := requiredResult[KnowledgeDiffusionResult](firstResult)
	if err != nil {
		return err
	}

	kdr2, err := requiredResult[KnowledgeDiffusionResult](secondResult)
	if err != nil {
		return err
	}

	merged := KnowledgeDiffusionResult{
		Files:              make(map[string]*KnowledgeDiffusionFileResult),
		Distribution:       make(map[int]int),
		WindowMonths:       kdr1.WindowMonths,
		reversedPeopleDict: kdr1.reversedPeopleDict,
		tickSize:           kdr1.tickSize,
	}

	// Merge files: union of authors per file.
	maps.Copy(merged.Files, kdr1.Files)

	for name, fileData := range kdr2.Files {
		if existing, ok := merged.Files[name]; ok {
			// Merge author sets: keep the one with more editors.
			if fileData.UniqueEditorsCount > existing.UniqueEditorsCount {
				merged.Files[name] = fileData
			}
		} else {
			merged.Files[name] = fileData
		}
	}

	// Recompute distribution from merged files.
	for _, f := range merged.Files {
		merged.Distribution[f.UniqueEditorsCount]++
	}

	return merged
}

// windowTicks converts the WindowMonths to ticks based on tickSize.
func (kd *KnowledgeDiffusionAnalysis) windowTicks() int {
	if kd.tickSize <= 0 {
		return 0
	}
	// Average month ≈ 30.44 days
	monthDuration := time.Duration(float64(30.44*24) * float64(time.Hour))
	windowDuration := time.Duration(kd.WindowMonths) * monthDuration

	return int(windowDuration / kd.tickSize)
}

func (kd *KnowledgeDiffusionAnalysis) serializeText(result *KnowledgeDiffusionResult, writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "  knowledge_diffusion:")
	_, _ = fmt.Fprintf(writer, "    window_months: %d\n", result.WindowMonths)

	// Sort files for deterministic output.
	fileNames := make([]string, 0, len(result.Files))
	for name := range result.Files {
		fileNames = append(fileNames, name)
	}

	sort.Strings(fileNames)

	_, _ = fmt.Fprintln(writer, "    files:")

	for _, name := range fileNames {
		fileData := result.Files[name]
		_, _ = fmt.Fprintf(writer, "      %s:\n", yaml.SafeString(name))
		_, _ = fmt.Fprintf(writer, "        unique_editors: %d\n", fileData.UniqueEditorsCount)
		_, _ = fmt.Fprintf(writer, "        recent_editors: %d\n", fileData.RecentEditorsCount)

		// Timeline: sort ticks.
		ticks := make([]int, 0, len(fileData.UniqueEditorsOverTime))
		for tick := range fileData.UniqueEditorsOverTime {
			ticks = append(ticks, tick)
		}

		sort.Ints(ticks)
		// Serialize's legacy text path has no error channel.
		_, _ = fmt.Fprint(writer, "        editors_over_time: {")

		for i, tick := range ticks {
			if i > 0 {
				_, _ = fmt.Fprint(writer, ", ")
			}

			_, _ = fmt.Fprintf(writer, "%d: %d", tick, fileData.UniqueEditorsOverTime[tick])
		}

		_, _ = fmt.Fprintln(writer, "}")
	}

	// Distribution histogram.
	_, _ = fmt.Fprintln(writer, "    distribution:")

	editorCounts := make([]int, 0, len(result.Distribution))
	for count := range result.Distribution {
		editorCounts = append(editorCounts, count)
	}

	sort.Ints(editorCounts)

	for _, count := range editorCounts {
		_, _ = fmt.Fprintf(writer, "      %d: %d\n", count, result.Distribution[count])
	}

	_, _ = fmt.Fprintln(writer, "    people:")

	for _, person := range result.reversedPeopleDict {
		_, _ = fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}

	_, _ = fmt.Fprintln(writer, "    tick_size:", int(result.tickSize.Seconds()))
}

func (kd *KnowledgeDiffusionAnalysis) serializeBinary(result *KnowledgeDiffusionResult, writer io.Writer) error {
	windowMonths, err := intToProtoInt32(result.WindowMonths, "knowledge-diffusion window months")
	if err != nil {
		return err
	}

	message := pb.KnowledgeDiffusionResults{
		DevIndex:     result.reversedPeopleDict,
		TickSize:     int64(result.tickSize),
		WindowMonths: windowMonths,
	}

	message.Files, err = knowledgeDiffusionFilesToProto(result.Files)
	if err != nil {
		return err
	}

	message.Distribution, err = knowledgeDiffusionIntMapToProto(
		result.Distribution,
		"knowledge-diffusion distribution editor count",
		"knowledge-diffusion distribution file count",
	)
	if err != nil {
		return err
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal knowledge diffusion result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write knowledge diffusion result: %w", err)
	}

	return nil
}

func knowledgeDiffusionFilesToProto(
	files map[string]*KnowledgeDiffusionFileResult,
) (map[string]*pb.KnowledgeDiffusionFileData, error) {
	result := make(map[string]*pb.KnowledgeDiffusionFileData, len(files))
	for fileName, fileData := range files {
		protoFile, err := knowledgeDiffusionFileToProto(fileData)
		if err != nil {
			return nil, err
		}

		result[fileName] = protoFile
	}

	return result, nil
}

func knowledgeDiffusionFileToProto(fileData *KnowledgeDiffusionFileResult) (*pb.KnowledgeDiffusionFileData, error) {
	uniqueEditors, err := intToProtoInt32(fileData.UniqueEditorsCount, "knowledge-diffusion unique editors")
	if err != nil {
		return nil, err
	}

	recentEditors, err := intToProtoInt32(fileData.RecentEditorsCount, "knowledge-diffusion recent editors")
	if err != nil {
		return nil, err
	}

	editorsOverTime, err := knowledgeDiffusionIntMapToProto(
		fileData.UniqueEditorsOverTime, "knowledge-diffusion tick", "knowledge-diffusion editor count",
	)
	if err != nil {
		return nil, err
	}

	authors, err := knowledgeDiffusionAuthorsToProto(fileData.Authors)
	if err != nil {
		return nil, err
	}

	return &pb.KnowledgeDiffusionFileData{
		UniqueEditorsCount: uniqueEditors, RecentEditorsCount: recentEditors,
		UniqueEditorsOverTime: editorsOverTime, Authors: authors,
	}, nil
}

func knowledgeDiffusionIntMapToProto(values map[int]int, keyField, valueField string) (map[int32]int32, error) {
	result := make(map[int32]int32, len(values))
	for key, value := range values {
		protoKey, err := intToProtoInt32(key, keyField)
		if err != nil {
			return nil, err
		}

		protoValue, err := intToProtoInt32(value, valueField)
		if err != nil {
			return nil, err
		}

		result[protoKey] = protoValue
	}

	return result, nil
}

func knowledgeDiffusionAuthorsToProto(authors []int) ([]int32, error) {
	result := make([]int32, len(authors))
	for index, author := range authors {
		authorID, err := intToProtoInt32(author, "knowledge-diffusion author")
		if err != nil {
			return nil, err
		}

		result[index] = authorID
	}

	return result, nil
}

// recordEdit records that an author edited a file at the given tick.
func (kd *KnowledgeDiffusionAnalysis) recordEdit(fileName string, author, tick int) {
	authors, exists := kd.fileAuthors[fileName]
	if !exists {
		authors = map[int]*authorFileInfo{}
		kd.fileAuthors[fileName] = authors
	}

	info, exists := authors[author]
	if !exists {
		authors[author] = &authorFileInfo{FirstTick: tick, LastTick: tick}
	} else {
		info.LastTick = tick
	}
}

var _ = core.RegisterPipelineItem(&KnowledgeDiffusionAnalysis{})
