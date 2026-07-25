package leaves

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/join"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/yaml"
)

var (
	errTickSizeUnspecified      = errors.New("tick size must be specified")
	errDevsMismatchingTickSizes = errors.New("mismatching tick sizes")
)

// DevsAnalysis calculates the number of commits through time per developer.
// It also records the numbers of added, deleted and changed lines through time per developer.
// Those numbers are additionally measured per language.
type DevsAnalysis struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	// ConsiderEmptyCommits indicates whether empty commits (e.g., merges) should be taken
	// into account.
	ConsiderEmptyCommits bool

	// ticks maps ticks to developers to stats
	ticks map[int]map[int]*DevTick
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict
	reversedPeopleDict []string
	// TickSize references TicksSinceStart.TickSize
	tickSize time.Duration

	l core.Logger
}

// DevsResult is returned by DevsAnalysis.Finalize() and carries the daily statistics
// per developer.
type DevsResult struct {
	// Ticks is <tick index> -> <developer index> -> daily stats
	Ticks map[int]map[int]*DevTick

	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict
	reversedPeopleDict []string
	// TickSize references TicksSinceStart.TickSize
	tickSize time.Duration
}

// DevTick is the statistics for a development tick and a particular developer.
type DevTick struct {
	items.LineStats

	// Commits is the number of commits made by a particular developer in a particular tick.
	Commits int

	// LanguagesDetection carries fine-grained line stats per programming language.
	Languages map[string]items.LineStats
}

const (
	// ConfigDevsConsiderEmptyCommits is the name of the option to set DevsAnalysis.ConsiderEmptyCommits.
	ConfigDevsConsiderEmptyCommits = "Devs.ConsiderEmptyCommits"
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (devs *DevsAnalysis) Name() string {
	return "Devs"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (devs *DevsAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (devs *DevsAnalysis) Requires() []string {
	return []string{
		identity.DependencyAuthor, items.DependencyTreeChanges, items.DependencyTick,
		items.DependencyLanguages, items.DependencyLineStats,
	}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (devs *DevsAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{{
		Name:        ConfigDevsConsiderEmptyCommits,
		Description: "Take into account empty commits such as trivial merges.",
		Flag:        "empty-commits",
		Type:        core.BoolConfigurationOption,
		Default:     false,
	}}

	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (devs *DevsAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		devs.l = l
	}

	if val, exists := facts[ConfigDevsConsiderEmptyCommits].(bool); exists {
		devs.ConsiderEmptyCommits = val
	}

	if val, exists := facts[identity.FactIdentityDetectorReversedPeopleDict].([]string); exists {
		devs.reversedPeopleDict = val
	}

	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		devs.tickSize = val
	}

	return nil
}

func (*DevsAnalysis) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (devs *DevsAnalysis) Flag() string {
	return "devs"
}

// Description returns the text which explains what the analysis is doing.
func (devs *DevsAnalysis) Description() string {
	return "Calculates the number of commits, added, removed and changed lines per developer through time."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (devs *DevsAnalysis) Initialize(repository *git.Repository) error {
	if devs.tickSize == 0 {
		return errTickSizeUnspecified
	}

	devs.l = core.NewLogger()
	devs.ticks = map[int]map[int]*DevTick{}
	devs.OneShotMergeProcessor.Initialize()

	return nil
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (devs *DevsAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	if !devs.ShouldConsumeCommit(deps) {
		return noDependencies(), nil
	}

	author := deps[identity.DependencyAuthor].(int)

	treeDiff := deps[items.DependencyTreeChanges].(object.Changes)
	if len(treeDiff) == 0 && !devs.ConsiderEmptyCommits {
		return noDependencies(), nil
	}

	tick := deps[items.DependencyTick].(int)

	devstick, exists := devs.ticks[tick]
	if !exists {
		devstick = map[int]*DevTick{}
		devs.ticks[tick] = devstick
	}

	developerTick, exists := devstick[author]
	if !exists {
		developerTick = &DevTick{Languages: map[string]items.LineStats{}}
		devstick[author] = developerTick
	}

	developerTick.Commits++
	langs := deps[items.DependencyLanguages].(map[plumbing.Hash]string)

	lineStats := deps[items.DependencyLineStats].(map[object.ChangeEntry]items.LineStats)
	for changeEntry, stats := range lineStats {
		developerTick.Added += stats.Added
		developerTick.Removed += stats.Removed
		developerTick.Changed += stats.Changed
		lang := langs[changeEntry.TreeEntry.Hash]
		langStats := developerTick.Languages[lang]
		developerTick.Languages[lang] = items.LineStats{
			Added:   langStats.Added + stats.Added,
			Removed: langStats.Removed + stats.Removed,
			Changed: langStats.Changed + stats.Changed,
		}
	}

	return noDependencies(), nil
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.

func (devs *DevsAnalysis) Finalize() any {
	return DevsResult{
		Ticks:              devs.ticks,
		reversedPeopleDict: devs.reversedPeopleDict,
		tickSize:           devs.tickSize,
	}
}

// Fork clones this pipeline item.
func (devs *DevsAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(devs, n)
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (devs *DevsAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	devsResult := result.(DevsResult)
	if binary {
		return devs.serializeBinary(&devsResult, writer)
	}

	devs.serializeText(&devsResult, writer)

	return nil
}

// Deserialize converts the specified protobuf bytes to DevsResult.
func (devs *DevsAnalysis) Deserialize(pbmessage []byte) (any, error) {
	message := pb.DevsAnalysisResults{}

	err := proto.Unmarshal(pbmessage, &message)
	if err != nil {
		return nil, fmt.Errorf("unmarshal developers result: %w", err)
	}

	ticks := map[int]map[int]*DevTick{}

	for tick, dd := range message.GetTicks() {
		rdd := map[int]*DevTick{}
		ticks[int(tick)] = rdd

		for dev, stats := range dd.GetDevs() {
			if dev == -1 {
				dev = core.AuthorMissing
			}

			languages := map[string]items.LineStats{}

			rdd[int(dev)] = &DevTick{
				Commits: int(stats.GetCommits()),
				LineStats: items.LineStats{
					Added:   int(stats.GetStats().GetAdded()),
					Removed: int(stats.GetStats().GetRemoved()),
					Changed: int(stats.GetStats().GetChanged()),
				},
				Languages: languages,
			}
			for lang, ls := range stats.GetLanguages() {
				languages[lang] = items.LineStats{
					Added:   int(ls.GetAdded()),
					Removed: int(ls.GetRemoved()),
					Changed: int(ls.GetChanged()),
				}
			}
		}
	}

	result := DevsResult{
		Ticks:              ticks,
		reversedPeopleDict: message.GetDevIndex(),
		tickSize:           time.Duration(message.GetTickSize()),
	}

	return result, nil
}

// MergeResults combines two DevsAnalysis-es together.
func (devs *DevsAnalysis) MergeResults(result1, result2 any,
	commonResult1, commonResult2 *core.CommonAnalysisResult,
) any {
	cr1 := result1.(DevsResult)

	cr2 := result2.(DevsResult)
	if cr1.tickSize != cr2.tickSize {
		return fmt.Errorf("%w (r1: %d, r2: %d) received",
			errDevsMismatchingTickSizes, cr1.tickSize, cr2.tickSize)
	}

	firstStart := items.FloorTime(commonResult1.BeginTimeAsTime(), cr1.tickSize)
	secondStart := items.FloorTime(commonResult2.BeginTimeAsTime(), cr2.tickSize)

	startTime := firstStart
	if secondStart.Before(startTime) {
		startTime = secondStart
	}

	offset1 := int(firstStart.Sub(startTime) / cr1.tickSize)
	offset2 := int(secondStart.Sub(startTime) / cr2.tickSize)

	merged := DevsResult{tickSize: cr1.tickSize}
	var mergedIndex map[string]join.JoinedIndex
	mergedIndex, merged.reversedPeopleDict = join.PeopleIdentities(
		cr1.reversedPeopleDict, cr2.reversedPeopleDict,
	)
	newticks := map[int]map[int]*DevTick{}

	merged.Ticks = newticks
	mergeDevTicks(newticks, cr1, offset1, mergedIndex)
	mergeDevTicks(newticks, cr2, offset2, mergedIndex)

	return merged
}

func mergeDevTicks(
	target map[int]map[int]*DevTick,
	source DevsResult,
	offset int,
	peopleIndex map[string]join.JoinedIndex,
) {
	for tick, developers := range source.Ticks {
		targetTick := tick + offset
		if target[targetTick] == nil {
			target[targetTick] = map[int]*DevTick{}
		}

		for developer, stats := range developers {
			targetDeveloper := developer
			if developer != core.AuthorMissing {
				targetDeveloper = peopleIndex[source.reversedPeopleDict[developer]].Final
			}

			mergeDevTick(target[targetTick], targetDeveloper, stats)
		}
	}
}

func mergeDevTick(target map[int]*DevTick, developer int, source *DevTick) {
	stats := target[developer]
	if stats == nil {
		stats = &DevTick{Languages: map[string]items.LineStats{}}
		target[developer] = stats
	}

	stats.Commits += source.Commits
	stats.Added += source.Added
	stats.Removed += source.Removed

	stats.Changed += source.Changed
	for language, lines := range source.Languages {
		previous := stats.Languages[language]
		stats.Languages[language] = items.LineStats{
			Added: previous.Added + lines.Added, Removed: previous.Removed + lines.Removed,
			Changed: previous.Changed + lines.Changed,
		}
	}
}

func (devs *DevsAnalysis) serializeText(result *DevsResult, writer io.Writer) {
	fmt.Fprintln(writer, "  ticks:")

	ticks := make([]int, len(result.Ticks))
	{
		i := 0
		for tick := range result.Ticks {
			ticks[i] = tick
			i++
		}
	}

	sort.Ints(ticks)

	for _, tick := range ticks {
		fmt.Fprintf(writer, "    %d:\n", tick)
		rtick := result.Ticks[tick]
		devseq := make([]int, len(rtick))
		{
			i := 0
			for dev := range rtick {
				devseq[i] = dev
				i++
			}
		}

		sort.Ints(devseq)

		for _, dev := range devseq {
			stats := rtick[dev]
			if dev == core.AuthorMissing {
				dev = -1
			}
			var langs []string

			for lang, languageStats := range stats.Languages {
				if lang == "" {
					lang = "none"
				}

				langs = append(langs,
					fmt.Sprintf("%s: [%d, %d, %d]", lang, languageStats.Added, languageStats.Removed, languageStats.Changed))
			}

			sort.Strings(langs)
			fmt.Fprintf(writer, "      %d: [%d, %d, %d, %d, {%s}]\n",
				dev, stats.Commits, stats.Added, stats.Removed, stats.Changed,
				strings.Join(langs, ", "))
		}
	}

	fmt.Fprintln(writer, "  people:")

	for _, person := range result.reversedPeopleDict {
		fmt.Fprintf(writer, "  - %s\n", yaml.SafeString(person))
	}

	fmt.Fprintln(writer, "  tick_size:", int(result.tickSize.Seconds()))
}

func (devs *DevsAnalysis) serializeBinary(result *DevsResult, writer io.Writer) error {
	message := pb.DevsAnalysisResults{}
	message.DevIndex = result.reversedPeopleDict
	message.TickSize = int64(result.tickSize)

	message.Ticks = map[int32]*pb.TickDevs{}
	for tick, devs := range result.Ticks {
		tickID, err := intToProtoInt32(tick, "developer statistics tick")
		if err != nil {
			return err
		}

		tickDevs := &pb.TickDevs{}
		message.Ticks[tickID] = tickDevs
		tickDevs.Devs = map[int32]*pb.DevTick{}

		for dev, stats := range devs {
			if dev == core.AuthorMissing {
				dev = -1
			}

			developerID, err := intToProtoInt32(dev, "developer statistics author")
			if err != nil {
				return err
			}

			commits, err := intToProtoInt32(stats.Commits, "developer commit count")
			if err != nil {
				return err
			}

			lineStats, err := devLineStatsToProto(stats.Added, stats.Changed, stats.Removed)
			if err != nil {
				return err
			}

			languages := map[string]*pb.LineStats{}

			tickDevs.Devs[developerID] = &pb.DevTick{
				Commits:   commits,
				Stats:     lineStats,
				Languages: languages,
			}
			for lang, ls := range stats.Languages {
				languageStats, err := devLineStatsToProto(ls.Added, ls.Changed, ls.Removed)
				if err != nil {
					return err
				}

				languages[lang] = languageStats
			}
		}
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal developers result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write developers result: %w", err)
	}

	return nil
}

func devLineStatsToProto(added, changed, removed int) (*pb.LineStats, error) {
	pbAdded, err := intToProtoInt32(added, "developer added-line count")
	if err != nil {
		return nil, err
	}

	pbChanged, err := intToProtoInt32(changed, "developer changed-line count")
	if err != nil {
		return nil, err
	}

	pbRemoved, err := intToProtoInt32(removed, "developer removed-line count")
	if err != nil {
		return nil, err
	}

	return &pb.LineStats{Added: pbAdded, Changed: pbChanged, Removed: pbRemoved}, nil
}

// GetTickSize returns the tick size used to generate this devs analysis result.
func (dr DevsResult) GetTickSize() time.Duration {
	return dr.tickSize
}

// GetIdentities returns the list of developer identities used to generate this devs analysis result.
// The format is |-joined keys, see internals/plumbing/identity for details.
func (dr DevsResult) GetIdentities() []string {
	return dr.reversedPeopleDict
}

var _ = core.RegisterPipelineItem(&DevsAnalysis{})
