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

func TestOwnershipConcentrationMeta(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	assert.Equal(t, "OwnershipConcentration", oc.Name())
	assert.Len(t, oc.Provides(), 0)
	assert.Contains(t, oc.Requires(), identity.DependencyAuthor)
	assert.Contains(t, oc.Requires(), items.DependencyTick)
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
	facts := map[string]interface{}{}
	facts[identity.FactIdentityDetectorReversedPeopleDict] = []string{"Alice", "Bob"}
	facts[items.FactTickSize] = 24 * time.Hour
	logger := core.NewLogger()
	facts[core.ConfigLogger] = logger

	assert.Nil(t, oc.Configure(facts))
	assert.Equal(t, []string{"Alice", "Bob"}, oc.reversedPeopleDict)
	assert.Equal(t, 24*time.Hour, oc.tickSize)
}

func TestOwnershipConcentrationInitialize(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	assert.Nil(t, oc.Initialize(test.Repository))
	assert.NotNil(t, oc.snapshots)
	assert.Equal(t, -1, oc.lastTick)
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
	oc.reversedPeopleDict = []string{"Alice", "Bob"}
	oc.tickSize = 24 * time.Hour
	assert.Nil(t, oc.Initialize(test.Repository))

	oc.snapshots[0] = &OwnershipConcentrationSnapshot{
		Gini:        0.25,
		HHI:         0.5,
		TotalLines:  100,
		AuthorLines: map[int]int64{0: 60, 1: 40},
	}

	result := oc.Finalize()
	assert.NotNil(t, result)

	ocr := result.(OwnershipConcentrationResult)
	assert.Equal(t, []string{"Alice", "Bob"}, ocr.reversedPeopleDict)
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
			"src":  {Gini: 0.3, HHI: 0.6},
			"docs": {Gini: 0.0, HHI: 0.5},
		},
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
	}

	var buf bytes.Buffer
	err := oc.Serialize(result, false, &buf)
	assert.Nil(t, err)

	output := buf.String()
	assert.Contains(t, output, "ownership_concentration:")
	assert.Contains(t, output, "per_tick:")
	assert.Contains(t, output, "gini: 0.0000")
	assert.Contains(t, output, "hhi: 1.0000")
	assert.Contains(t, output, "gini: 0.2500")
	assert.Contains(t, output, "per_subsystem:")
	assert.Contains(t, output, "people:")
	assert.Contains(t, output, "Alice")
	assert.Contains(t, output, "Bob")
}

func TestOwnershipConcentrationSerializeBinaryRoundtrip(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}
	result := OwnershipConcentrationResult{
		Snapshots: map[int]*OwnershipConcentrationSnapshot{
			0: {Gini: 0.0, HHI: 1.0, TotalLines: 100, AuthorLines: map[int]int64{0: 100}},
			5: {Gini: 0.25, HHI: 0.52, TotalLines: 200, AuthorLines: map[int]int64{0: 120, 1: 80}},
		},
		SubsystemConcentration: map[string]*SubsystemConcentration{
			"src": {Gini: 0.3, HHI: 0.6},
		},
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
	}

	var buf bytes.Buffer
	err := oc.Serialize(result, true, &buf)
	assert.Nil(t, err)
	assert.Greater(t, buf.Len(), 0)

	rawResult2, err := oc.Deserialize(buf.Bytes())
	assert.Nil(t, err)
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

	assert.InDelta(t, 0.3, result2.SubsystemConcentration["src"].Gini, 0.001)
	assert.InDelta(t, 0.6, result2.SubsystemConcentration["src"].HHI, 0.001)
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

func TestOwnershipConcentrationMergeResults(t *testing.T) {
	oc := OwnershipConcentrationAnalysis{}

	r1 := OwnershipConcentrationResult{
		Snapshots: map[int]*OwnershipConcentrationSnapshot{
			0: {Gini: 0.0, HHI: 1.0, TotalLines: 100, AuthorLines: map[int]int64{0: 100}},
			5: {Gini: 0.2, HHI: 0.5, TotalLines: 200, AuthorLines: map[int]int64{0: 120, 1: 80}},
		},
		SubsystemConcentration: map[string]*SubsystemConcentration{
			"src": {Gini: 0.3, HHI: 0.6},
		},
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
	}

	r2 := OwnershipConcentrationResult{
		Snapshots: map[int]*OwnershipConcentrationSnapshot{
			5:  {Gini: 0.3, HHI: 0.4, TotalLines: 300, AuthorLines: map[int]int64{0: 150, 1: 100, 2: 50}},
			10: {Gini: 0.1, HHI: 0.5, TotalLines: 50, AuthorLines: map[int]int64{2: 50}},
		},
		SubsystemConcentration: map[string]*SubsystemConcentration{
			"src":  {Gini: 0.4, HHI: 0.7},
			"docs": {Gini: 0.0, HHI: 0.5},
		},
		reversedPeopleDict: []string{"Alice", "Bob", "Charlie"},
		tickSize:           24 * time.Hour,
	}

	c1 := &core.CommonAnalysisResult{}
	c2 := &core.CommonAnalysisResult{}

	merged := oc.MergeResults(r1, r2, c1, c2).(OwnershipConcentrationResult)

	// Tick 0 only from r1
	assert.InDelta(t, 0.0, merged.Snapshots[0].Gini, 0.001)
	// Tick 5: r2 has more total lines (300 > 200), so r2 wins
	assert.InDelta(t, 0.3, merged.Snapshots[5].Gini, 0.001)
	assert.Equal(t, int64(300), merged.Snapshots[5].TotalLines)
	// Tick 10 only from r2
	assert.InDelta(t, 0.1, merged.Snapshots[10].Gini, 0.001)

	// Subsystem: r1 takes priority for "src", r2 adds "docs"
	assert.InDelta(t, 0.3, merged.SubsystemConcentration["src"].Gini, 0.001)
	assert.InDelta(t, 0.0, merged.SubsystemConcentration["docs"].Gini, 0.001)
}
