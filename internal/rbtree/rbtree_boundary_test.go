package rbtree

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDuplicateInsertDoesNotAllocate(t *testing.T) {
	allocator := NewAllocator()
	tree := NewRBTree(allocator)
	inserted, _ := tree.Insert(Item{Key: 7, Value: 11})
	require.True(t, inserted)

	size, used := allocator.Size(), allocator.Used()
	inserted, _ = tree.Insert(Item{Key: 7, Value: 99})

	assert.False(t, inserted)
	assert.Equal(t, size, allocator.Size())
	assert.Equal(t, used, allocator.Used())
	assert.Equal(t, uint32(11), *tree.Get(7))
}

func TestAllocatorDeserializeRejectsTruncatedData(t *testing.T) {
	serialized := serializedAllocatorFixture(t)

	for _, testCase := range []struct {
		name string
		data []byte
	}{
		{"header", serialized[:serializedAllocatorHeaderSize-1]},
		{"payload", serialized[:len(serialized)-1]},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := deserializeAllocatorFixture(t, testCase.data)
			require.Error(t, err)
			assert.ErrorIs(t, err, errIncompleteRead)
		})
	}
}

func TestAllocatorDeserializeRejectsOversizedLengths(t *testing.T) {
	serialized := serializedAllocatorFixture(t)

	t.Run("storage", func(t *testing.T) {
		data := append([]byte(nil), serialized...)
		binary.BigEndian.PutUint64(data[12:20], uint64(math.MaxUint32)+1)

		err := deserializeAllocatorFixture(t, data)
		require.Error(t, err)
		assert.ErrorIs(t, err, errInvalidSerializedAllocator)
	})

	t.Run("buffer", func(t *testing.T) {
		data := append([]byte(nil), serialized...)
		storageLength := binary.BigEndian.Uint64(data[12:20])
		binary.BigEndian.PutUint64(data[20:28], maxSerializedBufferLength(storageLength)+1)

		err := deserializeAllocatorFixture(t, data)
		require.Error(t, err)
		assert.ErrorIs(t, err, errInvalidSerializedAllocator)
	})
}

func TestAllocatorDeserializeValidatesVersionAndIntegrity(t *testing.T) {
	serialized := serializedAllocatorFixture(t)

	t.Run("version", func(t *testing.T) {
		data := append([]byte(nil), serialized...)
		binary.BigEndian.PutUint32(data[8:12], serializedAllocatorVersion+1)

		err := deserializeAllocatorFixture(t, data)
		require.Error(t, err)
		assert.ErrorIs(t, err, errInvalidSerializedAllocator)
	})

	t.Run("checksum", func(t *testing.T) {
		data := append([]byte(nil), serialized...)
		data[len(data)-1] ^= 0xff

		err := deserializeAllocatorFixture(t, data)
		require.Error(t, err)
		assert.ErrorIs(t, err, errAllocatorIntegrity)
	})
}

func serializedAllocatorFixture(t *testing.T) []byte {
	t.Helper()

	allocator := NewAllocator()
	for value := uint32(1); value <= 16; value++ {
		index := allocator.malloc()
		allocator.storage[index] = node{
			item:  Item{Key: value, Value: value},
			color: black,
		}
	}
	allocator.Hibernate()

	path := filepath.Join(t.TempDir(), "allocator")
	require.NoError(t, allocator.Serialize(path))
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return data
}

func deserializeAllocatorFixture(t *testing.T, data []byte) error {
	t.Helper()

	path := filepath.Join(t.TempDir(), "allocator")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	allocator := &Allocator{}
	err := allocator.Deserialize(path)
	assert.Zero(t, allocator.hibernatedStorageLen)
	for _, buffer := range allocator.hibernatedData {
		assert.Nil(t, buffer)
	}

	return err
}
