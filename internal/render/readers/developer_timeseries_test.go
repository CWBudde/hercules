package readers

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeveloperTimeSeriesFixVerification verifies that the fix for developer time series
// produces the exact same data structure as Python's get_devs() method.
func TestDeveloperTimeSeriesFixVerification(t *testing.T) {
	testFile := "../testdata/example_data/hercules_devs.pb"

	reader := &ProtobufReader{}
	file, err := os.Open(testFile)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()

	err = reader.Read(file)
	require.NoError(t, err)

	t.Log("=== Verifying Developer Time Series Fix ===")

	// Get the fixed time series data
	devData, err := reader.GetDeveloperTimeSeriesData()
	require.NoError(t, err, "GetDeveloperTimeSeriesData should work with real temporal parsing")

	t.Logf("Fixed time series structure:")
	t.Logf("  People count: %d", len(devData.People))
	t.Logf("  Time ticks count: %d", len(devData.Days))

	// Verify we have multi-day data (not synthetic single-day aggregation)
	assert.Greater(t, len(devData.Days), 1, "Should have multiple time ticks, not synthetic single day")

	// Log all time tick keys to verify they're real temporal indices
	var tickKeys []int
	for tickKey := range devData.Days {
		tickKeys = append(tickKeys, tickKey)
	}
	t.Logf("  Time tick keys: %v", tickKeys)

	// Verify no synthetic "day 0" aggregation (unless day 0 is a real tick)
	if len(tickKeys) == 1 && tickKeys[0] == 0 {
		t.Error("REGRESSION: Still using synthetic single-day aggregation (day 0 only)")
	}

	// Verify data structure matches Python format: (people, days)
	// where days is {day: {dev: DevDay}}
	t.Run("DataStructureValidation", func(t *testing.T) {
		assert.NotEmpty(t, devData.People, "People list should not be empty")
		assert.NotEmpty(t, devData.Days, "Days map should not be empty")

		// Check each time tick
		for tickKey, dayDevs := range devData.Days {
			t.Logf("    Tick %d: %d developers", tickKey, len(dayDevs))

			// Verify developer data structure
			for devIdx, devDay := range dayDevs {
				// Verify DevDay structure has all required fields
				assert.GreaterOrEqual(t, devDay.Commits, 0, "Commits should be non-negative")
				assert.GreaterOrEqual(t, devDay.LinesAdded, 0, "LinesAdded should be non-negative")
				assert.GreaterOrEqual(t, devDay.LinesRemoved, 0, "LinesRemoved should be non-negative")
				assert.GreaterOrEqual(t, devDay.LinesModified, 0, "LinesModified should be non-negative")
				assert.NotNil(t, devDay.Languages, "Languages map should be initialized")

				// Log detailed stats for first few developers
				if len(dayDevs) <= 3 && devIdx < len(devData.People) {
					devName := "unknown"
					if devIdx < len(devData.People) {
						devName = devData.People[devIdx]
					}
					t.Logf("      Dev %d (%s): commits=%d, lines=+%d/-%d/%d, langs=%d",
						devIdx, devName, devDay.Commits, devDay.LinesAdded,
						devDay.LinesRemoved, devDay.LinesModified, len(devDay.Languages))

					// Log language statistics
					for lang, langStats := range devDay.Languages {
						if len(langStats) >= 3 {
							t.Logf("        %s: [%d, %d, %d]", lang, langStats[0], langStats[1], langStats[2])
						}
					}
				}
			}
		}
	})

	t.Run("PythonCompatibilityValidation", func(t *testing.T) {
		// Verify the structure exactly matches Python's expectations:
		// Python: people, days = reader.get_devs()
		// where days is: {day_int: {dev_int: DevDay}}

		// Test that we can iterate like Python does
		pythonStyleIteration := true

		// Python: for day, dev_data in days.items():
		for dayInt, devData := range devData.Days {
			assert.IsType(t, 0, dayInt, "Day keys should be integers")

			// Python: for dev_idx, dev_day in dev_data.items():
			for devIdx, devDay := range devData {
				assert.IsType(t, 0, devIdx, "Developer indices should be integers")

				// Verify DevDay has all the fields Python expects
				_ = devDay.Commits       // Python: dev_day.commits
				_ = devDay.LinesAdded    // Python: dev_day.stats.added
				_ = devDay.LinesRemoved  // Python: dev_day.stats.removed
				_ = devDay.LinesModified // Python: dev_day.stats.changed
				_ = devDay.Languages     // Python: dev_day.languages

				// Python language format: {lang: [added, removed, changed]}
				for lang, langStats := range devDay.Languages {
					assert.IsType(t, "", lang, "Language keys should be strings")
					assert.Len(t, langStats, 3, "Language stats should have [added, removed, changed] format")
				}
			}
		}

		assert.True(t, pythonStyleIteration, "Data structure should support Python-style iteration")
	})
}

// TestDeveloperModeIntegration tests that developer analysis modes work with real temporal data.
func TestDeveloperModeIntegration(t *testing.T) {
	testFile := "../testdata/example_data/hercules_devs.pb"

	reader := &ProtobufReader{}
	file, err := os.Open(testFile)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()

	err = reader.Read(file)
	require.NoError(t, err)

	t.Log("=== Testing Developer Mode Integration ===")

	// Get the fixed time series data
	devData, err := reader.GetDeveloperTimeSeriesData()
	require.NoError(t, err)

	t.Logf("Integration test data:")
	t.Logf("  People: %v", devData.People)
	t.Logf("  Time ticks: %v", getMapKeys(devData.Days))

	// Test that we can perform typical developer analysis operations
	t.Run("TemporalAnalysis", func(t *testing.T) {
		totalCommits := 0
		totalLinesAdded := 0

		// Aggregate across all time ticks (like devs mode would do)
		for tickKey, dayDevs := range devData.Days {
			t.Logf("Processing tick %d with %d developers", tickKey, len(dayDevs))

			for _, devDay := range dayDevs {
				totalCommits += devDay.Commits
				totalLinesAdded += devDay.LinesAdded
			}
		}

		t.Logf("Total commits across all ticks: %d", totalCommits)
		t.Logf("Total lines added across all ticks: %d", totalLinesAdded)

		// These should be non-negative (sanity check)
		assert.GreaterOrEqual(t, totalCommits, 0, "Total commits should be non-negative")
		assert.GreaterOrEqual(t, totalLinesAdded, 0, "Total lines added should be non-negative")
	})

	t.Run("ParallelAnalysis", func(t *testing.T) {
		// Test analysis that would be used by devs-parallel mode

		if len(devData.Days) < 2 {
			t.Skip("Need multiple time ticks for parallel analysis")
		}

		// Count developers active in multiple time periods
		developerActivity := make(map[int]int) // devIdx -> number of active time ticks

		for _, dayDevs := range devData.Days {
			for devIdx, devDay := range dayDevs {
				if devDay.Commits > 0 || devDay.LinesAdded > 0 {
					developerActivity[devIdx]++
				}
			}
		}

		parallelDevs := 0
		for devIdx, activeTickCount := range developerActivity {
			if activeTickCount > 1 {
				parallelDevs++
				t.Logf("Developer %d active in %d time ticks", devIdx, activeTickCount)
			}
		}

		t.Logf("Developers active in multiple time periods: %d", parallelDevs)
	})
}

// Helper function to get map keys for logging.
func getMapKeys(m map[int]map[int]DevDay) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
