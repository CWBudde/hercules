package fixtures

import (
	"github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/test"
)

// FileDiff initializes a new plumbing.FileDiff item for testing.
func FileDiff() *plumbing.FileDiff {
	fd := &plumbing.FileDiff{}
	_ = fd.Initialize(test.Repository)
	return fd
}
