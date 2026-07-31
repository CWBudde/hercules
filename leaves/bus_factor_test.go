package leaves

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/test"
)

// ownershipMergeBeginTime is the repository start used by the ownership merge tests.
const ownershipMergeBeginTime = 1556224895

func TestBusFactorMeta(t *testing.T) {
	bf := BusFactorAnalysis{}
	assert.Equal(t, "BusFactor", bf.Name())
	assert.Empty(t, bf.Provides())
	assert.Contains(t, bf.Requires(), identity.DependencyAuthor)
	assert.Contains(t, bf.Requires(), dependencyOwnershipSnapshot)
	assert.Equal(t, "bus-factor", bf.Flag())
	assert.NotEmpty(t, bf.Description())
}

func TestBusFactorRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&BusFactorAnalysis{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "BusFactor", summoned[0].Name())
	leaves := core.Registry.GetLeaves()
	matched := false
	for _, tp := range leaves {
		if tp.Flag() == (&BusFactorAnalysis{}).Flag() {
			matched = true
			break
		}
	}
	assert.True(t, matched)
}

func TestBusFactorConfigure(t *testing.T) {
	bf := BusFactorAnalysis{}
	facts := map[string]any{}
	facts[identity.FactIdentityDetectorReversedPeopleDict] = []string{testPersonAlice, testPersonBob}
	facts[items.FactTickSize] = 24 * time.Hour
	facts[ConfigBusFactorThreshold] = float32(0.9)
	logger := core.NewLogger()
	facts[core.ConfigLogger] = logger

	assert.NoError(t, bf.Configure(facts))
	assert.Equal(t, []string{testPersonAlice, testPersonBob}, bf.reversedPeopleDict)
	assert.Equal(t, 24*time.Hour, bf.tickSize)
	assert.InDelta(t, float32(0.9), bf.Threshold, 0.001)
}

func TestBusFactorConfigureDefaults(t *testing.T) {
	bf := BusFactorAnalysis{}
	facts := map[string]any{}
	assert.NoError(t, bf.Configure(facts))
	// threshold stays zero until Initialize
	assert.NoError(t, bf.Initialize(test.Repository))
	assert.InDelta(t, float32(0.8), bf.Threshold, 0.001)
}

func TestBusFactorInitialize(t *testing.T) {
	bf := BusFactorAnalysis{}
	assert.NoError(t, bf.Initialize(test.Repository))
	assert.NotNil(t, bf.snapshots)
	assert.Nil(t, bf.ownership)
	assert.InDelta(t, float32(0.8), bf.Threshold, 0.001)
}

func TestBusFactorListConfigurationOptions(t *testing.T) {
	bf := BusFactorAnalysis{}
	opts := bf.ListConfigurationOptions()
	assert.Len(t, opts, 1)
	assert.Equal(t, ConfigBusFactorThreshold, opts[0].Name)
	assert.Equal(t, "bus-factor-threshold", opts[0].Flag)
}

func TestComputeBusFactor(t *testing.T) {
	tests := []struct {
		name       string
		authors    map[int]int64
		total      int64
		threshold  float32
		wantFactor int
	}{
		{
			name:       "empty repo",
			authors:    map[int]int64{},
			total:      0,
			threshold:  0.8,
			wantFactor: 0,
		},
		{
			name:       "single author",
			authors:    map[int]int64{0: 100},
			total:      100,
			threshold:  0.8,
			wantFactor: 1,
		},
		{
			name:       "two equal authors at 80%",
			authors:    map[int]int64{0: 50, 1: 50},
			total:      100,
			threshold:  0.8,
			wantFactor: 2,
		},
		{
			name:       "dominant author covers 80%",
			authors:    map[int]int64{0: 80, 1: 20},
			total:      100,
			threshold:  0.8,
			wantFactor: 1,
		},
		{
			name:       "three authors, need two for 80%",
			authors:    map[int]int64{0: 50, 1: 40, 2: 10},
			total:      100,
			threshold:  0.8,
			wantFactor: 2,
		},
		{
			name:       "five authors evenly split",
			authors:    map[int]int64{0: 20, 1: 20, 2: 20, 3: 20, 4: 20},
			total:      100,
			threshold:  0.8,
			wantFactor: 4,
		},
		{
			name:       "threshold at 100%",
			authors:    map[int]int64{0: 50, 1: 50},
			total:      100,
			threshold:  1.0,
			wantFactor: 2,
		},
		{
			name:       "threshold at 50%",
			authors:    map[int]int64{0: 60, 1: 40},
			total:      100,
			threshold:  0.5,
			wantFactor: 1,
		},
		{
			name:       "two lines require both authors",
			authors:    map[int]int64{0: 1, 1: 1},
			total:      2,
			threshold:  0.8,
			wantFactor: 2,
		},
		{
			name:       "three lines require all three",
			authors:    map[int]int64{0: 1, 1: 1, 2: 1},
			total:      3,
			threshold:  0.8,
			wantFactor: 3,
		},
		{
			name:       "seven lines require six",
			authors:    map[int]int64{0: 5, 1: 2},
			total:      7,
			threshold:  0.8,
			wantFactor: 2,
		},
		{
			name:       "four of five exactly meets decimal threshold",
			authors:    map[int]int64{0: 4, 1: 1},
			total:      5,
			threshold:  0.8,
			wantFactor: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeBusFactor(tt.authors, tt.total, tt.threshold)
			assert.Equal(t, tt.wantFactor, got)
		})
	}
}

func TestBusFactorFinalize(t *testing.T) {
	bf := BusFactorAnalysis{}
	bf.reversedPeopleDict = []string{testPersonAlice, testPersonBob}
	bf.tickSize = 24 * time.Hour
	bf.Threshold = 0.8
	assert.NoError(t, bf.Initialize(test.Repository))

	// Manually populate snapshots
	bf.snapshots[0] = &BusFactorSnapshot{
		BusFactor:   2,
		TotalLines:  100,
		AuthorLines: map[int]int64{0: 60, 1: 40},
	}

	// Finalize with lastTick=-1 means no extra snapshot taken
	result := bf.Finalize()
	assert.NotNil(t, result)

	bfr := result.(BusFactorResult)
	assert.Equal(t, []string{testPersonAlice, testPersonBob}, bfr.reversedPeopleDict)
	assert.InDelta(t, float32(0.8), bfr.Threshold, 0.001)
	assert.Len(t, bfr.Snapshots, 1)
	assert.Equal(t, 2, bfr.Snapshots[0].BusFactor)
}

func TestBusFactorSerializeText(t *testing.T) {
	bf := BusFactorAnalysis{}
	result := BusFactorResult{
		Snapshots: map[int]*BusFactorSnapshot{
			0: {BusFactor: 1, TotalLines: 100, AuthorLines: map[int]int64{0: 100}},
			5: {BusFactor: 2, TotalLines: 200, AuthorLines: map[int]int64{0: 120, 1: 80}},
		},
		SubsystemBusFactor: map[string]int{
			testSourceDirectory: 1,
			testDocsDirectory:   2,
		},
		Threshold:          0.8,
		reversedPeopleDict: []string{testPersonAlice, testPersonBob},
		tickSize:           24 * time.Hour,
	}

	var buf bytes.Buffer
	err := bf.Serialize(result, false, &buf)
	assert.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "bus_factor:")
	assert.Contains(t, output, "threshold: 0.80")
	assert.Contains(t, output, "per_tick:")
	assert.Contains(t, output, "bus_factor: 1")
	assert.Contains(t, output, "bus_factor: 2")
	assert.Contains(t, output, "per_subsystem:")
	assert.Contains(t, output, "\"src\": 1")
	assert.Contains(t, output, "\"docs\": 2")
	assert.Contains(t, output, "people:")
	assert.Contains(t, output, testPersonAlice)
	assert.Contains(t, output, testPersonBob)
}

func TestBusFactorSerializeBinaryRoundtrip(t *testing.T) {
	bf := BusFactorAnalysis{}
	result := BusFactorResult{
		Snapshots: map[int]*BusFactorSnapshot{
			0: {BusFactor: 1, TotalLines: 100, AuthorLines: map[int]int64{0: 80, 1: 20}},
			5: {BusFactor: 2, TotalLines: 200, AuthorLines: map[int]int64{0: 120, 1: 80}},
		},
		SubsystemBusFactor: map[string]int{
			testSourceDirectory: 1,
		},
		Threshold:          0.8,
		reversedPeopleDict: []string{testPersonAlice, testPersonBob},
		tickSize:           24 * time.Hour,
	}

	// Serialize to binary
	var buf bytes.Buffer
	err := bf.Serialize(result, true, &buf)
	assert.NoError(t, err)
	assert.Positive(t, buf.Len())

	// Deserialize
	rawResult2, err := bf.Deserialize(buf.Bytes())
	assert.NoError(t, err)
	result2 := rawResult2.(BusFactorResult)

	// Compare
	assert.Equal(t, result.reversedPeopleDict, result2.reversedPeopleDict)
	assert.InDelta(t, result.Threshold, result2.Threshold, 0.001)
	assert.Equal(t, result.tickSize, result2.tickSize)
	assert.Len(t, result2.Snapshots, 2)

	assert.Equal(t, 1, result2.Snapshots[0].BusFactor)
	assert.Equal(t, int64(100), result2.Snapshots[0].TotalLines)
	assert.Equal(t, int64(80), result2.Snapshots[0].AuthorLines[0])
	assert.Equal(t, int64(20), result2.Snapshots[0].AuthorLines[1])

	assert.Equal(t, 2, result2.Snapshots[5].BusFactor)
	assert.Equal(t, int64(200), result2.Snapshots[5].TotalLines)

	assert.Equal(t, 1, result2.SubsystemBusFactor[testSourceDirectory])
}

func TestBusFactorFork(t *testing.T) {
	bf := BusFactorAnalysis{}
	bf.snapshots = map[int]*BusFactorSnapshot{
		0: {BusFactor: 1, TotalLines: 100},
	}

	forks := bf.Fork(2)
	assert.Len(t, forks, 2)

	// ForkSamePipelineItem returns the same instance
	bf2 := forks[0].(*BusFactorAnalysis)
	bf3 := forks[1].(*BusFactorAnalysis)
	assert.Equal(t, bf.snapshots, bf2.snapshots)
	assert.Equal(t, bf.snapshots, bf3.snapshots)
}

// busFactorMergeInputs returns two results whose people dictionaries overlap partially, so the
// merge has to reconcile the identity indices as well as add the line counts.
func busFactorMergeInputs() (BusFactorResult, BusFactorResult) {
	first := BusFactorResult{
		Snapshots: map[int]*BusFactorSnapshot{
			0: {BusFactor: 1, TotalLines: 100, AuthorLines: map[int]int64{0: 100}},
			5: {BusFactor: 2, TotalLines: 200, AuthorLines: map[int]int64{0: 120, 1: 80}},
		},
		SubsystemBusFactor: map[string]int{testSourceDirectory: 1},
		Threshold:          0.8,
		reversedPeopleDict: []string{testPersonAlice, testPersonBob},
		tickSize:           24 * time.Hour,
	}

	second := BusFactorResult{
		Snapshots: map[int]*BusFactorSnapshot{
			5:  {BusFactor: 2, TotalLines: 150, AuthorLines: map[int]int64{0: 100, 1: 50}},
			10: {BusFactor: 1, TotalLines: 50, AuthorLines: map[int]int64{1: 50}},
		},
		SubsystemBusFactor: map[string]int{testSourceDirectory: 2, testDocsDirectory: 1},
		Threshold:          0.8,
		reversedPeopleDict: []string{testPersonBob, testPersonCharlie},
		tickSize:           24 * time.Hour,
	}

	return first, second
}

func TestBusFactorMergeResultsSumsOwnership(t *testing.T) {
	bf := BusFactorAnalysis{}
	r1, r2 := busFactorMergeInputs()

	common := &core.CommonAnalysisResult{BeginTime: ownershipMergeBeginTime}

	merged, ok := bf.MergeResults(r1, r2, common, common).(BusFactorResult)
	require.True(t, ok)

	assert.Equal(t,
		[]string{testPersonAlice, testPersonBob, testPersonCharlie}, merged.reversedPeopleDict)

	// Tick 0: the second repository has not started yet, so only the first contributes.
	assert.Equal(t, int64(100), merged.Snapshots[0].TotalLines)
	assert.Equal(t, map[int]int64{0: 100}, merged.Snapshots[0].AuthorLines)
	assert.Equal(t, 1, merged.Snapshots[0].BusFactor)

	// Tick 5: both repositories are alive, so their per-identity counts add up. Bob is index 1
	// in the first dictionary and index 0 in the second.
	assert.Equal(t, int64(350), merged.Snapshots[5].TotalLines)
	assert.Equal(t, map[int]int64{0: 120, 1: 180, 2: 50}, merged.Snapshots[5].AuthorLines)
	// ceil(350 * 0.8) = 280; 180 + 120 covers it, 180 alone does not.
	assert.Equal(t, 2, merged.Snapshots[5].BusFactor)

	// Tick 10: the first repository stopped producing snapshots but its lines still exist, so
	// its last known distribution is carried forward.
	assert.Equal(t, int64(250), merged.Snapshots[10].TotalLines)
	assert.Equal(t, map[int]int64{0: 120, 1: 80, 2: 50}, merged.Snapshots[10].AuthorLines)
	assert.Equal(t, 2, merged.Snapshots[10].BusFactor)

	// Subsystem: max of both, a worst-case reading.
	assert.Equal(t, 2, merged.SubsystemBusFactor[testSourceDirectory])
	assert.Equal(t, 1, merged.SubsystemBusFactor[testDocsDirectory])
}

func TestBusFactorMergeResultsIsCommutative(t *testing.T) {
	bf := BusFactorAnalysis{}
	r1, r2 := busFactorMergeInputs()

	common := &core.CommonAnalysisResult{BeginTime: ownershipMergeBeginTime}

	forward, ok := bf.MergeResults(r1, r2, common, common).(BusFactorResult)
	require.True(t, ok)

	backward, ok := bf.MergeResults(r2, r1, common, common).(BusFactorResult)
	require.True(t, ok)

	for tick, snapshot := range forward.Snapshots {
		require.Contains(t, backward.Snapshots, tick)
		assert.Equal(t, snapshot.TotalLines, backward.Snapshots[tick].TotalLines)
		assert.Equal(t, snapshot.BusFactor, backward.Snapshots[tick].BusFactor)
	}

	assert.Len(t, backward.Snapshots, len(forward.Snapshots))
	assert.ElementsMatch(t, forward.reversedPeopleDict, backward.reversedPeopleDict)
}

func TestBusFactorMergeResultsRebasesTicks(t *testing.T) {
	bf := BusFactorAnalysis{}

	r1 := BusFactorResult{
		Snapshots: map[int]*BusFactorSnapshot{
			0: {BusFactor: 1, TotalLines: 100, AuthorLines: map[int]int64{0: 100}},
		},
		Threshold:          0.8,
		reversedPeopleDict: []string{testPersonAlice},
		tickSize:           24 * time.Hour,
	}

	r2 := BusFactorResult{
		Snapshots: map[int]*BusFactorSnapshot{
			0: {BusFactor: 1, TotalLines: 40, AuthorLines: map[int]int64{0: 40}},
		},
		Threshold:          0.8,
		reversedPeopleDict: []string{testPersonBob},
		tickSize:           24 * time.Hour,
	}

	c1 := &core.CommonAnalysisResult{BeginTime: ownershipMergeBeginTime}
	c2 := &core.CommonAnalysisResult{BeginTime: ownershipMergeBeginTime + 2*24*3600}

	merged, ok := bf.MergeResults(r1, r2, c1, c2).(BusFactorResult)
	require.True(t, ok)

	// The second repository starts two ticks later, so its tick 0 lands on the merged tick 2.
	assert.Equal(t, int64(100), merged.Snapshots[0].TotalLines)
	assert.Equal(t, int64(140), merged.Snapshots[2].TotalLines)
	assert.Equal(t, map[int]int64{0: 100, 1: 40}, merged.Snapshots[2].AuthorLines)
	assert.Len(t, merged.Snapshots, 2)
}

func TestBusFactorMergeResultsRejectsMismatchingConfiguration(t *testing.T) {
	bf := BusFactorAnalysis{}
	common := &core.CommonAnalysisResult{BeginTime: ownershipMergeBeginTime}

	r1, r2 := busFactorMergeInputs()
	r2.tickSize = 48 * time.Hour

	err, ok := bf.MergeResults(r1, r2, common, common).(error)
	require.True(t, ok)
	require.ErrorIs(t, err, errBusFactorMismatchingTickSizes)

	r1, r2 = busFactorMergeInputs()
	r2.Threshold = 0.5

	err, ok = bf.MergeResults(r1, r2, common, common).(error)
	require.True(t, ok)
	require.ErrorIs(t, err, errBusFactorMismatchingThresholds)
}
