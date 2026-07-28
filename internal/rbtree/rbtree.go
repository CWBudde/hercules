package rbtree

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"sync"
)

var (
	errIncompleteRead             = errors.New("incomplete read")
	errInvalidSerializedAllocator = errors.New("invalid serialized allocator")
	errAllocatorIntegrity         = errors.New("serialized allocator integrity check failed")
)

const (
	serializedAllocatorMagic       = "HCRBALOC"
	serializedAllocatorVersion     = uint32(1)
	serializedAllocatorBufferCount = 6
	serializedAllocatorHeaderSize  = 8 + 4 + 8 + serializedAllocatorBufferCount*(8+4)
)

//
// Public definitions
//

// Item is the object stored in each tree node.
type Item struct {
	Key   uint32
	Value uint32
}

// Allocator is the allocator for nodes in a RBTree.
type Allocator struct {
	HibernationThreshold int

	storage              []node
	gapCount             uint32
	nextGap              uint32
	hibernatedData       [6][]byte
	hibernatedStorageLen int
}

// NewAllocator creates a new allocator for RBTree's nodes.
func NewAllocator() *Allocator {
	return &Allocator{
		storage: []node{},
	}
}

// Size returns the currently allocated size.
func (allocator *Allocator) Size() int {
	return len(allocator.storage)
}

// Used returns the number of nodes contained in the allocator.
func (allocator *Allocator) Used() int {
	if allocator.storage == nil {
		panic("hibernated allocators cannot be used")
	}

	return len(allocator.storage) - int(allocator.gapCount)
}

// Clone copies an existing RBTree allocator.
func (allocator *Allocator) Clone() *Allocator {
	if allocator.storage == nil {
		panic("cannot clone a hibernated allocator")
	}

	return &Allocator{
		HibernationThreshold: allocator.HibernationThreshold,
		storage:              append(make([]node, 0, cap(allocator.storage)), allocator.storage...),
		gapCount:             allocator.gapCount,
		nextGap:              allocator.nextGap,
	}
}

// Hibernate compresses the allocated memory.
func (allocator *Allocator) Hibernate() {
	if allocator.hibernatedStorageLen > 0 {
		panic("cannot hibernate an already hibernated Allocator")
	}

	if len(allocator.storage) < allocator.HibernationThreshold {
		return
	}

	allocator.hibernatedStorageLen = len(allocator.storage)
	if allocator.hibernatedStorageLen == 0 {
		return
	}

	buffers, empty := allocator.deinterleave()
	if empty {
		return
	}

	doAssert(allocator.gapCount == 0)
	allocator.nextGap = 0
	allocator.storage = nil
	allocator.compressBuffers(buffers)
}

// Boot performs the opposite of Hibernate() - decompresses and restores the allocated memory.
func (allocator *Allocator) Boot() {
	if allocator.hibernatedStorageLen == 0 {
		// not hibernated
		return
	}

	if allocator.hibernatedData[0] == nil {
		panic("cannot boot a serialized Allocator")
	}

	doAssert(allocator.gapCount == 0)
	allocator.nextGap = 0

	buffers := [6][]uint32{}
	waitGroup := &sync.WaitGroup{}
	waitGroup.Add(len(buffers))

	for bufferIndex := range buffers {
		go func(index int) {
			buffers[index] = make([]uint32, allocator.hibernatedStorageLen)
			DecompressUInt32Slice(allocator.hibernatedData[index], buffers[index])
			allocator.hibernatedData[index] = nil

			waitGroup.Done()
		}(bufferIndex)
	}

	waitGroup.Wait()

	allocator.storage = make([]node, allocator.hibernatedStorageLen, (allocator.hibernatedStorageLen*3)/2)
	allocator.hibernatedStorageLen = 0

	for nodeIndex := range allocator.storage {
		currentNode := &allocator.storage[nodeIndex]

		switch buffers[5][nodeIndex] {
		case uint32(red):
			currentNode.color = red
		case uint32(black):
			currentNode.color = black
		case uint32(gap):
			currentNode.color = gap
		default:
			panic("invalid serialized node color")
		}

		if currentNode.color == gap {
			currentNode.right = allocator.nextGap
			allocator.nextGap = uint32(nodeIndex)
			allocator.gapCount++

			continue
		}

		currentNode.item.Key = buffers[0][nodeIndex]
		currentNode.item.Value = buffers[1][nodeIndex]
		currentNode.left = buffers[2][nodeIndex]
		currentNode.parent = buffers[3][nodeIndex]
		currentNode.right = buffers[4][nodeIndex]
	}
}

// Serialize writes the hibernated allocator on disk. The format is private to
// Hercules' run-local hibernation files, but still carries a version and per-buffer
// checksum so truncated or corrupted temporary state is rejected deterministically.
func (allocator *Allocator) Serialize(path string) error {
	if allocator.storage != nil {
		panic("serialization requires the hibernated state")
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create serialized allocator %q: %w", path, err)
	}
	// The explicit close below reports flush failures. This deferred close only
	// covers earlier validation/write errors, which remain the primary error.
	defer func() { _ = file.Close() }()

	header := make([]byte, serializedAllocatorHeaderSize)
	copy(header, serializedAllocatorMagic)
	binary.BigEndian.PutUint32(header[8:12], serializedAllocatorVersion)

	if allocator.hibernatedStorageLen < 0 {
		return fmt.Errorf("%w: negative storage length %d",
			errInvalidSerializedAllocator, allocator.hibernatedStorageLen)
	}

	storageLength := uint64(allocator.hibernatedStorageLen)
	binary.BigEndian.PutUint64(header[12:20], storageLength)

	offset := 20
	for _, hibernatedBuffer := range allocator.hibernatedData {
		binary.BigEndian.PutUint64(header[offset:offset+8], uint64(len(hibernatedBuffer)))
		binary.BigEndian.PutUint32(header[offset+8:offset+12], crc32.ChecksumIEEE(hibernatedBuffer))
		offset += 12
	}

	err = writeFull(file, header)
	if err != nil {
		return fmt.Errorf("write allocator header: %w", err)
	}

	for bufferIndex, hibernatedBuffer := range allocator.hibernatedData {
		err = writeFull(file, hibernatedBuffer)
		if err != nil {
			return fmt.Errorf("write allocator buffer %d: %w", bufferIndex, err)
		}
	}

	for bufferIndex := range allocator.hibernatedData {
		allocator.hibernatedData[bufferIndex] = nil
	}

	err = file.Close()
	if err != nil {
		return fmt.Errorf("close serialized allocator %q: %w", path, err)
	}

	return nil
}

// Deserialize reads a hibernated allocator from disk.
func (allocator *Allocator) Deserialize(path string) error {
	if allocator.storage != nil {
		panic("deserialization requires the hibernated state")
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open serialized allocator %q: %w", path, err)
	}
	// Closing a read-only regular file cannot change the validated in-memory
	// result, so the close error is intentionally discarded.
	defer func() { _ = file.Close() }()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat serialized allocator %q: %w", path, err)
	}
	metadata, err := readSerializedAllocatorMetadata(file, fileInfo.Size())
	if err != nil {
		return err
	}
	hibernatedData, err := readSerializedAllocatorBuffers(file, metadata)
	if err != nil {
		return err
	}
	allocator.hibernatedStorageLen = int(metadata.storageLength)
	allocator.hibernatedData = hibernatedData
	return nil
}

type serializedAllocatorMetadata struct {
	storageLength uint64
	bufferLengths [serializedAllocatorBufferCount]uint64
	checksums     [serializedAllocatorBufferCount]uint32
}

func readSerializedAllocatorMetadata(reader io.Reader, fileSize int64) (serializedAllocatorMetadata, error) {
	if fileSize < serializedAllocatorHeaderSize {
		return serializedAllocatorMetadata{}, fmt.Errorf("%w: allocator header: got %d bytes, need %d",
			errIncompleteRead, fileSize, serializedAllocatorHeaderSize)
	}
	header := make([]byte, serializedAllocatorHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return serializedAllocatorMetadata{}, fmt.Errorf("%w: allocator header: %w", errIncompleteRead, err)
	}
	if string(header[:8]) != serializedAllocatorMagic {
		return serializedAllocatorMetadata{}, fmt.Errorf("%w: unexpected format magic", errInvalidSerializedAllocator)
	}
	version := binary.BigEndian.Uint32(header[8:12])
	if version != serializedAllocatorVersion {
		return serializedAllocatorMetadata{}, fmt.Errorf("%w: unsupported version %d", errInvalidSerializedAllocator, version)
	}
	storageLength := binary.BigEndian.Uint64(header[12:20])
	if storageLength > uint64(negativeLimitNode) || storageLength > maxIntValue() {
		return serializedAllocatorMetadata{}, fmt.Errorf("%w: storage length %d exceeds the supported limit",
			errInvalidSerializedAllocator, storageLength)
	}
	metadata := serializedAllocatorMetadata{storageLength: storageLength}
	payloadLength, err := parseSerializedAllocatorBufferMetadata(header, &metadata)
	if err != nil {
		return serializedAllocatorMetadata{}, err
	}
	actualPayloadLength := fileSize - serializedAllocatorHeaderSize
	if payloadLength != uint64(actualPayloadLength) {
		return serializedAllocatorMetadata{}, fmt.Errorf(
			"%w: allocator payload: got %d bytes, expected %d",
			errIncompleteRead, actualPayloadLength, payloadLength,
		)
	}
	return metadata, nil
}

func parseSerializedAllocatorBufferMetadata(
	header []byte,
	metadata *serializedAllocatorMetadata,
) (uint64, error) {
	var payloadLength uint64
	maxBufferLength := maxSerializedBufferLength(metadata.storageLength)
	offset := 20
	for bufferIndex := range metadata.bufferLengths {
		bufferLength := binary.BigEndian.Uint64(header[offset : offset+8])
		metadata.checksums[bufferIndex] = binary.BigEndian.Uint32(header[offset+8 : offset+12])
		offset += 12
		if bufferLength > maxIntValue() || bufferLength > maxBufferLength {
			return 0, fmt.Errorf("%w: buffer %d length %d exceeds the supported limit %d",
				errInvalidSerializedAllocator, bufferIndex, bufferLength, maxBufferLength)
		}
		if math.MaxUint64-payloadLength < bufferLength {
			return 0, fmt.Errorf("%w: allocator payload length overflow", errInvalidSerializedAllocator)
		}
		metadata.bufferLengths[bufferIndex] = bufferLength
		payloadLength += bufferLength
	}
	return payloadLength, nil
}

func readSerializedAllocatorBuffers(
	reader io.Reader,
	metadata serializedAllocatorMetadata,
) ([serializedAllocatorBufferCount][]byte, error) {
	var hibernatedData [serializedAllocatorBufferCount][]byte
	for bufferIndex, bufferLength := range metadata.bufferLengths {
		bufferLengthInt := int(bufferLength) //nolint:gosec // validated against maxIntValue above
		hibernatedData[bufferIndex] = make([]byte, bufferLengthInt)
		if _, err := io.ReadFull(reader, hibernatedData[bufferIndex]); err != nil {
			return [serializedAllocatorBufferCount][]byte{}, fmt.Errorf("%w: allocator buffer %d: %w",
				errIncompleteRead, bufferIndex, err)
		}
		if crc32.ChecksumIEEE(hibernatedData[bufferIndex]) != metadata.checksums[bufferIndex] {
			return [serializedAllocatorBufferCount][]byte{}, fmt.Errorf(
				"%w: buffer %d", errAllocatorIntegrity, bufferIndex,
			)
		}
	}
	return hibernatedData, nil
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return fmt.Errorf("write serialized allocator: %w", err)
		}

		if written == 0 {
			return io.ErrShortWrite
		}

		data = data[written:]
	}

	return nil
}

func maxIntValue() uint64 {
	return uint64(^uint(0) >> 1)
}

func maxSerializedBufferLength(storageLength uint64) uint64 {
	rawLength := storageLength * 4
	// LZ4_compressBound is raw + raw/255 + 16. The pure-Go encoding also
	// carries an eight-byte original-length prefix.
	return rawLength + rawLength/255 + 24
}

func (allocator *Allocator) malloc() uint32 {
	if allocator.storage == nil {
		panic("hibernated allocators cannot be used")
	}

	if allocator.gapCount > 0 {
		allocator.gapCount--
		key := allocator.nextGap
		gapNode := &allocator.storage[key]
		allocator.nextGap = gapNode.right

		*gapNode = node{}

		return key
	}

	switch storageLength := len(allocator.storage); {
	case storageLength == 0:
		// zero is reserved
		allocator.storage = append(allocator.storage, node{}, node{})
		return 1
	case storageLength < negativeLimitNode:
		allocator.storage = append(allocator.storage, node{})
		return uint32(storageLength)
	}

	panic("the size of my RBTree allocator has reached the maximum value for uint32")
}

func (allocator *Allocator) free(key uint32) {
	if allocator.storage == nil {
		panic("hibernated allocators cannot be used")
	}

	if key == 0 {
		panic("node #0 is special and cannot be deallocated")
	}

	doAssert(allocator.gapCount == 0 || allocator.nextGap != 0)

	gapNode := &allocator.storage[key]
	doAssert(gapNode.color != gap)
	*gapNode = node{
		right: allocator.nextGap,
		color: gap,
	}
	allocator.gapCount++
	allocator.nextGap = key
}

// RBTree is a red-black tree with an API similar to C++ STL's.
//
// The implementation is inspired (read: stolen) from:
// http://en.literateprograms.org/Red-black_tree_(C)#chunk use:private function prototypes.
//
// The code was optimized for the simple integer types of Key and Value.
// The code was further optimized for using allocators.
// Credits: Yaz Saito.
type RBTree struct {
	// Root of the tree
	root uint32

	// The minimum and maximum nodes under the tree.
	minNode, maxNode uint32

	// Number of nodes under root, including the root
	count int32

	// Nodes allocator
	allocator *Allocator
}

// NewRBTree creates a new red-black binary tree.
func NewRBTree(allocator *Allocator) *RBTree {
	return &RBTree{allocator: allocator}
}

// Allocator returns the bound nodes allocator.
func (tree *RBTree) Allocator() *Allocator {
	return tree.allocator
}

// Len returns the number of elements in the tree.
func (tree *RBTree) Len() int {
	return int(tree.count)
}

// CloneShallow performs a shallow copy of the tree - the nodes are assumed to already exist in the allocator.
func (tree *RBTree) CloneShallow(allocator *Allocator) *RBTree {
	clone := *tree
	clone.allocator = allocator

	return &clone
}

// CloneDeep performs a deep copy of the tree - the nodes are created from scratch.
func (tree *RBTree) CloneDeep(allocator *Allocator) *RBTree {
	clone := &RBTree{
		count:     tree.count,
		allocator: allocator,
	}
	nodeMap := map[uint32]uint32{}

	originStorage := tree.storage()
	for iter := tree.Min(); !iter.Limit(); iter = iter.Next() {
		newNode := allocator.malloc()
		cloneNode := &allocator.storage[newNode]
		cloneNode.item = *iter.Item()
		cloneNode.color = originStorage[iter.node].color
		nodeMap[iter.node] = newNode
	}

	cloneStorage := allocator.storage
	for iter := tree.Min(); !iter.Limit(); iter = iter.Next() {
		cloneNode := &cloneStorage[nodeMap[iter.node]]
		originNode := originStorage[iter.node]
		cloneNode.left = nodeMap[originNode.left]
		cloneNode.right = nodeMap[originNode.right]
		cloneNode.parent = nodeMap[originNode.parent]
	}

	clone.root = nodeMap[tree.root]
	clone.minNode = nodeMap[tree.minNode]
	clone.maxNode = nodeMap[tree.maxNode]

	return clone
}

// Erase removes all the nodes from the tree.
func (tree *RBTree) Erase() {
	nodes := make([]uint32, 0, tree.count)
	for iter := tree.Min(); !iter.Limit(); iter = iter.Next() {
		nodes = append(nodes, iter.node)
	}

	for _, node := range nodes {
		tree.allocator.free(node)
	}

	tree.root = 0
	tree.minNode = 0
	tree.maxNode = 0
	tree.count = 0
}

// Get is a convenience function for finding an element equal to Key. Returns
// nil if not found.
func (tree *RBTree) Get(key uint32) *uint32 {
	n, exact := tree.findGE(key)
	if exact {
		return &tree.storage()[n].item.Value
	}

	return nil
}

// Min creates an iterator that points to the minimum item in the tree.
// If the tree is empty, returns Limit().
func (tree *RBTree) Min() Iterator {
	return Iterator{tree, tree.minNode}
}

// Max creates an iterator that points at the maximum item in the tree.
//
// If the tree is empty, returns NegativeLimit().
func (tree *RBTree) Max() Iterator {
	if tree.maxNode == 0 {
		return Iterator{tree, negativeLimitNode}
	}

	return Iterator{tree, tree.maxNode}
}

// Limit creates an iterator that points beyond the maximum item in the tree.
func (tree *RBTree) Limit() Iterator {
	return Iterator{tree, 0}
}

// NegativeLimit creates an iterator that points before the minimum item in the tree.
func (tree *RBTree) NegativeLimit() Iterator {
	return Iterator{tree, negativeLimitNode}
}

// FindGE finds the smallest element N such that N >= Key, and returns the
// iterator pointing to the element. If no such element is found,
// returns tree.Limit().
func (tree *RBTree) FindGE(key uint32) Iterator {
	n, _ := tree.findGE(key)
	return Iterator{tree, n}
}

// FindLE finds the largest element N such that N <= Key, and returns the
// iterator pointing to the element. If no such element is found,
// returns iter.NegativeLimit().
func (tree *RBTree) FindLE(key uint32) Iterator {
	nodeIndex, exact := tree.findGE(key)
	if exact {
		return Iterator{tree, nodeIndex}
	}

	if nodeIndex != 0 {
		return Iterator{tree, doPrev(nodeIndex, tree.storage())}
	}

	if tree.maxNode == 0 {
		return Iterator{tree, negativeLimitNode}
	}

	return Iterator{tree, tree.maxNode}
}

// Insert an item. If the item is already in the tree, do nothing and
// return false. Else return true.
func (tree *RBTree) Insert(item Item) (bool, Iterator) {
	nodeIndex := tree.doInsert(item)
	if nodeIndex == 0 {
		return false, Iterator{}
	}

	alloc := tree.storage()
	insertedNode := nodeIndex
	alloc[nodeIndex].color = red
	tree.rebalanceAfterInsert(nodeIndex, alloc)

	return true, Iterator{tree, insertedNode}
}

// DeleteWithKey deletes an item with the given Key. Returns true iff the item was
// found.
func (tree *RBTree) DeleteWithKey(key uint32) bool {
	nodeIndex, exact := tree.findGE(key)
	if exact {
		tree.doDelete(nodeIndex)
		return true
	}

	return false
}

// DeleteWithIterator deletes the current item.
//
// REQUIRES: !iter.Limit() && !iter.NegativeLimit().
func (tree *RBTree) DeleteWithIterator(iter Iterator) {
	doAssert(!iter.Limit() && !iter.NegativeLimit())
	tree.doDelete(iter.node)
}

func (allocator *Allocator) deinterleave() ([6][]uint32, bool) {
	buffers := [6][]uint32{}
	for i := range buffers {
		buffers[i] = make([]uint32, len(allocator.storage))
	}

	for nodeIndex, current := range allocator.storage {
		buffers[5][nodeIndex] = uint32(current.color)
		if current.color != gap {
			buffers[0][nodeIndex], buffers[1][nodeIndex] = current.item.Key, current.item.Value
			buffers[2][nodeIndex] = current.left
			buffers[3][nodeIndex] = current.parent
			buffers[4][nodeIndex] = current.right

			continue
		}

		if nodeIndex+int(allocator.gapCount) == allocator.hibernatedStorageLen {
			allocator.gapCount = 0
			if nodeIndex <= 1 {
				allocator.hibernatedStorageLen, allocator.nextGap = 0, 0
				allocator.storage = allocator.storage[:0]

				return buffers, true
			}

			allocator.hibernatedStorageLen = nodeIndex

			break
		}

		allocator.gapCount--
	}

	return buffers, false
}

func (allocator *Allocator) compressBuffers(buffers [6][]uint32) {
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(buffers))

	for bufferIndex, buffer := range buffers {
		go func(index int, buffer []uint32) {
			defer waitGroup.Done()

			allocator.hibernatedData[index] = CompressUInt32Slice(buffer[:allocator.hibernatedStorageLen])
			buffers[index] = nil
		}(bufferIndex, buffer)
	}

	waitGroup.Wait()
}

func (tree *RBTree) storage() []node {
	return tree.allocator.storage
}

func (tree *RBTree) rebalanceAfterInsert(nodeIndex uint32, alloc []node) {
	for {
		parent := alloc[nodeIndex].parent
		if parent == 0 {
			alloc[nodeIndex].color = black

			return
		}

		if alloc[parent].color == black {
			return
		}

		grandparent := alloc[parent].parent
		uncle := insertUncle(parent, grandparent, alloc)

		if uncle != 0 && alloc[uncle].color == red {
			nodeIndex = recolorInsertFamily(parent, uncle, grandparent, alloc)

			continue
		}

		rotatedNode, rotated := tree.rotateInsertTriangle(nodeIndex, parent, alloc)
		if rotated {
			nodeIndex = rotatedNode
			continue
		}

		alloc[parent].color = black
		alloc[grandparent].color = red

		if isLeftChild(nodeIndex, alloc) {
			tree.rotateRight(grandparent)
		} else {
			tree.rotateLeft(grandparent)
		}

		return
	}
}

func insertUncle(parent, grandparent uint32, alloc []node) uint32 {
	if isLeftChild(parent, alloc) {
		return alloc[grandparent].right
	}

	return alloc[grandparent].left
}

func recolorInsertFamily(parent, uncle, grandparent uint32, alloc []node) uint32 {
	alloc[parent].color = black
	alloc[uncle].color = black
	alloc[grandparent].color = red

	return grandparent
}

func (tree *RBTree) rotateInsertTriangle(nodeIndex, parent uint32, alloc []node) (uint32, bool) {
	if isRightChild(nodeIndex, alloc) && isLeftChild(parent, alloc) {
		tree.rotateLeft(parent)

		return alloc[nodeIndex].left, true
	}

	if isLeftChild(nodeIndex, alloc) && isRightChild(parent, alloc) {
		tree.rotateRight(parent)

		return alloc[nodeIndex].right, true
	}

	return nodeIndex, false
}

// Iterator allows scanning tree elements in sort order.
//
// Iterator invalidation rule is the same as C++ std::map<>'s. That
// is, if you delete the element that an iterator points to, the
// iterator becomes invalid. For other operation types, the iterator
// remains valid.
type Iterator struct {
	tree *RBTree
	node uint32
}

// Equal checks for the underlying nodes equality.
func (iter Iterator) Equal(other Iterator) bool {
	return iter.node == other.node
}

// Limit checks if the iterator points beyond the max element in the tree.
func (iter Iterator) Limit() bool {
	return iter.node == 0
}

// Min checks if the iterator points to the minimum element in the tree.
func (iter Iterator) Min() bool {
	return iter.node == iter.tree.minNode
}

// Max checks if the iterator points to the maximum element in the tree.
func (iter Iterator) Max() bool {
	return iter.node == iter.tree.maxNode
}

// NegativeLimit checks if the iterator points before the minimum element in the tree.
func (iter Iterator) NegativeLimit() bool {
	return iter.node == negativeLimitNode
}

// Item returns the current element. Allows mutating the node
// (key to be changed with care!).
//
// The result is nil if iter.Limit() || iter.NegativeLimit().
func (iter Iterator) Item() *Item {
	if iter.Limit() || iter.NegativeLimit() {
		return nil
	}

	return &iter.tree.storage()[iter.node].item
}

// Next creates a new iterator that points to the successor of the current element.
//
// REQUIRES: !iter.Limit().
func (iter Iterator) Next() Iterator {
	doAssert(!iter.Limit())

	if iter.NegativeLimit() {
		return Iterator{iter.tree, iter.tree.minNode}
	}

	return Iterator{iter.tree, doNext(iter.node, iter.tree.storage())}
}

// Prev creates a new iterator that points to the predecessor of the current
// node.
//
// REQUIRES: !iter.NegativeLimit().
func (iter Iterator) Prev() Iterator {
	doAssert(!iter.NegativeLimit())

	if !iter.Limit() {
		return Iterator{iter.tree, doPrev(iter.node, iter.tree.storage())}
	}

	if iter.tree.maxNode == 0 {
		return Iterator{iter.tree, negativeLimitNode}
	}

	return Iterator{iter.tree, iter.tree.maxNode}
}

func doAssert(b bool) {
	if !b {
		panic("rbtree internal assertion failed")
	}
}

const (
	red               = color(0)
	black             = color(1)
	negativeLimitNode = math.MaxUint32
	gap               = color(2)
)

type color uint8

type node struct {
	item        Item
	parent      uint32
	left, right uint32
	color       color // black or red or gap
}

// Internal node attribute accessors.
func getColor(n uint32, allocator []node) color {
	if n == 0 {
		return black
	}

	c := allocator[n].color
	doAssert(c < gap)

	return c
}

func isLeftChild(n uint32, allocator []node) bool {
	return n == allocator[allocator[n].parent].left
}

func isRightChild(n uint32, allocator []node) bool {
	return n == allocator[allocator[n].parent].right
}

func sibling(nodeIndex uint32, allocator []node) uint32 {
	doAssert(allocator[nodeIndex].parent != 0)

	if isLeftChild(nodeIndex, allocator) {
		return allocator[allocator[nodeIndex].parent].right
	}

	return allocator[allocator[nodeIndex].parent].left
}

// Return the minimum node that's larger than N. Return nil if no such
// node is found.
func doNext(nodeIndex uint32, allocator []node) uint32 {
	if allocator[nodeIndex].right != 0 {
		m := allocator[nodeIndex].right
		for allocator[m].left != 0 {
			m = allocator[m].left
		}

		return m
	}

	for nodeIndex != 0 {
		parentIndex := allocator[nodeIndex].parent
		if parentIndex == 0 {
			return 0
		}

		if isLeftChild(nodeIndex, allocator) {
			return parentIndex
		}

		nodeIndex = parentIndex
	}

	return 0
}

// Return the maximum node that's smaller than N. Return nil if no
// such node is found.
func doPrev(nodeIndex uint32, allocator []node) uint32 {
	if allocator[nodeIndex].left != 0 {
		return maxPredecessor(nodeIndex, allocator)
	}

	for nodeIndex != 0 {
		parentIndex := allocator[nodeIndex].parent
		if parentIndex == 0 {
			break
		}

		if isRightChild(nodeIndex, allocator) {
			return parentIndex
		}

		nodeIndex = parentIndex
	}

	return negativeLimitNode
}

// Return the predecessor of "n".
func maxPredecessor(n uint32, allocator []node) uint32 {
	doAssert(allocator[n].left != 0)

	m := allocator[n].left
	for allocator[m].right != 0 {
		m = allocator[m].right
	}

	return m
}

//
// Tree methods
//

//
// Private methods
//

func (tree *RBTree) recomputeMinNode() {
	alloc := tree.storage()

	tree.minNode = tree.root
	if tree.minNode != 0 {
		for alloc[tree.minNode].left != 0 {
			tree.minNode = alloc[tree.minNode].left
		}
	}
}

func (tree *RBTree) recomputeMaxNode() {
	alloc := tree.storage()

	tree.maxNode = tree.root
	if tree.maxNode != 0 {
		for alloc[tree.maxNode].right != 0 {
			tree.maxNode = alloc[tree.maxNode].right
		}
	}
}

func (tree *RBTree) maybeSetMinNode(nodeIndex uint32) {
	alloc := tree.storage()
	if tree.minNode == 0 {
		tree.minNode = nodeIndex
		tree.maxNode = nodeIndex
	} else if alloc[nodeIndex].item.Key < alloc[tree.minNode].item.Key {
		tree.minNode = nodeIndex
	}
}

func (tree *RBTree) maybeSetMaxNode(nodeIndex uint32) {
	alloc := tree.storage()
	if tree.maxNode == 0 {
		tree.minNode = nodeIndex
		tree.maxNode = nodeIndex
	} else if alloc[nodeIndex].item.Key > alloc[tree.maxNode].item.Key {
		tree.maxNode = nodeIndex
	}
}

// Try inserting "item" into the tree. Return nil if the item is
// already in the tree. Otherwise return a new (leaf) node.
func (tree *RBTree) doInsert(item Item) uint32 {
	if tree.root == 0 {
		nodeIndex := tree.allocator.malloc()
		tree.storage()[nodeIndex].item = item
		tree.root = nodeIndex
		tree.minNode = nodeIndex
		tree.maxNode = nodeIndex
		tree.count++

		return nodeIndex
	}

	parent := tree.root

	storage := tree.storage()
	for {
		parentNode := storage[parent]

		comp := int(item.Key) - int(parentNode.item.Key)
		switch {
		case comp == 0:
			return 0
		case comp < 0:
			if parentNode.left == 0 {
				nodeIndex := tree.allocator.malloc()
				storage = tree.storage()
				newNode := &storage[nodeIndex]
				newNode.item = item
				newNode.parent = parent
				storage[parent].left = nodeIndex
				tree.count++
				tree.maybeSetMinNode(nodeIndex)

				return nodeIndex
			}

			parent = parentNode.left
		default:
			if parentNode.right == 0 {
				nodeIndex := tree.allocator.malloc()
				storage = tree.storage()
				newNode := &storage[nodeIndex]
				newNode.item = item
				newNode.parent = parent
				storage[parent].right = nodeIndex
				tree.count++
				tree.maybeSetMaxNode(nodeIndex)

				return nodeIndex
			}

			parent = parentNode.right
		}
	}
}

// Find a node whose item >= Key. The 2nd return Value is true iff the
// node.item==Key. Returns (nil, false) if all nodes in the tree are <
// Key.
func (tree *RBTree) findGE(key uint32) (uint32, bool) {
	alloc := tree.storage()

	nodeIndex := tree.root
	candidate := uint32(0)

	for nodeIndex != 0 {
		nodeKey := alloc[nodeIndex].item.Key
		switch {
		case key == nodeKey:
			return nodeIndex, true
		case key < nodeKey:
			candidate = nodeIndex
			nodeIndex = alloc[nodeIndex].left
		default:
			nodeIndex = alloc[nodeIndex].right
		}
	}

	return candidate, false
}

// Delete N from the tree.
func (tree *RBTree) doDelete(nodeIndex uint32) {
	alloc := tree.storage()
	if alloc[nodeIndex].left != 0 && alloc[nodeIndex].right != 0 {
		pred := maxPredecessor(nodeIndex, alloc)
		tree.swapNodes(nodeIndex, pred)
	}

	doAssert(alloc[nodeIndex].left == 0 || alloc[nodeIndex].right == 0)

	child := alloc[nodeIndex].right
	if child == 0 {
		child = alloc[nodeIndex].left
	}

	if alloc[nodeIndex].color == black {
		alloc[nodeIndex].color = getColor(child, alloc)
		tree.deleteCase1(nodeIndex)
	}

	tree.replaceNode(nodeIndex, child)

	if alloc[nodeIndex].parent == 0 && child != 0 {
		alloc[child].color = black
	}

	tree.allocator.free(nodeIndex)

	tree.count--
	tree.updateBoundsAfterDelete(nodeIndex)
}

func (tree *RBTree) updateBoundsAfterDelete(nodeIndex uint32) {
	if tree.count == 0 {
		tree.minNode = 0
		tree.maxNode = 0

		return
	}

	if tree.minNode == nodeIndex {
		tree.recomputeMinNode()
	}

	if tree.maxNode == nodeIndex {
		tree.recomputeMaxNode()
	}
}

// Move n to the pred's place, and vice versa.
func (tree *RBTree) swapNodes(nodeIndex, predecessor uint32) {
	doAssert(predecessor != nodeIndex)

	alloc := tree.storage()
	isLeft := isLeftChild(predecessor, alloc)
	tmp := alloc[predecessor]
	tree.replaceNode(nodeIndex, predecessor)
	alloc[predecessor].color = alloc[nodeIndex].color

	if tmp.parent == nodeIndex {
		swapAdjacentNodes(nodeIndex, predecessor, isLeft, tmp, alloc)
	} else {
		swapSeparatedNodes(nodeIndex, predecessor, isLeft, tmp, alloc)
	}

	alloc[nodeIndex].color = tmp.color
}

func swapAdjacentNodes(nodeIndex, pred uint32, isLeft bool, previous node, alloc []node) {
	if isLeft {
		alloc[pred].left = nodeIndex
		alloc[pred].right = alloc[nodeIndex].right
		setParent(alloc[pred].right, pred, alloc)
	} else {
		alloc[pred].left = alloc[nodeIndex].left
		setParent(alloc[pred].left, pred, alloc)
		alloc[pred].right = nodeIndex
	}

	alloc[nodeIndex].item, alloc[nodeIndex].parent = previous.item, pred
	alloc[nodeIndex].left, alloc[nodeIndex].right = previous.left, previous.right
	setParent(alloc[nodeIndex].left, nodeIndex, alloc)
	setParent(alloc[nodeIndex].right, nodeIndex, alloc)
}

func swapSeparatedNodes(nodeIndex, pred uint32, isLeft bool, previous node, alloc []node) {
	alloc[pred].left, alloc[pred].right = alloc[nodeIndex].left, alloc[nodeIndex].right
	setParent(alloc[pred].left, pred, alloc)
	setParent(alloc[pred].right, pred, alloc)

	if isLeft {
		alloc[previous.parent].left = nodeIndex
	} else {
		alloc[previous.parent].right = nodeIndex
	}

	alloc[nodeIndex].item, alloc[nodeIndex].parent = previous.item, previous.parent
	alloc[nodeIndex].left, alloc[nodeIndex].right = previous.left, previous.right
	setParent(alloc[nodeIndex].left, nodeIndex, alloc)
	setParent(alloc[nodeIndex].right, nodeIndex, alloc)
}

func setParent(child, parent uint32, alloc []node) {
	if child != 0 {
		alloc[child].parent = parent
	}
}

func (tree *RBTree) deleteCase1(nodeIndex uint32) {
	alloc := tree.storage()
	for alloc[nodeIndex].parent != 0 {
		tree.rotateRedSibling(nodeIndex, alloc)

		parent := alloc[nodeIndex].parent
		siblingIndex := sibling(nodeIndex, alloc)

		if getColor(parent, alloc) == black && siblingHasBlackFamily(siblingIndex, alloc) {
			alloc[siblingIndex].color = red
			nodeIndex = parent

			continue
		}

		if getColor(parent, alloc) == red && siblingHasBlackFamily(siblingIndex, alloc) {
			alloc[siblingIndex].color = red
			alloc[parent].color = black

			return
		}

		tree.deleteCase5(nodeIndex)

		return
	}
}

func (tree *RBTree) rotateRedSibling(nodeIndex uint32, alloc []node) {
	siblingIndex := sibling(nodeIndex, alloc)
	if getColor(siblingIndex, alloc) != red {
		return
	}

	parent := alloc[nodeIndex].parent
	alloc[parent].color = red
	alloc[siblingIndex].color = black

	if nodeIndex == alloc[parent].left {
		tree.rotateLeft(parent)
	} else {
		tree.rotateRight(parent)
	}
}

func siblingHasBlackFamily(siblingIndex uint32, alloc []node) bool {
	return getColor(siblingIndex, alloc) == black &&
		getColor(alloc[siblingIndex].left, alloc) == black &&
		getColor(alloc[siblingIndex].right, alloc) == black
}

func (tree *RBTree) deleteCase5(nodeIndex uint32) {
	alloc := tree.storage()
	if nodeIndex == alloc[alloc[nodeIndex].parent].left &&
		getColor(sibling(nodeIndex, alloc), alloc) == black &&
		getColor(alloc[sibling(nodeIndex, alloc)].left, alloc) == red &&
		getColor(alloc[sibling(nodeIndex, alloc)].right, alloc) == black {
		alloc[sibling(nodeIndex, alloc)].color = red
		alloc[alloc[sibling(nodeIndex, alloc)].left].color = black
		tree.rotateRight(sibling(nodeIndex, alloc))
	} else if nodeIndex == alloc[alloc[nodeIndex].parent].right &&
		getColor(sibling(nodeIndex, alloc), alloc) == black &&
		getColor(alloc[sibling(nodeIndex, alloc)].right, alloc) == red &&
		getColor(alloc[sibling(nodeIndex, alloc)].left, alloc) == black {
		alloc[sibling(nodeIndex, alloc)].color = red
		alloc[alloc[sibling(nodeIndex, alloc)].right].color = black
		tree.rotateLeft(sibling(nodeIndex, alloc))
	}

	// case 6
	alloc[sibling(nodeIndex, alloc)].color = getColor(alloc[nodeIndex].parent, alloc)

	alloc[alloc[nodeIndex].parent].color = black
	if nodeIndex == alloc[alloc[nodeIndex].parent].left {
		doAssert(getColor(alloc[sibling(nodeIndex, alloc)].right, alloc) == red)
		alloc[alloc[sibling(nodeIndex, alloc)].right].color = black
		tree.rotateLeft(alloc[nodeIndex].parent)
	} else {
		doAssert(getColor(alloc[sibling(nodeIndex, alloc)].left, alloc) == red)
		alloc[alloc[sibling(nodeIndex, alloc)].left].color = black
		tree.rotateRight(alloc[nodeIndex].parent)
	}
}

func (tree *RBTree) replaceNode(oldn, newn uint32) {
	alloc := tree.storage()
	if alloc[oldn].parent == 0 {
		tree.root = newn
	} else {
		if oldn == alloc[alloc[oldn].parent].left {
			alloc[alloc[oldn].parent].left = newn
		} else {
			alloc[alloc[oldn].parent].right = newn
		}
	}

	if newn != 0 {
		alloc[newn].parent = alloc[oldn].parent
	}
}

/*
	X		     Y

A   Y	    =>     X   C

	B C 	  A B
*/
func (tree *RBTree) rotateLeft(pivot uint32) {
	alloc := tree.storage()
	replacement := alloc[pivot].right

	alloc[pivot].right = alloc[replacement].left
	if alloc[replacement].left != 0 {
		alloc[alloc[replacement].left].parent = pivot
	}

	alloc[replacement].parent = alloc[pivot].parent
	if alloc[pivot].parent == 0 {
		tree.root = replacement
	} else {
		if isLeftChild(pivot, alloc) {
			alloc[alloc[pivot].parent].left = replacement
		} else {
			alloc[alloc[pivot].parent].right = replacement
		}
	}

	alloc[replacement].left = pivot
	alloc[pivot].parent = replacement
}

/*
	  Y           X
	C  =>   A   Y

A B             C.
*/
func (tree *RBTree) rotateRight(pivot uint32) {
	alloc := tree.storage()
	replacement := alloc[pivot].left

	// Move "B"
	alloc[pivot].left = alloc[replacement].right
	if alloc[replacement].right != 0 {
		alloc[alloc[replacement].right].parent = pivot
	}

	alloc[replacement].parent = alloc[pivot].parent
	if alloc[pivot].parent == 0 {
		tree.root = replacement
	} else {
		if isLeftChild(pivot, alloc) {
			alloc[alloc[pivot].parent].left = replacement
		} else {
			alloc[alloc[pivot].parent].right = replacement
		}
	}

	alloc[replacement].right = pivot
	alloc[pivot].parent = replacement
}
