package leaves

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/test"
)

func TestBurndownDisposeRemovesHibernationFile(t *testing.T) {
	bd := BurndownAnalysis{}
	require.NoError(t, bd.Initialize(test.Repository))
	bd.HibernationToDisk = true
	bd.HibernationDirectory = t.TempDir()
	bd.globalHistory.updateDelta(0, 5, 100)
	require.NoError(t, bd.Hibernate())

	hibernatedFileName := bd.hibernatedFileName
	require.FileExists(t, hibernatedFileName)

	bd.Dispose()
	bd.Dispose()

	assert.Empty(t, bd.hibernatedFileName)
	_, err := os.Stat(hibernatedFileName)
	require.ErrorIs(t, err, os.ErrNotExist)
}
