package leaves

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cwbudde/hercules/internal/burndown"
)

const (
	lifecycleBaselinePath = "baseline.yml"
	lifecycleBaselineHash = "291286b4ac41952cbd1389fda66420ec03c1a9fe"
	lifecycleTextHash     = "c29112dbd697ad9b401333b80c18a63951bc18d9"
	lifecycleTextHash2    = "f7d918ec500e2f925ecde79b51cc007bac27de72"
	lifecycleBinaryHash   = "c86626638e0bc8cf47ca49bb1525b40e9737ee64"
)

func assertBurndownHistoryNonNegative(
	t *testing.T, scope string, history burndown.DenseHistory,
) {
	t.Helper()

	for row, values := range history {
		for column, value := range values {
			assert.GreaterOrEqualf(
				t, value, int64(0),
				"%s contains a negative balance at row %d, column %d",
				scope, row, column,
			)
		}
	}
}
