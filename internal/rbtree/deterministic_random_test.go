package rbtree

import "math"

// deterministicRandom is a small SplitMix64 generator for repeatable test and
// benchmark data. It is deliberately test-only and is not used for security.
type deterministicRandom struct {
	state uint64
}

func newDeterministicRandom(seed uint64) *deterministicRandom {
	return &deterministicRandom{state: seed}
}

func (random *deterministicRandom) Uint32() uint32 {
	random.state += 0x9e3779b97f4a7c15
	value := random.state
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb

	return uint32((value ^ (value >> 31)) >> 32)
}

func (random *deterministicRandom) Int31n(limit int) int {
	if limit <= 0 {
		panic("deterministic random limit must be positive")
	}

	return int(random.Uint32()&math.MaxInt32) % limit
}

func checkedTestUint32(value int) uint32 {
	if value < 0 || uint64(value) > math.MaxUint32 {
		panic("test value exceeds uint32 range")
	}

	return uint32(value)
}
