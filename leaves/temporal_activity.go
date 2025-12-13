package leaves

import (
	"fmt"
	"io"
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/yaml"
)

// TemporalActivityAnalysis calculates commit or line change activity across temporal dimensions.
// It tracks when developers work by extracting weekday, hour, month, and ISO week from commits.
// This complements DevsAnalysis which tracks activity over project lifetime.
//
// Use cases:
//   - Understand when developers typically work
//   - Identify time zone distribution of team
//   - Detect work pattern changes over time
//   - Assess work-life balance indicators
//
// The analysis can count either commits (--temporal-mode=commits) or
// lines changed (--temporal-mode=lines).
type TemporalActivityAnalysis struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	// Mode determines what to count: "commits" or "lines"
	Mode string

	// activities maps developer index to their temporal activity
	activities map[int]*DeveloperTemporalActivity
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict
	reversedPeopleDict []string

	l core.Logger
}

// DeveloperTemporalActivity stores activity counts across temporal dimensions for one developer.
type DeveloperTemporalActivity struct {
	Weekdays [7]int  // Sunday=0 to Saturday=6
	Hours    [24]int // 0-23
	Months   [12]int // January=0 to December=11
	Weeks    [53]int // ISO week 1-53 stored at index 0-52 (week N stored at index N-1)
}

// TemporalActivityResult is returned by TemporalActivityAnalysis.Finalize().
type TemporalActivityResult struct {
	// Activities maps developer index to temporal activity
	Activities map[int]*DeveloperTemporalActivity
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict
	reversedPeopleDict []string
	// Mode is "commits" or "lines"
	Mode string
}

const (
	// ConfigTemporalActivityMode is the name of the option to set TemporalActivityAnalysis.Mode.
	ConfigTemporalActivityMode = "TemporalActivity.Mode"
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (ta *TemporalActivityAnalysis) Name() string {
	return "TemporalActivity"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (ta *TemporalActivityAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (ta *TemporalActivityAnalysis) Requires() []string {
	return []string{
		identity.DependencyAuthor,
		items.DependencyLineStats,
	}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (ta *TemporalActivityAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{{
		Name:        ConfigTemporalActivityMode,
		Description: "Count commits or lines changed (commits|lines).",
		Flag:        "temporal-mode",
		Type:        core.StringConfigurationOption,
		Default:     "commits",
	}}
	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (ta *TemporalActivityAnalysis) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		ta.l = l
	}
	if val, exists := facts[ConfigTemporalActivityMode].(string); exists {
		if val != "commits" && val != "lines" {
			return fmt.Errorf("invalid temporal mode: %s (must be 'commits' or 'lines')", val)
		}
		ta.Mode = val
	}
	if val, exists := facts[identity.FactIdentityDetectorReversedPeopleDict].([]string); exists {
		ta.reversedPeopleDict = val
	}
	return nil
}

// ConfigureUpstream configures the upstream dependencies.
func (*TemporalActivityAnalysis) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (ta *TemporalActivityAnalysis) Flag() string {
	return "temporal-activity"
}

// Description returns the text which explains what the analysis is doing.
func (ta *TemporalActivityAnalysis) Description() string {
	return "Calculates commit or line change activity by weekday, hour, month, and ISO week."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (ta *TemporalActivityAnalysis) Initialize(repository *git.Repository) error {
	ta.l = core.NewLogger()
	ta.activities = map[int]*DeveloperTemporalActivity{}
	ta.OneShotMergeProcessor.Initialize()
	if ta.Mode == "" {
		ta.Mode = "commits"
	}
	return nil
}

// Consume runs this PipelineItem on the next commit data.
func (ta *TemporalActivityAnalysis) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if !ta.ShouldConsumeCommit(deps) {
		return nil, nil
	}

	commit := deps[core.DependencyCommit].(*object.Commit)
	author := deps[identity.DependencyAuthor].(int)

	// Extract temporal components from commit timestamp
	commitTime := commit.Author.When
	weekday := int(commitTime.Weekday()) // Sunday=0, Monday=1, ..., Saturday=6
	hour := commitTime.Hour()             // 0-23
	month := int(commitTime.Month()) - 1  // January=0, ..., December=11
	_, week := commitTime.ISOWeek()       // ISO week number 1-53

	// Get or create activity tracker for this developer
	activity := ta.activities[author]
	if activity == nil {
		activity = &DeveloperTemporalActivity{}
		ta.activities[author] = activity
	}

	// Calculate value to add
	value := 1 // Default for commits mode
	if ta.Mode == "lines" {
		lineStats := deps[items.DependencyLineStats].(map[object.ChangeEntry]items.LineStats)
		totalLines := 0
		for _, stats := range lineStats {
			totalLines += stats.Added + stats.Removed
		}
		value = totalLines
	}

	// Update temporal counters
	activity.Weekdays[weekday] += value
	activity.Hours[hour] += value
	activity.Months[month] += value
	// Handle ISO week: week can be 1-53, store at index week-1 (0-based indexing)
	// Week 1 → index 0, Week 2 → index 1, ..., Week 53 → index 52
	if week >= 1 && week <= 53 {
		activity.Weeks[week-1] += value
	}

	return nil, nil
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (ta *TemporalActivityAnalysis) Finalize() interface{} {
	return TemporalActivityResult{
		Activities:         ta.activities,
		reversedPeopleDict: ta.reversedPeopleDict,
		Mode:               ta.Mode,
	}
}

// Fork clones this pipeline item.
func (ta *TemporalActivityAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(ta, n)
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (ta *TemporalActivityAnalysis) Serialize(result interface{}, binary bool, writer io.Writer) error {
	temporalResult := result.(TemporalActivityResult)
	if binary {
		return ta.serializeBinary(&temporalResult, writer)
	}
	ta.serializeText(&temporalResult, writer)
	return nil
}

func (ta *TemporalActivityAnalysis) serializeText(result *TemporalActivityResult, writer io.Writer) {
	fmt.Fprintln(writer, "  temporal_activity:")
	fmt.Fprintf(writer, "    mode: %s\n", result.Mode)
	fmt.Fprintln(writer, "    activities:")

	// Sort developers for consistent output
	devs := make([]int, 0, len(result.Activities))
	for dev := range result.Activities {
		devs = append(devs, dev)
	}
	sort.Ints(devs)

	for _, dev := range devs {
		activity := result.Activities[dev]
		devID := dev
		if dev == core.AuthorMissing {
			devID = -1
		}

		fmt.Fprintf(writer, "      %d:\n", devID)

		// Weekdays
		fmt.Fprintf(writer, "        weekdays: [")
		for i, count := range activity.Weekdays {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")

		// Hours
		fmt.Fprintf(writer, "        hours: [")
		for i, count := range activity.Hours {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")

		// Months
		fmt.Fprintf(writer, "        months: [")
		for i, count := range activity.Months {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")

		// Weeks
		fmt.Fprintf(writer, "        weeks: [")
		for i, count := range activity.Weeks {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")
	}

	// Output people dictionary
	fmt.Fprintln(writer, "    people:")
	for _, person := range result.reversedPeopleDict {
		fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}
}

func (ta *TemporalActivityAnalysis) serializeBinary(result *TemporalActivityResult, writer io.Writer) error {
	message := pb.TemporalActivityResults{}
	message.DevIndex = result.reversedPeopleDict
	message.Mode = result.Mode
	message.Activities = make(map[int32]*pb.DeveloperTemporalActivity)

	for dev, activity := range result.Activities {
		devID := int32(dev)
		if dev == core.AuthorMissing {
			devID = -1
		}

		pbActivity := &pb.DeveloperTemporalActivity{
			Weekdays: make([]int32, 7),
			Hours:    make([]int32, 24),
			Months:   make([]int32, 12),
			Weeks:    make([]int32, 53),
		}

		// Copy weekdays
		for i, count := range activity.Weekdays {
			pbActivity.Weekdays[i] = int32(count)
		}

		// Copy hours
		for i, count := range activity.Hours {
			pbActivity.Hours[i] = int32(count)
		}

		// Copy months
		for i, count := range activity.Months {
			pbActivity.Months[i] = int32(count)
		}

		// Copy weeks
		for i, count := range activity.Weeks {
			pbActivity.Weeks[i] = int32(count)
		}

		message.Activities[devID] = pbActivity
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}

func init() {
	core.Registry.Register(&TemporalActivityAnalysis{})
}

// MergeResults combines two TemporalActivityResult-s together.
func (ta *TemporalActivityAnalysis) MergeResults(
	r1, r2 interface{}, c1, c2 *core.CommonAnalysisResult,
) interface{} {
	tar1 := r1.(TemporalActivityResult)
	tar2 := r2.(TemporalActivityResult)
	
	if tar1.Mode != tar2.Mode {
		return fmt.Errorf("mismatching modes (r1: %s, r2: %s)", tar1.Mode, tar2.Mode)
	}
	
	merged := TemporalActivityResult{
		Activities:         make(map[int]*DeveloperTemporalActivity),
		reversedPeopleDict: tar1.reversedPeopleDict, // Use first dict, should be same
		Mode:               tar1.Mode,
	}
	
	// Merge activities from both results
	allDevs := make(map[int]bool)
	for dev := range tar1.Activities {
		allDevs[dev] = true
	}
	for dev := range tar2.Activities {
		allDevs[dev] = true
	}
	
	for dev := range allDevs {
		mergedActivity := &DeveloperTemporalActivity{}
		
		// Add activities from r1
		if activity1, exists := tar1.Activities[dev]; exists {
			for i := range mergedActivity.Weekdays {
				mergedActivity.Weekdays[i] += activity1.Weekdays[i]
			}
			for i := range mergedActivity.Hours {
				mergedActivity.Hours[i] += activity1.Hours[i]
			}
			for i := range mergedActivity.Months {
				mergedActivity.Months[i] += activity1.Months[i]
			}
			for i := range mergedActivity.Weeks {
				mergedActivity.Weeks[i] += activity1.Weeks[i]
			}
		}
		
		// Add activities from r2
		if activity2, exists := tar2.Activities[dev]; exists {
			for i := range mergedActivity.Weekdays {
				mergedActivity.Weekdays[i] += activity2.Weekdays[i]
			}
			for i := range mergedActivity.Hours {
				mergedActivity.Hours[i] += activity2.Hours[i]
			}
			for i := range mergedActivity.Months {
				mergedActivity.Months[i] += activity2.Months[i]
			}
			for i := range mergedActivity.Weeks {
				mergedActivity.Weeks[i] += activity2.Weeks[i]
			}
		}
		
		merged.Activities[dev] = mergedActivity
	}
	
	return merged
}
