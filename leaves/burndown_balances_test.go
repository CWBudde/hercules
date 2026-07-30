package leaves

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/burndown"
	"github.com/cwbudde/hercules/internal/core"
)

// recordingLogger captures the warnings a non-strict balance check emits.
type recordingLogger struct {
	core.Logger

	warnings []string
}

// balanceTestPerson is the single tracked author in the negative-balance fixture.
const balanceTestPerson = "alice"

func (logger *recordingLogger) Warn(args ...any) {
	logger.warnings = append(logger.warnings, args[0].(string))
}

// negativeBalanceResult carries two negative cells in two different scopes, the project one
// first in scan order and the person one the more negative of the two.
func negativeBalanceResult() BurndownResult {
	return BurndownResult{
		GlobalHistory:      burndown.DenseHistory{{5}, {0, 0, -3}},
		PeopleHistories:    []burndown.DenseHistory{{{5}, {0, 0, -9}}},
		PeopleMatrix:       burndown.DenseHistory{{5, 0, 0}},
		reversedPeopleDict: []string{balanceTestPerson},
		sampling:           5,
		granularity:        7,
	}
}

// TestBurndownSerializeWarnsOnNegativeBalancesByDefault pins the demotion: a negative alive-line
// count is a long-standing accounting defect (PLAN.md B1c/B2), and aborting over it discards
// every other analysis a multi-hour run computed. The default is now one warning per operation.
func TestBurndownSerializeWarnsOnNegativeBalancesByDefault(t *testing.T) {
	for _, binary := range []bool{false, true} {
		logger := &recordingLogger{Logger: core.NewLogger()}
		analyser := &BurndownAnalysis{l: logger}
		buffer := &bytes.Buffer{}

		require.NoError(t, analyser.Serialize(negativeBalanceResult(), binary, buffer))
		assert.NotEmpty(t, buffer.Bytes(), "the result must still be written")

		require.Len(t, logger.warnings, 1, "exactly one warning per operation")
		assert.Contains(t, logger.warnings[0], `person "`+balanceTestPerson+`" became -9`,
			"the warning names the worst cell, not the first one")
		assert.Contains(t, logger.warnings[0], "2 cell(s) affected in total")
		assert.Contains(t, logger.warnings[0], "person: 1, project: 1")
		assert.Contains(t, logger.warnings[0], "--strict-burndown-balances")
	}
}

// TestBurndownStrictBalancesReportsFirstCell keeps the strict message stable: it is the first
// offending cell in scan order, so it does not move as unrelated cells appear or disappear.
func TestBurndownStrictBalancesReportsFirstCell(t *testing.T) {
	logger := &recordingLogger{Logger: core.NewLogger()}
	analyser := &BurndownAnalysis{l: logger, StrictBalances: true}
	buffer := &bytes.Buffer{}

	err := analyser.Serialize(negativeBalanceResult(), false, buffer)
	require.ErrorIs(t, err, errNegativeBurndownBalance)

	var balanceError *negativeBurndownBalanceError

	require.ErrorAs(t, err, &balanceError)
	assert.Equal(t, "project", balanceError.Scope)
	assert.Equal(t, int64(-3), balanceError.Value)
	assert.Empty(t, buffer.Bytes())
	assert.Empty(t, logger.warnings, "strict mode fails instead of warning")
}

// TestBurndownMergeWarnsOnNegativeInputs covers `hercules combine`, which sees results produced
// by other runs and must not be taken down by one of them.
func TestBurndownMergeWarnsOnNegativeInputs(t *testing.T) {
	logger := &recordingLogger{Logger: core.NewLogger()}
	analyser := &BurndownAnalysis{l: logger}
	common := &core.CommonAnalysisResult{
		BeginTime:     600566400, // 1989 Jan 12
		EndTime:       604713600, // 1989 March 1
		CommitsNumber: 10,
		RunTime:       100000,
	}

	first := negativeBalanceResult()
	second := negativeBalanceResult()
	first.tickSize = 24 * 3600 * 1e9
	second.tickSize = first.tickSize

	merged := analyser.MergeResults(first, second, common, common)
	_, isError := merged.(error)
	require.False(t, isError, "merging must not fail on negative inputs by default")

	assert.NotEmpty(t, logger.warnings)
}

// TestLegacyBurndownSerializeWarnsOnNegativeBalances applies the same policy to the legacy item,
// which shares the flag through burndownSharedOptions().
func TestLegacyBurndownSerializeWarnsOnNegativeBalances(t *testing.T) {
	logger := &recordingLogger{Logger: core.NewLogger()}
	analyser := &LegacyBurndownAnalysis{l: logger}
	buffer := &bytes.Buffer{}

	require.NoError(t, analyser.Serialize(negativeBalanceResult(), false, buffer))
	assert.NotEmpty(t, buffer.Bytes())
	assert.Len(t, logger.warnings, 1)
}

// TestBurndownStrictBalancesIsConfigurable guards the flag plumbing for both items.
func TestBurndownStrictBalancesIsConfigurable(t *testing.T) {
	facts := map[string]any{ConfigBurndownStrictBalances: true}

	analyser := &BurndownAnalysis{}
	require.NoError(t, analyser.Configure(facts))
	assert.True(t, analyser.StrictBalances)

	legacy := &LegacyBurndownAnalysis{}
	require.NoError(t, legacy.Configure(facts))
	assert.True(t, legacy.StrictBalances)
}
