package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFactValueDistinguishesMissingAndInvalidFacts(t *testing.T) {
	facts := map[string]any{
		"count": 7,
		"name":  "hercules",
	}

	count, exists, err := FactValue[int](facts, "count")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, 7, count)

	_, exists, err = FactValue[int](facts, "missing")
	require.NoError(t, err)
	assert.False(t, exists)

	_, exists, err = FactValue[int](facts, "name")
	require.Error(t, err)
	assert.True(t, exists)
	assert.ErrorIs(t, err, ErrInvalidFactType)

	var typeErr *FactTypeError
	require.ErrorAs(t, err, &typeErr)
	assert.Equal(t, "name", typeErr.Key)
	assert.Equal(t, "invalid fact type: \"name\" expects int, got string", err.Error())
}

func TestRequiredFactValueRejectsMissingFact(t *testing.T) {
	_, err := RequiredFactValue[string](map[string]any{}, "output")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFactMissing)
	assert.Contains(t, err.Error(), `"output"`)
}
