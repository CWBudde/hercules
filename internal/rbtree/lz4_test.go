package rbtree

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompressDecompressUInt32Slice(t *testing.T) {
	data := make([]uint32, 1000)
	for i := range data {
		data[i] = 7
	}
	packed := CompressUInt32Slice(data)
	assert.Less(t, len(packed), len(data)*4, "compressed size should be smaller than raw")
	for i := range data {
		data[i] = 0
	}
	DecompressUInt32Slice(packed, data)
	for i := range data {
		assert.Equal(t, uint32(7), data[i], i)
	}
}
