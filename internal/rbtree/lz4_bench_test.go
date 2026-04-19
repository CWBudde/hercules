package rbtree

import (
	"math/rand"
	"testing"
)

// makeBenchData returns a uint32 slice with the requested distribution:
//
//	"uniform"  – same value repeated (very compressible)
//	"random"   – pseudo-random values (hard to compress)
//	"realistic" – blocks of equal values with occasional changes (mimics line-age data)
func makeBenchData(n int, kind string) []uint32 {
	data := make([]uint32, n)
	switch kind {
	case "uniform":
		for i := range data {
			data[i] = 7
		}
	case "random":
		r := rand.New(rand.NewSource(42))
		for i := range data {
			data[i] = r.Uint32()
		}
	case "realistic":
		r := rand.New(rand.NewSource(99))
		val := r.Uint32()
		for i := range data {
			if r.Intn(20) == 0 {
				val = r.Uint32()
			}
			data[i] = val
		}
	}
	return data
}

func benchmarkCompress(b *testing.B, n int, kind string) {
	b.Helper()
	data := makeBenchData(n, kind)
	b.ResetTimer()
	b.ReportAllocs()
	var sink []byte
	for i := 0; i < b.N; i++ {
		sink = CompressUInt32Slice(data)
	}
	_ = sink
}

func benchmarkRoundtrip(b *testing.B, n int, kind string) {
	b.Helper()
	data := makeBenchData(n, kind)
	compressed := CompressUInt32Slice(data)
	result := make([]uint32, n)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		DecompressUInt32Slice(compressed, result)
	}
	_ = result
}

// ---- Compress benchmarks ----

func BenchmarkCompressUInt32Slice_1k_uniform(b *testing.B) {
	benchmarkCompress(b, 1_000, "uniform")
}
func BenchmarkCompressUInt32Slice_1k_random(b *testing.B) {
	benchmarkCompress(b, 1_000, "random")
}
func BenchmarkCompressUInt32Slice_1k_realistic(b *testing.B) {
	benchmarkCompress(b, 1_000, "realistic")
}

func BenchmarkCompressUInt32Slice_10k_uniform(b *testing.B) {
	benchmarkCompress(b, 10_000, "uniform")
}
func BenchmarkCompressUInt32Slice_10k_random(b *testing.B) {
	benchmarkCompress(b, 10_000, "random")
}
func BenchmarkCompressUInt32Slice_10k_realistic(b *testing.B) {
	benchmarkCompress(b, 10_000, "realistic")
}

func BenchmarkCompressUInt32Slice_100k_uniform(b *testing.B) {
	benchmarkCompress(b, 100_000, "uniform")
}
func BenchmarkCompressUInt32Slice_100k_random(b *testing.B) {
	benchmarkCompress(b, 100_000, "random")
}
func BenchmarkCompressUInt32Slice_100k_realistic(b *testing.B) {
	benchmarkCompress(b, 100_000, "realistic")
}

// ---- Decompress benchmarks ----

func BenchmarkDecompressUInt32Slice_1k_uniform(b *testing.B) {
	benchmarkRoundtrip(b, 1_000, "uniform")
}
func BenchmarkDecompressUInt32Slice_1k_random(b *testing.B) {
	benchmarkRoundtrip(b, 1_000, "random")
}
func BenchmarkDecompressUInt32Slice_1k_realistic(b *testing.B) {
	benchmarkRoundtrip(b, 1_000, "realistic")
}

func BenchmarkDecompressUInt32Slice_10k_uniform(b *testing.B) {
	benchmarkRoundtrip(b, 10_000, "uniform")
}
func BenchmarkDecompressUInt32Slice_10k_random(b *testing.B) {
	benchmarkRoundtrip(b, 10_000, "random")
}
func BenchmarkDecompressUInt32Slice_10k_realistic(b *testing.B) {
	benchmarkRoundtrip(b, 10_000, "realistic")
}

func BenchmarkDecompressUInt32Slice_100k_uniform(b *testing.B) {
	benchmarkRoundtrip(b, 100_000, "uniform")
}
func BenchmarkDecompressUInt32Slice_100k_random(b *testing.B) {
	benchmarkRoundtrip(b, 100_000, "random")
}
func BenchmarkDecompressUInt32Slice_100k_realistic(b *testing.B) {
	benchmarkRoundtrip(b, 100_000, "realistic")
}
