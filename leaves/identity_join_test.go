package leaves

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/burndown"
	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/join"
)

const (
	joinedAliceIdentity = "identity-join-alice"
	joinedDuplicateFile = "identity-join-duplicate.go"
)

func TestDevsMergeResultsPreservesDuplicateIdentityRows(t *testing.T) {
	first := DevsResult{
		Ticks: map[int]map[int]*DevTick{
			0: {
				0: {Commits: 1},
				1: {Commits: 2},
			},
		},
		reversedPeopleDict: []string{joinedAliceIdentity, joinedAliceIdentity},
		tickSize:           time.Hour,
	}
	second := DevsResult{
		Ticks: map[int]map[int]*DevTick{
			0: {
				0: {Commits: 4},
			},
		},
		reversedPeopleDict: []string{joinedAliceIdentity},
		tickSize:           time.Hour,
	}
	common := &core.CommonAnalysisResult{}

	merged := (&DevsAnalysis{}).MergeResults(first, second, common, common).(DevsResult)

	require.Equal(t, []string{joinedAliceIdentity}, merged.reversedPeopleDict)
	require.Contains(t, merged.Ticks, 0)
	require.Contains(t, merged.Ticks[0], 0)
	assert.Equal(t, 7, merged.Ticks[0][0].Commits)
}

func TestCouplesMergePreservesDuplicateIdentityRows(t *testing.T) {
	first := CouplesResult{
		reversedPeopleDict: []string{joinedAliceIdentity, joinedAliceIdentity},
		Files:              []string{joinedDuplicateFile, joinedDuplicateFile},
		FilesLines:         []int{2, 3},
		PeopleFiles:        [][]int{{0}, {1}},
		PeopleMatrix: []map[int]int64{
			{0: 1},
			{1: 2},
			{2: 3},
		},
		FilesMatrix: []map[int]int64{
			{0: 1},
			{1: 2},
		},
	}

	merged, ok := (&CouplesAnalysis{}).MergeResults(
		first, CouplesResult{}, nil, nil,
	).(CouplesResult)
	require.True(t, ok)
	assert.Equal(t, CouplesResult{
		reversedPeopleDict: []string{joinedAliceIdentity},
		Files:              []string{joinedDuplicateFile},
		FilesLines:         []int{5},
		PeopleFiles:        [][]int{{0}},
		PeopleMatrix: []map[int]int64{
			{0: 3},
			{1: 3},
		},
		FilesMatrix: []map[int]int64{
			{0: 3},
		},
	}, merged)
}

func TestJoinedBurndownHistoriesPreservesDuplicateRows(t *testing.T) {
	mapping := join.IdentityMapping{
		First:  []int{0, 0},
		Second: []int{0},
	}
	first := []burndown.DenseHistory{
		{{1, 2}},
		{{3, 4}},
	}
	second := []burndown.DenseHistory{
		{{5, 6}},
	}

	firstJoined, secondJoined := joinedBurndownHistories(0, mapping, first, second)

	assert.Equal(t, burndown.DenseHistory{{4, 6}}, firstJoined)
	assert.Equal(t, burndown.DenseHistory{{5, 6}}, secondJoined)
}

func TestMergePeopleInteractionPreservesDuplicateRows(t *testing.T) {
	first := BurndownResult{
		reversedPeopleDict: []string{joinedAliceIdentity, joinedAliceIdentity},
		PeopleMatrix: burndown.DenseHistory{
			{1, 2, 3, 4},
			{10, 20, 30, 40},
		},
	}
	mapping := join.IdentityMapping{First: []int{0, 0}}

	merged := mergePeopleInteraction(
		first, BurndownResult{}, []string{joinedAliceIdentity}, mapping,
	)

	assert.Equal(t, burndown.DenseHistory{{11, 22, 77}}, merged)
}
