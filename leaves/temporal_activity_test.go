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
	required := [...]string{identity.DependencyAuthor, items.DependencyLineStats, items.DependencyTick}
	for _, name := range required {
		assert.Contains(t, ta.Requires(), name)
	}
	opts := ta.ListConfigurationOptions()
	assert.Len(t, opts, 0)
	assert.Equal(t, ta.Flag(), "temporal-activity")
	assert.Equal(t, ta.Description(), "Calculates commit and line change activity by weekday, hour, month, and ISO week.")
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
	facts[items.FactTickSize] = 24 * time.Hour
	logger := core.NewLogger()
	facts[core.ConfigLogger] = logger

	assert.Nil(t, ta.Configure(facts))
	assert.Equal(t, ta.reversedPeopleDict, []string{"Alice", "Bob"})
	assert.Equal(t, 24*time.Hour, ta.tickSize)
	assert.Equal(t, logger, ta.l)
}

func TestTemporalActivityInitialize(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	assert.Nil(t, ta.Initialize(test.Repository))
	assert.NotNil(t, ta.activities)
	assert.NotNil(t, ta.ticks)
}

func TestTemporalActivityConsume(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	assert.Nil(t, ta.Initialize(test.Repository))

	deps := map[string]interface{}{}
	deps[core.DependencyIsMerge] = false
	deps[identity.DependencyAuthor] = 0
	deps[items.DependencyTick] = 0

	// Create a commit with known timestamp
	// Tuesday, 2023-01-03 14:30:00 UTC
	commitTime := time.Date(2023, time.January, 3, 14, 30, 0, 0, time.UTC)
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

	// Verify activity was recorded
	assert.Len(t, ta.activities, 1)
	activity := ta.activities[0]
	assert.NotNil(t, activity)

	// Tuesday is weekday 2
	assert.Equal(t, 1, activity.Weekdays.Commits[2])
	assert.Equal(t, 70, activity.Weekdays.Lines[2])
	// Hour 14 (2pm)
	assert.Equal(t, 1, activity.Hours.Commits[14])
	assert.Equal(t, 70, activity.Hours.Lines[14])
	// January is month 0
	assert.Equal(t, 1, activity.Months.Commits[0])
	assert.Equal(t, 70, activity.Months.Lines[0])
	// Week 1 of 2023 (stored at index 0 because of week-1 indexing)
	_, week := commitTime.ISOWeek()
	assert.Equal(t, 1, activity.Weeks.Commits[week-1])
	assert.Equal(t, 70, activity.Weeks.Lines[week-1])

	// Verify tick data was recorded
	assert.Len(t, ta.ticks, 1)
	assert.NotNil(t, ta.ticks[0])
	assert.NotNil(t, ta.ticks[0][0])
	tickData := ta.ticks[0][0]
	assert.Equal(t, 1, tickData.Commits)
	assert.Equal(t, 70, tickData.Lines)
	assert.Equal(t, 2, tickData.Weekday)
	assert.Equal(t, 14, tickData.Hour)
}

func TestTemporalActivityMultipleCommits(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	assert.Nil(t, ta.Initialize(test.Repository))

	// Simulate multiple commits from same developer on different days/times
	commits := []struct {
		author int
		time   time.Time
		tick   int
		lines  int
	}{
		{0, time.Date(2023, time.January, 2, 10, 0, 0, 0, time.UTC), 0, 10}, // Monday 10am
		{0, time.Date(2023, time.January, 2, 15, 0, 0, 0, time.UTC), 0, 20}, // Monday 3pm (same tick)
		{0, time.Date(2023, time.January, 3, 10, 0, 0, 0, time.UTC), 1, 30}, // Tuesday 10am
		{1, time.Date(2023, time.January, 4, 14, 0, 0, 0, time.UTC), 2, 40}, // Wednesday 2pm (different dev)
	}

	for _, c := range commits {
		deps := map[string]interface{}{}
		deps[core.DependencyIsMerge] = false
		deps[identity.DependencyAuthor] = c.author
		deps[items.DependencyTick] = c.tick
		deps[core.DependencyCommit] = &object.Commit{
			Author: object.Signature{When: c.time},
		}
		deps[items.DependencyLineStats] = map[object.ChangeEntry]items.LineStats{
			{}: {Added: c.lines, Removed: 0},
		}

		result, err := ta.Consume(deps)
		assert.Nil(t, err)
		assert.Nil(t, result)
	}

	// Verify dev 0 has 3 commits
	assert.Len(t, ta.activities, 2)
	dev0 := ta.activities[0]
	assert.Equal(t, 2, dev0.Weekdays.Commits[1]) // Monday
	assert.Equal(t, 1, dev0.Weekdays.Commits[2]) // Tuesday
	assert.Equal(t, 2, dev0.Hours.Commits[10])   // 10am
	assert.Equal(t, 1, dev0.Hours.Commits[15])   // 3pm

	// Verify line counts
	assert.Equal(t, 30, dev0.Weekdays.Lines[1]) // Monday: 10 + 20
	assert.Equal(t, 30, dev0.Weekdays.Lines[2]) // Tuesday: 30

	// Verify dev 1 has 1 commit
	dev1 := ta.activities[1]
	assert.Equal(t, 1, dev1.Weekdays.Commits[3]) // Wednesday
	assert.Equal(t, 1, dev1.Hours.Commits[14])   // 2pm
	assert.Equal(t, 40, dev1.Weekdays.Lines[3])  // Wednesday: 40
}

func TestTemporalActivityWeekdayBoundaries(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	assert.Nil(t, ta.Initialize(test.Repository))

	// Test all 7 weekdays
	weekdays := []time.Time{
		time.Date(2023, time.January, 1, 12, 0, 0, 0, time.UTC), // Sunday
		time.Date(2023, time.January, 2, 12, 0, 0, 0, time.UTC), // Monday
		time.Date(2023, time.January, 3, 12, 0, 0, 0, time.UTC), // Tuesday
		time.Date(2023, time.January, 4, 12, 0, 0, 0, time.UTC), // Wednesday
		time.Date(2023, time.January, 5, 12, 0, 0, 0, time.UTC), // Thursday
		time.Date(2023, time.January, 6, 12, 0, 0, 0, time.UTC), // Friday
		time.Date(2023, time.January, 7, 12, 0, 0, 0, time.UTC), // Saturday
	}

	for i, commitTime := range weekdays {
		deps := map[string]interface{}{}
		deps[core.DependencyIsMerge] = false
		deps[identity.DependencyAuthor] = 0
		deps[items.DependencyTick] = i
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
		assert.Equal(t, 1, activity.Weekdays.Commits[i], "Weekday %d should have 1 commit", i)
	}
}

func TestTemporalActivityHourBoundaries(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	assert.Nil(t, ta.Initialize(test.Repository))

	// Test boundary hours: 0 (midnight) and 23 (11pm)
	hours := []int{0, 1, 12, 22, 23}
	for i, hour := range hours {
		commitTime := time.Date(2023, time.January, 1, hour, 0, 0, 0, time.UTC)
		deps := map[string]interface{}{}
		deps[core.DependencyIsMerge] = false
		deps[identity.DependencyAuthor] = 0
		deps[items.DependencyTick] = i
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
	assert.Equal(t, 1, activity.Hours.Commits[0])
	assert.Equal(t, 1, activity.Hours.Commits[1])
	assert.Equal(t, 1, activity.Hours.Commits[12])
	assert.Equal(t, 1, activity.Hours.Commits[22])
	assert.Equal(t, 1, activity.Hours.Commits[23])
}

func TestTemporalActivityMonthBoundaries(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	assert.Nil(t, ta.Initialize(test.Repository))

	// Test all 12 months
	tick := 0
	for month := time.January; month <= time.December; month++ {
		commitTime := time.Date(2023, month, 15, 12, 0, 0, 0, time.UTC)
		deps := map[string]interface{}{}
		deps[core.DependencyIsMerge] = false
		deps[identity.DependencyAuthor] = 0
		deps[items.DependencyTick] = tick
		deps[core.DependencyCommit] = &object.Commit{
			Author: object.Signature{When: commitTime},
		}
		deps[items.DependencyLineStats] = map[object.ChangeEntry]items.LineStats{}

		result, err := ta.Consume(deps)
		assert.Nil(t, err)
		assert.Nil(t, result)
		tick++
	}

	// Verify all months have 1 commit
	activity := ta.activities[0]
	for i := 0; i < 12; i++ {
		assert.Equal(t, 1, activity.Months.Commits[i], "Month %d should have 1 commit", i)
	}
}

func TestTemporalActivityISOWeekEdgeCases(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	assert.Nil(t, ta.Initialize(test.Repository))

	// Test week 1 and week 53 (leap week year)
	// 2020 is a leap week year (has week 53)
	testCases := []time.Time{
		time.Date(2020, time.January, 6, 12, 0, 0, 0, time.UTC),   // Week 2
		time.Date(2020, time.December, 28, 12, 0, 0, 0, time.UTC), // Week 53
	}

	for i, commitTime := range testCases {
		deps := map[string]interface{}{}
		deps[core.DependencyIsMerge] = false
		deps[identity.DependencyAuthor] = 0
		deps[items.DependencyTick] = i
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
	assert.Equal(t, 1, activity.Weeks.Commits[1]) // Week 2 stored at index 1
	// Verify week 53 is stored correctly
	_, week53 := testCases[1].ISOWeek()
	assert.Equal(t, 53, week53)
	assert.Equal(t, 1, activity.Weeks.Commits[52]) // Week 53 stored at index 52
}

func TestTemporalActivityFinalize(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.reversedPeopleDict = []string{"Alice", "Bob"}
	ta.tickSize = 24 * time.Hour
	assert.Nil(t, ta.Initialize(test.Repository))

	// Add some activity
	ta.activities[0] = &DeveloperTemporalActivity{
		Weekdays: newTemporalDimension(7),
		Hours:    newTemporalDimension(24),
		Months:   newTemporalDimension(12),
		Weeks:    newTemporalDimension(53),
	}
	ta.activities[0].Weekdays.Commits = []int{1, 2, 3, 4, 5, 6, 7}
	ta.activities[0].Weekdays.Lines = []int{10, 20, 30, 40, 50, 60, 70}

	result := ta.Finalize()
	assert.NotNil(t, result)

	tr := result.(TemporalActivityResult)
	assert.Equal(t, []string{"Alice", "Bob"}, tr.reversedPeopleDict)
	assert.Len(t, tr.Activities, 1)
	assert.Equal(t, ta.activities[0], tr.Activities[0])
}

func TestTemporalActivitySerializeText(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.reversedPeopleDict = []string{"Alice", "Bob"}

	result := TemporalActivityResult{
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
		Activities: map[int]*DeveloperTemporalActivity{
			0: {
				Weekdays: TemporalDimension{
					Commits: []int{1, 2, 0, 0, 0, 0, 0},
					Lines:   []int{10, 20, 0, 0, 0, 0, 0},
				},
				Hours: TemporalDimension{
					Commits: make([]int, 24),
					Lines:   make([]int, 24),
				},
				Months: TemporalDimension{
					Commits: make([]int, 12),
					Lines:   make([]int, 12),
				},
				Weeks: TemporalDimension{
					Commits: make([]int, 53),
					Lines:   make([]int, 53),
				},
			},
		},
		Ticks: map[int]map[int]*TemporalActivityTick{},
	}

	var buf bytes.Buffer
	err := ta.Serialize(result, false, &buf)
	assert.Nil(t, err)

	output := buf.String()
	assert.Contains(t, output, "temporal_activity:")
	assert.Contains(t, output, "activities:")
	assert.Contains(t, output, "0:")
	assert.Contains(t, output, "weekdays_commits:")
	assert.Contains(t, output, "weekdays_lines:")
	assert.Contains(t, output, "hours_commits:")
	assert.Contains(t, output, "hours_lines:")
	assert.Contains(t, output, "months_commits:")
	assert.Contains(t, output, "months_lines:")
	assert.Contains(t, output, "weeks_commits:")
	assert.Contains(t, output, "weeks_lines:")
	assert.Contains(t, output, "people:")
	assert.Contains(t, output, "Alice")
	assert.Contains(t, output, "Bob")
}

func TestTemporalActivitySerializeBinary(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.reversedPeopleDict = []string{"Alice"}
	result := TemporalActivityResult{
		reversedPeopleDict: []string{"Alice"},
		tickSize:           24 * time.Hour,
		Activities: map[int]*DeveloperTemporalActivity{
			0: {
				Weekdays: TemporalDimension{
					Commits: []int{1, 2, 0, 0, 0, 0, 0},
					Lines:   []int{10, 20, 0, 0, 0, 0, 0},
				},
				Hours: TemporalDimension{
					Commits: make([]int, 24),
					Lines:   make([]int, 24),
				},
				Months: TemporalDimension{
					Commits: make([]int, 12),
					Lines:   make([]int, 12),
				},
				Weeks: TemporalDimension{
					Commits: make([]int, 53),
					Lines:   make([]int, 53),
				},
			},
		},
		Ticks: map[int]map[int]*TemporalActivityTick{},
	}

	var buf bytes.Buffer
	err := ta.Serialize(result, true, &buf)
	assert.Nil(t, err)
	assert.Greater(t, buf.Len(), 0)
}

func TestTemporalActivityFork(t *testing.T) {
	ta1 := TemporalActivityAnalysis{}
	ta1.activities = map[int]*DeveloperTemporalActivity{
		0: {
			Weekdays: newTemporalDimension(7),
		},
	}

	forks := ta1.Fork(2)
	assert.Len(t, forks, 2)

	// Verify they are the same instance (ForkSamePipelineItem)
	ta2 := forks[0].(*TemporalActivityAnalysis)
	ta3 := forks[1].(*TemporalActivityAnalysis)
	assert.Equal(t, ta1.activities, ta2.activities)
	assert.Equal(t, ta1.activities, ta3.activities)
}

func TestTemporalActivityDeserialize(t *testing.T) {
	ta := TemporalActivityAnalysis{}
	ta.reversedPeopleDict = []string{"Alice", "Bob"}

	// Create test data with multiple developers
	result := TemporalActivityResult{
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
		Activities: map[int]*DeveloperTemporalActivity{
			0: {
				Weekdays: TemporalDimension{
					Commits: []int{1, 2, 3, 4, 5, 6, 7},
					Lines:   []int{10, 20, 30, 40, 50, 60, 70},
				},
				Hours: TemporalDimension{
					Commits: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24},
					Lines:   []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160, 170, 180, 190, 200, 210, 220, 230, 240},
				},
				Months: TemporalDimension{
					Commits: []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120},
					Lines:   []int{100, 200, 300, 400, 500, 600, 700, 800, 900, 1000, 1100, 1200},
				},
				Weeks: TemporalDimension{
					Commits: make([]int, 53),
					Lines:   make([]int, 53),
				},
			},
			1: {
				Weekdays: TemporalDimension{
					Commits: []int{10, 20, 30, 40, 50, 60, 70},
					Lines:   []int{100, 200, 300, 400, 500, 600, 700},
				},
				Hours: TemporalDimension{
					Commits: make([]int, 24),
					Lines:   make([]int, 24),
				},
				Months: TemporalDimension{
					Commits: make([]int, 12),
					Lines:   make([]int, 12),
				},
				Weeks: TemporalDimension{
					Commits: make([]int, 53),
					Lines:   make([]int, 53),
				},
			},
		},
		Ticks: map[int]map[int]*TemporalActivityTick{
			0: {
				0: {Commits: 1, Lines: 10, Weekday: 1, Hour: 10, Month: 0, Week: 0},
			},
		},
	}

	// Serialize to binary
	buffer := &bytes.Buffer{}
	err := ta.Serialize(result, true, buffer)
	assert.Nil(t, err)
	assert.Greater(t, buffer.Len(), 0)

	// Deserialize
	rawResult2, err := ta.Deserialize(buffer.Bytes())
	assert.Nil(t, err)
	result2 := rawResult2.(TemporalActivityResult)

	// Compare results
	assert.Equal(t, result.reversedPeopleDict, result2.reversedPeopleDict)
	assert.Len(t, result2.Activities, 2)

	// Verify developer 0 weekdays
	assert.Equal(t, result.Activities[0].Weekdays.Commits, result2.Activities[0].Weekdays.Commits)
	assert.Equal(t, result.Activities[0].Weekdays.Lines, result2.Activities[0].Weekdays.Lines)

	// Verify developer 1 weekdays
	assert.Equal(t, result.Activities[1].Weekdays.Commits, result2.Activities[1].Weekdays.Commits)
	assert.Equal(t, result.Activities[1].Weekdays.Lines, result2.Activities[1].Weekdays.Lines)

	// Verify ticks
	assert.Len(t, result2.Ticks, 1)
	assert.NotNil(t, result2.Ticks[0])
	assert.NotNil(t, result2.Ticks[0][0])
	assert.Equal(t, 1, result2.Ticks[0][0].Commits)
	assert.Equal(t, 10, result2.Ticks[0][0].Lines)
}

func TestTemporalActivityMergeResults(t *testing.T) {
	ta := TemporalActivityAnalysis{}

	// Create first result
	r1 := TemporalActivityResult{
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
		Activities: map[int]*DeveloperTemporalActivity{
			0: {
				Weekdays: TemporalDimension{
					Commits: []int{1, 2, 3, 4, 5, 6, 7},
					Lines:   []int{10, 20, 30, 40, 50, 60, 70},
				},
				Hours: TemporalDimension{
					Commits: make([]int, 24),
					Lines:   make([]int, 24),
				},
				Months: TemporalDimension{
					Commits: make([]int, 12),
					Lines:   make([]int, 12),
				},
				Weeks: TemporalDimension{
					Commits: make([]int, 53),
					Lines:   make([]int, 53),
				},
			},
			1: {
				Weekdays: TemporalDimension{
					Commits: []int{10, 20, 30, 40, 50, 60, 70},
					Lines:   []int{100, 200, 300, 400, 500, 600, 700},
				},
				Hours: TemporalDimension{
					Commits: make([]int, 24),
					Lines:   make([]int, 24),
				},
				Months: TemporalDimension{
					Commits: make([]int, 12),
					Lines:   make([]int, 12),
				},
				Weeks: TemporalDimension{
					Commits: make([]int, 53),
					Lines:   make([]int, 53),
				},
			},
		},
		Ticks: map[int]map[int]*TemporalActivityTick{},
	}

	// Create second result
	r2 := TemporalActivityResult{
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
		Activities: map[int]*DeveloperTemporalActivity{
			0: {
				Weekdays: TemporalDimension{
					Commits: []int{2, 3, 4, 5, 6, 7, 8},
					Lines:   []int{20, 30, 40, 50, 60, 70, 80},
				},
				Hours: TemporalDimension{
					Commits: make([]int, 24),
					Lines:   make([]int, 24),
				},
				Months: TemporalDimension{
					Commits: make([]int, 12),
					Lines:   make([]int, 12),
				},
				Weeks: TemporalDimension{
					Commits: make([]int, 53),
					Lines:   make([]int, 53),
				},
			},
			2: {
				Weekdays: TemporalDimension{
					Commits: []int{1, 1, 1, 1, 1, 1, 1},
					Lines:   []int{5, 5, 5, 5, 5, 5, 5},
				},
				Hours: TemporalDimension{
					Commits: make([]int, 24),
					Lines:   make([]int, 24),
				},
				Months: TemporalDimension{
					Commits: make([]int, 12),
					Lines:   make([]int, 12),
				},
				Weeks: TemporalDimension{
					Commits: make([]int, 53),
					Lines:   make([]int, 53),
				},
			},
		},
		Ticks: map[int]map[int]*TemporalActivityTick{},
	}

	c1 := &core.CommonAnalysisResult{}
	c2 := &core.CommonAnalysisResult{}

	// Merge results
	merged := ta.MergeResults(r1, r2, c1, c2).(TemporalActivityResult)

	// Verify all developers are present
	assert.Len(t, merged.Activities, 3)

	// Verify developer 0 (should be sum of both)
	assert.Equal(t, []int{3, 5, 7, 9, 11, 13, 15}, merged.Activities[0].Weekdays.Commits)
	assert.Equal(t, []int{30, 50, 70, 90, 110, 130, 150}, merged.Activities[0].Weekdays.Lines)

	// Verify developer 1 (only in r1)
	assert.Equal(t, r1.Activities[1].Weekdays.Commits, merged.Activities[1].Weekdays.Commits)
	assert.Equal(t, r1.Activities[1].Weekdays.Lines, merged.Activities[1].Weekdays.Lines)

	// Verify developer 2 (only in r2)
	assert.Equal(t, r2.Activities[2].Weekdays.Commits, merged.Activities[2].Weekdays.Commits)
	assert.Equal(t, r2.Activities[2].Weekdays.Lines, merged.Activities[2].Weekdays.Lines)
}
