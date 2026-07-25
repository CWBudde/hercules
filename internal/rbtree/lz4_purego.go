//go:build !cgo_lz4

package rbtree

import (
	"encoding/binary"
	"unsafe"

	lz4 "github.com/cwbudde/lz4"
)

// CompressUInt32Slice compresses a slice of uint32-s with LZ4.
func CompressUInt32Slice(data []uint32) []byte {
	src := unsafe.Slice((*byte)(unsafe.Pointer(&data[0])), len(data)*4)
	dst := make([]byte, lz4.CompressBlockBound(len(src))+8)
	binary.LittleEndian.PutUint64(dst, uint64(len(src)))

	compressedSize, err := lz4.CompressBlockHC(src, dst[8:], lz4.CompressionLevel(12), nil, nil)
	if err != nil || compressedSize == 0 {
		// incompressible: store raw with n=0 sentinel
		result := make([]byte, 8+len(src))
		binary.LittleEndian.PutUint64(result, uint64(len(src))|incompressibleFlag)
		copy(result[8:], src)

		return result
	}

	return dst[:8+compressedSize]
}

// DecompressUInt32Slice decompresses a slice of uint32-s previously compressed
// with CompressUInt32Slice. result must be preallocated.
func DecompressUInt32Slice(data []byte, result []uint32) {
	origLen := binary.LittleEndian.Uint64(data)

	dst := unsafe.Slice((*byte)(unsafe.Pointer(&result[0])), len(result)*4)
	if origLen&incompressibleFlag != 0 {
		copy(dst, data[8:])
		return
	}

	lz4.UncompressBlock(data[8:], dst) //nolint:errcheck
}

// incompressibleFlag is stored in the high bit of the 8-byte length header
// to signal that the payload is stored raw (lz4 expansion would be larger).
const incompressibleFlag = uint64(1) << 63
