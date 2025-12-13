package leaves

import (
	"bytes"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/meko-christian/hercules/internal/core"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/test"
	"github.com/stretchr/testify/assert"
)

func TestTemporalActivityMeta(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	assert.Equal(t, ta.Name(), "TemporalActivity")
	assert.Len(t, ta.Provides(), 0)
	required := [...]string{identity.DependencyAuthor, items.DependencyLineStats}
	for _, name := range required {
		assert.Contains(t, ta.Requires(), name)
	}
	opts := ta.ListConfigurationOptions()
	assert.Len(t, opts, 1)
	assert.Equal(t, opts[0].Name, ConfigTemporalActivityMode)
	assert.Equal(t, opts[0].Flag, "temporal-mode")
	assert.Equal(t, opts[0].Default, "commits")
	assert.Equal(t, ta.Flag(), "temporal-activity")
	assert.Equal(t, ta.Description(), "Calculates commit or line change activity by weekday, hour, month, and ISO week.")
}

func TestTemporalActivityRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&TemporalActivityAnalysis{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, summoned[0].Name(), "TemporalActivity")
	leaves := core.Registry.GetLeaves()
	matched := false
	for _, tp := range leaves {
		if tp.Flag() == (&TemporalActivityAnalysis{}).Flag() {
			matched = true
			break
		}
	}
	assert.True(t, matched)
}

func TestTemporalActivityConfigure(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	facts := map[string]interface{}{}
	facts[identity.FactIdentityDetectorReversedPeopleDict] = []string{"Alice", "Bob"}
	facts[ConfigTemporalActivityMode] = "lines"
	logger := core.NewLogger()
	facts[core.ConfigLogger] = logger

	assert.Nil(t, ta.Configure(facts))
	assert.Equal(t, ta.reversedPeopleDict, []string{"Alice", "Bob"})
	assert.Equal(t, ta.Mode, "lines")
	assert.Equal(t, logger, ta.l)
}

func TestTemporalActivityConfigureInvalidMode(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	facts := map[string]interface{}{}
	facts[ConfigTemporalActivityMode] = "invalid"

	err := ta.Configure(facts)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "invalid temporal mode")
}

func TestTemporalActivityInitialize(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	assert.Nil(t, ta.Initialize(test.Repository))
	assert.NotNil(t, ta.activities)
	assert.Equal(t, "commits", ta.Mode)
}

func TestTemporalActivityConsumeCommitsMode(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.Mode = "commits"
	assert.Nil(t, ta.Initialize(test.Repository))

	deps := map[string]interface{}{}
	deps[core.DependencyIsMerge] = false
	deps[identity.DependencyAuthor] = 0

	// Create a commit with known timestamp
	// Tuesday, 2023-01-03 14:30:00 UTC
	commitTime := time.Date(2023, time.January, 3, 14, 30, 0, 0, time.UTC)
	commit := &object.Commit{
		Author: object.Signature{
			When: commitTime,
		},
	}
	deps[core.DependencyCommit] = commit

	// Add empty line stats for commits mode
	deps[items.DependencyLineStats] = map[object.ChangeEntry]items.LineStats{}

	result, err := ta.Consume(deps)
	assert.Nil(t, err)
	assert.Nil(t, result)

	// Verify activity was recorded
	assert.Len(t, ta.activities, 1)
	activity := ta.activities[0]
	assert.NotNil(t, activity)

	// Tuesday is weekday 2
	assert.Equal(t, 1, activity.Weekdays[2])
	// Hour 14 (2pm)
	assert.Equal(t, 1, activity.Hours[14])
	// January is month 0
	assert.Equal(t, 1, activity.Months[0])
	// Week 1 of 2023 (stored at index 0 because of week-1 indexing)
	_, week := commitTime.ISOWeek()
	assert.Equal(t, 1, activity.Weeks[week-1])
}

func TestTemporalActivityConsumeLinesMode(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.Mode = "lines"
	assert.Nil(t, ta.Initialize(test.Repository))

	deps := map[string]interface{}{}
	deps[core.DependencyIsMerge] = false
	deps[identity.DependencyAuthor] = 1

	// Friday, 2023-06-16 09:00:00 UTC
	commitTime := time.Date(2023, time.June, 16, 9, 0, 0, 0, time.UTC)
	commit := &object.Commit{
		Author: object.Signature{
			When: commitTime,
		},
	}
	deps[core.DependencyCommit] = commit

	// Add line stats: 50 added, 20 removed = 70 total
	lineStats := map[object.ChangeEntry]items.LineStats{
		{}: {Added: 50, Removed: 20, Changed: 10},
	}
	deps[items.DependencyLineStats] = lineStats

	result, err := ta.Consume(deps)
	assert.Nil(t, err)
	assert.Nil(t, result)

	// Verify activity was recorded with line counts
	assert.Len(t, ta.activities, 1)
	activity := ta.activities[1]
	assert.NotNil(t, activity)

	// Friday is weekday 5
	assert.Equal(t, 70, activity.Weekdays[5])
	// Hour 9 (9am)
	assert.Equal(t, 70, activity.Hours[9])
	// June is month 5
	assert.Equal(t, 70, activity.Months[5])
	// Week 24 of 2023 (stored at index week-1)
	_, week := commitTime.ISOWeek()
	assert.Equal(t, 70, activity.Weeks[week-1])
}

func TestTemporalActivityMultipleCommits(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.Mode = "commits"
	assert.Nil(t, ta.Initialize(test.Repository))

	// Simulate multiple commits from same developer on different days/times
	commits := []struct {
		author int
		time   time.Time
	}{
		{0, time.Date(2023, time.January, 2, 10, 0, 0, 0, time.UTC)},  // Monday 10am
		{0, time.Date(2023, time.January, 2, 15, 0, 0, 0, time.UTC)},  // Monday 3pm
		{0, time.Date(2023, time.January, 3, 10, 0, 0, 0, time.UTC)},  // Tuesday 10am
		{1, time.Date(2023, time.January, 4, 14, 0, 0, 0, time.UTC)},  // Wednesday 2pm (different dev)
	}

	for _, c := range commits {
		deps := map[string]interface{}{}
		deps[core.DependencyIsMerge] = false
		deps[identity.DependencyAuthor] = c.author
		deps[core.DependencyCommit] = &object.Commit{
			Author: object.Signature{When: c.time},
		}
		deps[items.DependencyLineStats] = map[object.ChangeEntry]items.LineStats{}

		result, err := ta.Consume(deps)
		assert.Nil(t, err)
		assert.Nil(t, result)
	}

	// Verify dev 0 has 3 commits
	assert.Len(t, ta.activities, 2)
	dev0 := ta.activities[0]
	assert.Equal(t, 2, dev0.Weekdays[1]) // Monday
	assert.Equal(t, 1, dev0.Weekdays[2]) // Tuesday
	assert.Equal(t, 2, dev0.Hours[10])   // 10am
	assert.Equal(t, 1, dev0.Hours[15])   // 3pm

	// Verify dev 1 has 1 commit
	dev1 := ta.activities[1]
	assert.Equal(t, 1, dev1.Weekdays[3]) // Wednesday
	assert.Equal(t, 1, dev1.Hours[14])   // 2pm
}

func TestTemporalActivityWeekdayBoundaries(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.Mode = "commits"
	assert.Nil(t, ta.Initialize(test.Repository))

	// Test all 7 weekdays
	weekdays := []time.Time{
		time.Date(2023, time.January, 1, 12, 0, 0, 0, time.UTC),  // Sunday
		time.Date(2023, time.January, 2, 12, 0, 0, 0, time.UTC),  // Monday
		time.Date(2023, time.January, 3, 12, 0, 0, 0, time.UTC),  // Tuesday
		time.Date(2023, time.January, 4, 12, 0, 0, 0, time.UTC),  // Wednesday
		time.Date(2023, time.January, 5, 12, 0, 0, 0, time.UTC),  // Thursday
		time.Date(2023, time.January, 6, 12, 0, 0, 0, time.UTC),  // Friday
		time.Date(2023, time.January, 7, 12, 0, 0, 0, time.UTC),  // Saturday
	}

	for _, commitTime := range weekdays {
		deps := map[string]interface{}{}
		deps[core.DependencyIsMerge] = false
		deps[identity.DependencyAuthor] = 0
		deps[core.DependencyCommit] = &object.Commit{
			Author: object.Signature{When: commitTime},
		}
		deps[items.DependencyLineStats] = map[object.ChangeEntry]items.LineStats{}

		result, err := ta.Consume(deps)
		assert.Nil(t, err)
		assert.Nil(t, result)
	}

	// Verify all weekdays have 1 commit
	activity := ta.activities[0]
	for i := 0; i < 7; i++ {
		assert.Equal(t, 1, activity.Weekdays[i], "Weekday %d should have 1 commit", i)
	}
}

func TestTemporalActivityHourBoundaries(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.Mode = "commits"
	assert.Nil(t, ta.Initialize(test.Repository))

	// Test boundary hours: 0 (midnight) and 23 (11pm)
	hours := []int{0, 1, 12, 22, 23}
	for _, hour := range hours {
		commitTime := time.Date(2023, time.January, 1, hour, 0, 0, 0, time.UTC)
		deps := map[string]interface{}{}
		deps[core.DependencyIsMerge] = false
		deps[identity.DependencyAuthor] = 0
		deps[core.DependencyCommit] = &object.Commit{
			Author: object.Signature{When: commitTime},
		}
		deps[items.DependencyLineStats] = map[object.ChangeEntry]items.LineStats{}

		result, err := ta.Consume(deps)
		assert.Nil(t, err)
		assert.Nil(t, result)
	}

	// Verify hours
	activity := ta.activities[0]
	assert.Equal(t, 1, activity.Hours[0])
	assert.Equal(t, 1, activity.Hours[1])
	assert.Equal(t, 1, activity.Hours[12])
	assert.Equal(t, 1, activity.Hours[22])
	assert.Equal(t, 1, activity.Hours[23])
}

func TestTemporalActivityMonthBoundaries(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.Mode = "commits"
	assert.Nil(t, ta.Initialize(test.Repository))

	// Test all 12 months
	for month := time.January; month <= time.December; month++ {
		commitTime := time.Date(2023, month, 15, 12, 0, 0, 0, time.UTC)
		deps := map[string]interface{}{}
		deps[core.DependencyIsMerge] = false
		deps[identity.DependencyAuthor] = 0
		deps[core.DependencyCommit] = &object.Commit{
			Author: object.Signature{When: commitTime},
		}
		deps[items.DependencyLineStats] = map[object.ChangeEntry]items.LineStats{}

		result, err := ta.Consume(deps)
		assert.Nil(t, err)
		assert.Nil(t, result)
	}

	// Verify all months have 1 commit
	activity := ta.activities[0]
	for i := 0; i < 12; i++ {
		assert.Equal(t, 1, activity.Months[i], "Month %d should have 1 commit", i)
	}
}

func TestTemporalActivityISOWeekEdgeCases(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.Mode = "commits"
	assert.Nil(t, ta.Initialize(test.Repository))

	// Test week 1 and week 53 (leap week year)
	// 2020 is a leap week year (has week 53)
	testCases := []time.Time{
		time.Date(2020, time.January, 6, 12, 0, 0, 0, time.UTC),   // Week 2
		time.Date(2020, time.December, 28, 12, 0, 0, 0, time.UTC), // Week 53
	}

	for _, commitTime := range testCases {
		deps := map[string]interface{}{}
		deps[core.DependencyIsMerge] = false
		deps[identity.DependencyAuthor] = 0
		deps[core.DependencyCommit] = &object.Commit{
			Author: object.Signature{When: commitTime},
		}
		deps[items.DependencyLineStats] = map[object.ChangeEntry]items.LineStats{}

		result, err := ta.Consume(deps)
		assert.Nil(t, err)
		assert.Nil(t, result)
	}

	// Verify weeks (week N stored at index N-1)
	activity := ta.activities[0]
	assert.Equal(t, 1, activity.Weeks[1])  // Week 2 stored at index 1
	// Verify week 53 is stored correctly
	_, week53 := testCases[1].ISOWeek()
	assert.Equal(t, 53, week53)
	assert.Equal(t, 1, activity.Weeks[52])  // Week 53 stored at index 52
}

func TestTemporalActivityFinalize(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.Mode = "commits"
	ta.reversedPeopleDict = []string{"Alice", "Bob"}
	assert.Nil(t, ta.Initialize(test.Repository))

	// Add some activity
	ta.activities[0] = &DeveloperTemporalActivity{
		Weekdays: [7]int{1, 2, 3, 4, 5, 6, 7},
		Hours:    [24]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24},
	}

	result := ta.Finalize()
	assert.NotNil(t, result)

	tr := result.(TemporalActivityResult)
	assert.Equal(t, "commits", tr.Mode)
	assert.Equal(t, []string{"Alice", "Bob"}, tr.reversedPeopleDict)
	assert.Len(t, tr.Activities, 1)
	assert.Equal(t, ta.activities[0], tr.Activities[0])
}

func TestTemporalActivitySerializeText(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.reversedPeopleDict = []string{"Alice", "Bob"}

	result := TemporalActivityResult{
		Mode:               "commits",
		reversedPeopleDict: []string{"Alice", "Bob"},
		Activities: map[int]*DeveloperTemporalActivity{
			0: {
				Weekdays: [7]int{1, 2, 0, 0, 0, 0, 0},
				Hours:    [24]int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
				Months:   [12]int{5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
				Weeks:    [53]int{0, 1, 0}, // Rest are zeros
			},
		},
	}

	var buf bytes.Buffer
	err := ta.Serialize(result, false, &buf)
	assert.Nil(t, err)

	output := buf.String()
	assert.Contains(t, output, "temporal_activity:")
	assert.Contains(t, output, "mode: commits")
	assert.Contains(t, output, "activities:")
	assert.Contains(t, output, "0:")
	assert.Contains(t, output, "weekdays:")
	assert.Contains(t, output, "hours:")
	assert.Contains(t, output, "months:")
	assert.Contains(t, output, "weeks:")
	assert.Contains(t, output, "people:")
	assert.Contains(t, output, "Alice")
	assert.Contains(t, output, "Bob")
}

func TestTemporalActivitySerializeBinary(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.reversedPeopleDict = []string{"Alice"}
	result := TemporalActivityResult{
		Mode:               "commits",
		reversedPeopleDict: []string{"Alice"},
		Activities: map[int]*DeveloperTemporalActivity{
			0: {
				Weekdays: [7]int{1, 2, 0, 0, 0, 0, 0},
				Hours:    [24]int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			},
		},
	}

	var buf bytes.Buffer
	err := ta.Serialize(result, true, &buf)
	assert.Nil(t, err)
	assert.Greater(t, buf.Len(), 0)
}

func TestTemporalActivityFork(t *testing.T) {
	ta1 := TemporalActivityAnalysis{Mode: "commits"}
	ta1.activities = map[int]*DeveloperTemporalActivity{
		0: {},
	}

	forks := ta1.Fork(2)
	assert.Len(t, forks, 2)

	// Verify they are the same instance (ForkSamePipelineItem)
	ta2 := forks[0].(*TemporalActivityAnalysis)
	ta3 := forks[1].(*TemporalActivityAnalysis)
	assert.Equal(t, ta1.Mode, ta2.Mode)
	assert.Equal(t, ta1.Mode, ta3.Mode)
}
