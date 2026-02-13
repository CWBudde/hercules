package leaves

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/yaml"
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
func (kd *KnowledgeDiffusionAnalysis) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		kd.l = l
	}
	if val, exists := facts[ConfigKnowledgeDiffusionWindowMonths]; exists {
		kd.WindowMonths = val.(int)
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
func (*KnowledgeDiffusionAnalysis) ConfigureUpstream(facts map[string]interface{}) error {
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
func (kd *KnowledgeDiffusionAnalysis) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	changes := deps[items.DependencyTreeChanges].(object.Changes)
	author := deps[identity.DependencyAuthor].(int)
	tick := deps[items.DependencyTick].(int)

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
	return nil, nil
}

// recordEdit records that an author edited a file at the given tick.
func (kd *KnowledgeDiffusionAnalysis) recordEdit(fileName string, author int, tick int) {
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

// Finalize returns the result of the analysis.
func (kd *KnowledgeDiffusionAnalysis) Finalize() interface{} {
	files := make(map[string]*KnowledgeDiffusionFileResult, len(kd.fileAuthors))
	distribution := map[int]int{}
	windowTicks := kd.windowTicks()
	cutoffTick := kd.lastTick - windowTicks

	for fileName, authors := range kd.fileAuthors {
		// Build unique editors over time from first-edit ticks.
		type tickEntry struct {
			tick  int
			count int // cumulative count at this tick
		}
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
func (kd *KnowledgeDiffusionAnalysis) Serialize(result interface{}, binary bool, writer io.Writer) error {
	kdResult := result.(KnowledgeDiffusionResult)
	if binary {
		return kd.serializeBinary(&kdResult, writer)
	}
	kd.serializeText(&kdResult, writer)
	return nil
}

// Deserialize loads the result from Protocol Buffers blob.
func (kd *KnowledgeDiffusionAnalysis) Deserialize(pbmessage []byte) (interface{}, error) {
	message := pb.KnowledgeDiffusionResults{}
	err := proto.Unmarshal(pbmessage, &message)
	if err != nil {
		return nil, err
	}

	files := make(map[string]*KnowledgeDiffusionFileResult, len(message.Files))
	distribution := make(map[int]int, len(message.Distribution))

	for fileName, pbFile := range message.Files {
		editorsOverTime := make(map[int]int, len(pbFile.UniqueEditorsOverTime))
		for tick, count := range pbFile.UniqueEditorsOverTime {
			editorsOverTime[int(tick)] = int(count)
		}
		authors := make([]int, len(pbFile.Authors))
		for i, a := range pbFile.Authors {
			authors[i] = int(a)
		}
		files[fileName] = &KnowledgeDiffusionFileResult{
			UniqueEditorsCount:    int(pbFile.UniqueEditorsCount),
			UniqueEditorsOverTime: editorsOverTime,
			RecentEditorsCount:    int(pbFile.RecentEditorsCount),
			Authors:               authors,
		}
	}

	for editorCount, fileCount := range message.Distribution {
		distribution[int(editorCount)] = int(fileCount)
	}

	result := KnowledgeDiffusionResult{
		Files:              files,
		Distribution:       distribution,
		WindowMonths:       int(message.WindowMonths),
		reversedPeopleDict: message.DevIndex,
		tickSize:           time.Duration(message.TickSize),
	}
	return result, nil
}

func (kd *KnowledgeDiffusionAnalysis) serializeText(result *KnowledgeDiffusionResult, writer io.Writer) {
	fmt.Fprintln(writer, "  knowledge_diffusion:")
	fmt.Fprintf(writer, "    window_months: %d\n", result.WindowMonths)

	// Sort files for deterministic output.
	fileNames := make([]string, 0, len(result.Files))
	for name := range result.Files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)

	fmt.Fprintln(writer, "    files:")
	for _, name := range fileNames {
		f := result.Files[name]
		fmt.Fprintf(writer, "      %s:\n", yaml.SafeString(name))
		fmt.Fprintf(writer, "        unique_editors: %d\n", f.UniqueEditorsCount)
		fmt.Fprintf(writer, "        recent_editors: %d\n", f.RecentEditorsCount)

		// Timeline: sort ticks.
		ticks := make([]int, 0, len(f.UniqueEditorsOverTime))
		for tick := range f.UniqueEditorsOverTime {
			ticks = append(ticks, tick)
		}
		sort.Ints(ticks)
		fmt.Fprint(writer, "        editors_over_time: {")
		for i, tick := range ticks {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d: %d", tick, f.UniqueEditorsOverTime[tick])
		}
		fmt.Fprintln(writer, "}")
	}

	// Distribution histogram.
	fmt.Fprintln(writer, "    distribution:")
	editorCounts := make([]int, 0, len(result.Distribution))
	for count := range result.Distribution {
		editorCounts = append(editorCounts, count)
	}
	sort.Ints(editorCounts)
	for _, count := range editorCounts {
		fmt.Fprintf(writer, "      %d: %d\n", count, result.Distribution[count])
	}

	fmt.Fprintln(writer, "    people:")
	for _, person := range result.reversedPeopleDict {
		fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}
	fmt.Fprintln(writer, "    tick_size:", int(result.tickSize.Seconds()))
}

func (kd *KnowledgeDiffusionAnalysis) serializeBinary(result *KnowledgeDiffusionResult, writer io.Writer) error {
	message := pb.KnowledgeDiffusionResults{
		DevIndex:     result.reversedPeopleDict,
		TickSize:     int64(result.tickSize),
		WindowMonths: int32(result.WindowMonths),
	}

	message.Files = make(map[string]*pb.KnowledgeDiffusionFileData, len(result.Files))
	for fileName, f := range result.Files {
		pbFile := &pb.KnowledgeDiffusionFileData{
			UniqueEditorsCount:    int32(f.UniqueEditorsCount),
			RecentEditorsCount:    int32(f.RecentEditorsCount),
			UniqueEditorsOverTime: make(map[int32]int32, len(f.UniqueEditorsOverTime)),
			Authors:               make([]int32, len(f.Authors)),
		}
		for tick, count := range f.UniqueEditorsOverTime {
			pbFile.UniqueEditorsOverTime[int32(tick)] = int32(count)
		}
		for i, a := range f.Authors {
			pbFile.Authors[i] = int32(a)
		}
		message.Files[fileName] = pbFile
	}

	message.Distribution = make(map[int32]int32, len(result.Distribution))
	for editorCount, fileCount := range result.Distribution {
		message.Distribution[int32(editorCount)] = int32(fileCount)
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}

// MergeResults combines two KnowledgeDiffusionResult-s together.
func (kd *KnowledgeDiffusionAnalysis) MergeResults(
	r1, r2 interface{}, c1, c2 *core.CommonAnalysisResult,
) interface{} {
	kdr1 := r1.(KnowledgeDiffusionResult)
	kdr2 := r2.(KnowledgeDiffusionResult)

	merged := KnowledgeDiffusionResult{
		Files:              make(map[string]*KnowledgeDiffusionFileResult),
		Distribution:       make(map[int]int),
		WindowMonths:       kdr1.WindowMonths,
		reversedPeopleDict: kdr1.reversedPeopleDict,
		tickSize:           kdr1.tickSize,
	}

	// Merge files: union of authors per file.
	for name, f := range kdr1.Files {
		merged.Files[name] = f
	}
	for name, f := range kdr2.Files {
		if existing, ok := merged.Files[name]; ok {
			// Merge author sets: keep the one with more editors.
			if f.UniqueEditorsCount > existing.UniqueEditorsCount {
				merged.Files[name] = f
			}
		} else {
			merged.Files[name] = f
		}
	}

	// Recompute distribution from merged files.
	for _, f := range merged.Files {
		merged.Distribution[f.UniqueEditorsCount]++
	}

	return merged
}

func init() {
	core.Registry.Register(&KnowledgeDiffusionAnalysis{})
}
