package leaves

import (
	"bytes"
	"testing"
	"time"

	"github.com/meko-christian/hercules/internal/core"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/test"
	"github.com/stretchr/testify/assert"
)

func TestBusFactorMeta(t *testing.T) {
	bf := BusFactorAnalysis{}
	assert.Equal(t, "BusFactor", bf.Name())
	assert.Len(t, bf.Provides(), 0)
	assert.Contains(t, bf.Requires(), identity.DependencyAuthor)
	assert.Contains(t, bf.Requires(), items.DependencyTick)
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
	facts := map[string]interface{}{}
	facts[identity.FactIdentityDetectorReversedPeopleDict] = []string{"Alice", "Bob"}
	facts[items.FactTickSize] = 24 * time.Hour
	facts[ConfigBusFactorThreshold] = float32(0.9)
	logger := core.NewLogger()
	facts[core.ConfigLogger] = logger

	assert.Nil(t, bf.Configure(facts))
	assert.Equal(t, []string{"Alice", "Bob"}, bf.reversedPeopleDict)
	assert.Equal(t, 24*time.Hour, bf.tickSize)
	assert.InDelta(t, float32(0.9), bf.Threshold, 0.001)
}

func TestBusFactorConfigureDefaults(t *testing.T) {
	bf := BusFactorAnalysis{}
	facts := map[string]interface{}{}
	assert.Nil(t, bf.Configure(facts))
	// threshold stays zero until Initialize
	assert.Nil(t, bf.Initialize(test.Repository))
	assert.InDelta(t, float32(0.8), bf.Threshold, 0.001)
}

func TestBusFactorInitialize(t *testing.T) {
	bf := BusFactorAnalysis{}
	assert.Nil(t, bf.Initialize(test.Repository))
	assert.NotNil(t, bf.snapshots)
	assert.Equal(t, -1, bf.lastTick)
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
	bf.reversedPeopleDict = []string{"Alice", "Bob"}
	bf.tickSize = 24 * time.Hour
	bf.Threshold = 0.8
	assert.Nil(t, bf.Initialize(test.Repository))

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
	assert.Equal(t, []string{"Alice", "Bob"}, bfr.reversedPeopleDict)
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
			"src":  1,
			"docs": 2,
		},
		Threshold:          0.8,
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
	}

	var buf bytes.Buffer
	err := bf.Serialize(result, false, &buf)
	assert.Nil(t, err)

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
	assert.Contains(t, output, "Alice")
	assert.Contains(t, output, "Bob")
}

func TestBusFactorSerializeBinaryRoundtrip(t *testing.T) {
	bf := BusFactorAnalysis{}
	result := BusFactorResult{
		Snapshots: map[int]*BusFactorSnapshot{
			0: {BusFactor: 1, TotalLines: 100, AuthorLines: map[int]int64{0: 80, 1: 20}},
			5: {BusFactor: 2, TotalLines: 200, AuthorLines: map[int]int64{0: 120, 1: 80}},
		},
		SubsystemBusFactor: map[string]int{
			"src": 1,
		},
		Threshold:          0.8,
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
	}

	// Serialize to binary
	var buf bytes.Buffer
	err := bf.Serialize(result, true, &buf)
	assert.Nil(t, err)
	assert.Greater(t, buf.Len(), 0)

	// Deserialize
	rawResult2, err := bf.Deserialize(buf.Bytes())
	assert.Nil(t, err)
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

	assert.Equal(t, 1, result2.SubsystemBusFactor["src"])
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

func TestBusFactorMergeResults(t *testing.T) {
	bf := BusFactorAnalysis{}

	r1 := BusFactorResult{
		Snapshots: map[int]*BusFactorSnapshot{
			0: {BusFactor: 1, TotalLines: 100, AuthorLines: map[int]int64{0: 100}},
			5: {BusFactor: 2, TotalLines: 200, AuthorLines: map[int]int64{0: 120, 1: 80}},
		},
		SubsystemBusFactor: map[string]int{"src": 1},
		Threshold:          0.8,
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
	}

	r2 := BusFactorResult{
		Snapshots: map[int]*BusFactorSnapshot{
			5:  {BusFactor: 3, TotalLines: 300, AuthorLines: map[int]int64{0: 150, 1: 100, 2: 50}},
			10: {BusFactor: 1, TotalLines: 50, AuthorLines: map[int]int64{2: 50}},
		},
		SubsystemBusFactor: map[string]int{"src": 2, "docs": 1},
		Threshold:          0.8,
		reversedPeopleDict: []string{"Alice", "Bob", "Charlie"},
		tickSize:           24 * time.Hour,
	}

	c1 := &core.CommonAnalysisResult{}
	c2 := &core.CommonAnalysisResult{}

	merged := bf.MergeResults(r1, r2, c1, c2).(BusFactorResult)

	// Tick 0 only from r1
	assert.Equal(t, 1, merged.Snapshots[0].BusFactor)
	// Tick 5: r2 has more total lines (300 > 200), so r2 wins
	assert.Equal(t, 3, merged.Snapshots[5].BusFactor)
	assert.Equal(t, int64(300), merged.Snapshots[5].TotalLines)
	// Tick 10 only from r2
	assert.Equal(t, 1, merged.Snapshots[10].BusFactor)

	// Subsystem: max of both
	assert.Equal(t, 2, merged.SubsystemBusFactor["src"])
	assert.Equal(t, 1, merged.SubsystemBusFactor["docs"])
}
