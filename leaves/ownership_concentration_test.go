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

func TestOwnershipConcentrationMeta(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	assert.Equal(t, "OwnershipConcentration", oc.Name())
	assert.Empty(t, oc.Provides())
	assert.Contains(t, oc.Requires(), identity.DependencyAuthor)
	assert.Contains(t, oc.Requires(), dependencyOwnershipSnapshot)
	assert.Equal(t, "ownership-concentration", oc.Flag())
	assert.NotEmpty(t, oc.Description())
}

func TestOwnershipConcentrationRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&OwnershipConcentrationAnalysis{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "OwnershipConcentration", summoned[0].Name())
	leaves := core.Registry.GetLeaves()
	matched := false
	for _, tp := range leaves {
		if tp.Flag() == (&OwnershipConcentrationAnalysis{}).Flag() {
			matched = true
			break
		}
	}
	assert.True(t, matched)
}

func TestOwnershipConcentrationConfigure(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	facts := map[string]any{}
	facts[identity.FactIdentityDetectorReversedPeopleDict] = []string{testPersonAlice, testPersonBob}
	facts[items.FactTickSize] = 24 * time.Hour
	logger := core.NewLogger()
	facts[core.ConfigLogger] = logger

	assert.NoError(t, oc.Configure(facts))
	assert.Equal(t, []string{testPersonAlice, testPersonBob}, oc.reversedPeopleDict)
	assert.Equal(t, 24*time.Hour, oc.tickSize)
}

func TestOwnershipConcentrationInitialize(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	assert.NoError(t, oc.Initialize(test.Repository))
	assert.NotNil(t, oc.snapshots)
	assert.Nil(t, oc.ownership)
}

func TestOwnershipConcentrationListConfigurationOptions(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	opts := oc.ListConfigurationOptions()
	assert.Nil(t, opts)
}

func TestComputeGini(t *testing.T) {
	tests := []struct {
		name    string
		authors map[int]int64
		total   int64
		want    float64
		delta   float64
	}{
		{
			name:    "empty",
			authors: map[int]int64{},
			total:   0,
			want:    0,
			delta:   0.001,
		},
		{
			name:    "single author",
			authors: map[int]int64{0: 100},
			total:   100,
			want:    0,
			delta:   0.001,
		},
		{
			name:    "two equal authors",
			authors: map[int]int64{0: 50, 1: 50},
			total:   100,
			want:    0,
			delta:   0.001,
		},
		{
			name:    "extreme inequality",
			authors: map[int]int64{0: 999, 1: 1},
			total:   1000,
			want:    0.499,
			delta:   0.01,
		},
		{
			name:    "three equal authors",
			authors: map[int]int64{0: 100, 1: 100, 2: 100},
			total:   300,
			want:    0,
			delta:   0.001,
		},
		{
			name:    "one dominant author among three",
			authors: map[int]int64{0: 80, 1: 10, 2: 10},
			total:   100,
			want:    0.467,
			delta:   0.01,
		},
		{
			name:    "five equal authors",
			authors: map[int]int64{0: 20, 1: 20, 2: 20, 3: 20, 4: 20},
			total:   100,
			want:    0,
			delta:   0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeGini(tt.authors, tt.total)
			assert.InDelta(t, tt.want, got, tt.delta)
		})
	}
}

func TestComputeHHI(t *testing.T) {
	tests := []struct {
		name    string
		authors map[int]int64
		total   int64
		want    float64
		delta   float64
	}{
		{
			name:    "empty",
			authors: map[int]int64{},
			total:   0,
			want:    0,
			delta:   0.001,
		},
		{
			name:    "single author (monopoly)",
			authors: map[int]int64{0: 100},
			total:   100,
			want:    1.0,
			delta:   0.001,
		},
		{
			name:    "two equal authors",
			authors: map[int]int64{0: 50, 1: 50},
			total:   100,
			want:    0.5, // 1/2 = 0.5
			delta:   0.001,
		},
		{
			name:    "five equal authors",
			authors: map[int]int64{0: 20, 1: 20, 2: 20, 3: 20, 4: 20},
			total:   100,
			want:    0.2, // 1/5 = 0.2
			delta:   0.001,
		},
		{
			name:    "dominant author 80-20",
			authors: map[int]int64{0: 80, 1: 20},
			total:   100,
			want:    0.68, // 0.8^2 + 0.2^2 = 0.64 + 0.04
			delta:   0.001,
		},
		{
			name:    "three authors unequal",
			authors: map[int]int64{0: 60, 1: 30, 2: 10},
			total:   100,
			want:    0.46, // 0.36 + 0.09 + 0.01
			delta:   0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeHHI(tt.authors, tt.total)
			assert.InDelta(t, tt.want, got, tt.delta)
		})
	}
}

func TestOwnershipConcentrationFinalize(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	oc.reversedPeopleDict = []string{testPersonAlice, testPersonBob}
	oc.tickSize = 24 * time.Hour
	assert.NoError(t, oc.Initialize(test.Repository))

	oc.snapshots[0] = &OwnershipConcentrationSnapshot{
		Gini:        0.25,
		HHI:         0.5,
		TotalLines:  100,
		AuthorLines: map[int]int64{0: 60, 1: 40},
	}

	result := oc.Finalize()
	assert.NotNil(t, result)

	ocr := result.(OwnershipConcentrationResult)
	assert.Equal(t, []string{testPersonAlice, testPersonBob}, ocr.reversedPeopleDict)
	assert.Len(t, ocr.Snapshots, 1)
	assert.InDelta(t, 0.25, ocr.Snapshots[0].Gini, 0.001)
	assert.InDelta(t, 0.5, ocr.Snapshots[0].HHI, 0.001)
}

func TestOwnershipConcentrationSerializeText(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	result := OwnershipConcentrationResult{
		Snapshots: map[int]*OwnershipConcentrationSnapshot{
			0: {Gini: 0.0, HHI: 1.0, TotalLines: 100, AuthorLines: map[int]int64{0: 100}},
			5: {Gini: 0.25, HHI: 0.52, TotalLines: 200, AuthorLines: map[int]int64{0: 120, 1: 80}},
		},
		SubsystemConcentration: map[string]*SubsystemConcentration{
			testSourceDirectory: {Gini: 0.3, HHI: 0.6},
			testDocsDirectory:   {Gini: 0.0, HHI: 0.5},
		},
		reversedPeopleDict: []string{testPersonAlice, testPersonBob},
		tickSize:           24 * time.Hour,
	}

	var buf bytes.Buffer
	err := oc.Serialize(result, false, &buf)
	assert.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "ownership_concentration:")
	assert.Contains(t, output, "per_tick:")
	assert.Contains(t, output, "gini: 0.0000")
	assert.Contains(t, output, "hhi: 1.0000")
	assert.Contains(t, output, "gini: 0.2500")
	assert.Contains(t, output, "per_subsystem:")
	assert.Contains(t, output, "people:")
	assert.Contains(t, output, testPersonAlice)
	assert.Contains(t, output, testPersonBob)
}

func TestOwnershipConcentrationSerializeBinaryRoundtrip(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	result := OwnershipConcentrationResult{
		Snapshots: map[int]*OwnershipConcentrationSnapshot{
			0: {Gini: 0.0, HHI: 1.0, TotalLines: 100, AuthorLines: map[int]int64{0: 100}},
			5: {Gini: 0.25, HHI: 0.52, TotalLines: 200, AuthorLines: map[int]int64{0: 120, 1: 80}},
		},
		SubsystemConcentration: map[string]*SubsystemConcentration{
			testSourceDirectory: {Gini: 0.3, HHI: 0.6},
		},
		reversedPeopleDict: []string{testPersonAlice, testPersonBob},
		tickSize:           24 * time.Hour,
	}

	var buf bytes.Buffer
	err := oc.Serialize(result, true, &buf)
	assert.NoError(t, err)
	assert.Positive(t, buf.Len())

	rawResult2, err := oc.Deserialize(buf.Bytes())
	assert.NoError(t, err)
	result2 := rawResult2.(OwnershipConcentrationResult)

	assert.Equal(t, result.reversedPeopleDict, result2.reversedPeopleDict)
	assert.Equal(t, result.tickSize, result2.tickSize)
	assert.Len(t, result2.Snapshots, 2)

	assert.InDelta(t, 0.0, result2.Snapshots[0].Gini, 0.001)
	assert.InDelta(t, 1.0, result2.Snapshots[0].HHI, 0.001)
	assert.Equal(t, int64(100), result2.Snapshots[0].TotalLines)
	assert.Equal(t, int64(100), result2.Snapshots[0].AuthorLines[0])

	assert.InDelta(t, 0.25, result2.Snapshots[5].Gini, 0.001)
	assert.InDelta(t, 0.52, result2.Snapshots[5].HHI, 0.001)
	assert.Equal(t, int64(200), result2.Snapshots[5].TotalLines)

	assert.InDelta(t, 0.3, result2.SubsystemConcentration[testSourceDirectory].Gini, 0.001)
	assert.InDelta(t, 0.6, result2.SubsystemConcentration[testSourceDirectory].HHI, 0.001)
}

func TestOwnershipConcentrationFork(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	oc.snapshots = map[int]*OwnershipConcentrationSnapshot{
		0: {Gini: 0.5, HHI: 0.5, TotalLines: 100},
	}

	forks := oc.Fork(2)
	assert.Len(t, forks, 2)

	oc2 := forks[0].(*OwnershipConcentrationAnalysis)
	oc3 := forks[1].(*OwnershipConcentrationAnalysis)
	assert.Equal(t, oc.snapshots, oc2.snapshots)
	assert.Equal(t, oc.snapshots, oc3.snapshots)
}

// ownershipConcentrationMergeInputs returns two results whose people dictionaries overlap
// partially, so the merge has to reconcile the identity indices as well as add the line counts.
func ownershipConcentrationMergeInputs() (
	OwnershipConcentrationResult, OwnershipConcentrationResult,
) {
	first := OwnershipConcentrationResult{
		Snapshots: map[int]*OwnershipConcentrationSnapshot{
			0: {Gini: 0.0, HHI: 1.0, TotalLines: 100, AuthorLines: map[int]int64{0: 100}},
			5: {Gini: 0.2, HHI: 0.52, TotalLines: 200, AuthorLines: map[int]int64{0: 120, 1: 80}},
		},
		SubsystemConcentration: map[string]*SubsystemConcentration{
			testSourceDirectory: {Gini: 0.3, HHI: 0.6},
		},
		reversedPeopleDict: []string{testPersonAlice, testPersonBob},
		tickSize:           24 * time.Hour,
	}

	second := OwnershipConcentrationResult{
		Snapshots: map[int]*OwnershipConcentrationSnapshot{
			5:  {Gini: 0.17, HHI: 0.56, TotalLines: 150, AuthorLines: map[int]int64{0: 100, 1: 50}},
			10: {Gini: 0.0, HHI: 1.0, TotalLines: 50, AuthorLines: map[int]int64{1: 50}},
		},
		SubsystemConcentration: map[string]*SubsystemConcentration{
			testSourceDirectory: {Gini: 0.4, HHI: 0.7},
			testDocsDirectory:   {Gini: 0.0, HHI: 0.5},
		},
		reversedPeopleDict: []string{testPersonBob, testPersonCharlie},
		tickSize:           24 * time.Hour,
	}

	return first, second
}

func TestOwnershipConcentrationMergeResultsSumsOwnership(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	r1, r2 := ownershipConcentrationMergeInputs()

	common := &core.CommonAnalysisResult{BeginTime: ownershipMergeBeginTime}

	merged, ok := oc.MergeResults(r1, r2, common, common).(OwnershipConcentrationResult)
	require.True(t, ok)

	assert.Equal(t,
		[]string{testPersonAlice, testPersonBob, testPersonCharlie}, merged.reversedPeopleDict)

	// Tick 0: the second repository has not started yet, so only the first contributes.
	assert.Equal(t, int64(100), merged.Snapshots[0].TotalLines)
	assert.InDelta(t, 0.0, merged.Snapshots[0].Gini, 0.0001)
	assert.InDelta(t, 1.0, merged.Snapshots[0].HHI, 0.0001)

	// Tick 5: both repositories are alive, so their per-identity counts add up. Bob is index 1
	// in the first dictionary and index 0 in the second. The metrics are recomputed from the
	// summed distribution rather than merged from the parts.
	tick5 := map[int]int64{0: 120, 1: 180, 2: 50}
	assert.Equal(t, int64(350), merged.Snapshots[5].TotalLines)
	assert.Equal(t, tick5, merged.Snapshots[5].AuthorLines)
	assert.InDelta(t, computeGini(tick5, 350), merged.Snapshots[5].Gini, 0.0001)
	assert.InDelta(t, computeHHI(tick5, 350), merged.Snapshots[5].HHI, 0.0001)

	// Tick 10: the first repository stopped producing snapshots but its lines still exist, so
	// its last known distribution is carried forward.
	tick10 := map[int]int64{0: 120, 1: 80, 2: 50}
	assert.Equal(t, int64(250), merged.Snapshots[10].TotalLines)
	assert.Equal(t, tick10, merged.Snapshots[10].AuthorLines)
	assert.InDelta(t, computeGini(tick10, 250), merged.Snapshots[10].Gini, 0.0001)

	// Subsystem: the more concentrated of the two, a worst-case reading.
	assert.InDelta(t, 0.4, merged.SubsystemConcentration[testSourceDirectory].Gini, 0.0001)
	assert.InDelta(t, 0.7, merged.SubsystemConcentration[testSourceDirectory].HHI, 0.0001)
	assert.InDelta(t, 0.0, merged.SubsystemConcentration[testDocsDirectory].Gini, 0.0001)
}

func TestOwnershipConcentrationMergeResultsIsCommutative(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	r1, r2 := ownershipConcentrationMergeInputs()

	common := &core.CommonAnalysisResult{BeginTime: ownershipMergeBeginTime}

	forward, ok := oc.MergeResults(r1, r2, common, common).(OwnershipConcentrationResult)
	require.True(t, ok)

	backward, ok := oc.MergeResults(r2, r1, common, common).(OwnershipConcentrationResult)
	require.True(t, ok)

	for tick, snapshot := range forward.Snapshots {
		require.Contains(t, backward.Snapshots, tick)
		assert.Equal(t, snapshot.TotalLines, backward.Snapshots[tick].TotalLines)
		assert.InDelta(t, snapshot.Gini, backward.Snapshots[tick].Gini, 0.0001)
		assert.InDelta(t, snapshot.HHI, backward.Snapshots[tick].HHI, 0.0001)
	}

	assert.Len(t, backward.Snapshots, len(forward.Snapshots))
}

func TestOwnershipConcentrationMergeResultsRebasesTicks(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}

	r1 := OwnershipConcentrationResult{
		Snapshots: map[int]*OwnershipConcentrationSnapshot{
			0: {Gini: 0.0, HHI: 1.0, TotalLines: 100, AuthorLines: map[int]int64{0: 100}},
		},
		reversedPeopleDict: []string{testPersonAlice},
		tickSize:           24 * time.Hour,
	}

	r2 := OwnershipConcentrationResult{
		Snapshots: map[int]*OwnershipConcentrationSnapshot{
			0: {Gini: 0.0, HHI: 1.0, TotalLines: 100, AuthorLines: map[int]int64{0: 100}},
		},
		reversedPeopleDict: []string{testPersonBob},
		tickSize:           24 * time.Hour,
	}

	c1 := &core.CommonAnalysisResult{BeginTime: ownershipMergeBeginTime}
	c2 := &core.CommonAnalysisResult{BeginTime: ownershipMergeBeginTime + 2*24*3600}

	merged, ok := oc.MergeResults(r1, r2, c1, c2).(OwnershipConcentrationResult)
	require.True(t, ok)

	// The second repository starts two ticks later, so its tick 0 lands on the merged tick 2,
	// where the two equal owners make the distribution perfectly equal.
	assert.Equal(t, int64(100), merged.Snapshots[0].TotalLines)
	assert.Equal(t, int64(200), merged.Snapshots[2].TotalLines)
	assert.InDelta(t, 0.0, merged.Snapshots[2].Gini, 0.0001)
	assert.InDelta(t, 0.5, merged.Snapshots[2].HHI, 0.0001)
	assert.Len(t, merged.Snapshots, 2)
}

func TestOwnershipConcentrationMergeResultsRejectsMismatchingTickSizes(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	common := &core.CommonAnalysisResult{BeginTime: ownershipMergeBeginTime}

	r1, r2 := ownershipConcentrationMergeInputs()
	r2.tickSize = 48 * time.Hour

	err, ok := oc.MergeResults(r1, r2, common, common).(error)
	require.True(t, ok)
	require.ErrorIs(t, err, errOwnershipConcentrationMismatchingTickSizes)
}
