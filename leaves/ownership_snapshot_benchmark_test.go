package leaves

import (
	"fmt"
	"testing"

	"github.com/cwbudde/hercules/internal/core"
)

const (
	benchmarkOwnershipAuthors      = 32
	benchmarkOwnershipLinesPerFile = 1_000_000
)

// A package-level sink prevents benchmark work from being optimized away.
var benchmarkOwnershipSnapshot *ownershipTotals

// BenchmarkOwnershipSnapshotsStableLargeFiles compares the per-commit path, which rescans only
// the files a commit touched, with the full rebuild a merge performs. Setup creates many large,
// stable files outside the timed section; each timed commit moves one ownership boundary in one
// file and copies the author-sized snapshot.
func BenchmarkOwnershipSnapshotsStableLargeFiles(b *testing.B) {
	for _, fileCount := range []int{1_000, 50_000} {
		name := fmt.Sprintf("files=%d", fileCount)

		b.Run("touched-files/"+name, func(b *testing.B) {
			resolver, accumulator := benchmarkSeedOwnership(b, fileCount)
			tick := 1

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				boundary := 1 + tick%(benchmarkOwnershipLinesPerFile-1)
				resolver[1] = ownershipFile(
					"file-1", benchmarkOwnershipLinesPerFile,
					ownershipTestRun{0, 0, 0},
					ownershipTestRun{boundary, 1, core.TickNumber(tick)},
				)

				changes := ownershipChanges(1)
				changes.Resolver = resolver

				err := accumulator.consume(tick, changes)
				if err != nil {
					b.Fatal(err)
				}

				benchmarkOwnershipSnapshot = accumulator.snapshot()
				tick++
			}

			b.ReportMetric(1, "rescanned-files/op")
			b.ReportMetric(benchmarkOwnershipAuthors, "snapshot-author-entries/op")
			b.ReportMetric(float64(fileCount), "stable-live-files")
			b.ReportMetric(
				float64(fileCount)*benchmarkOwnershipLinesPerFile,
				"stable-live-lines",
			)
		})

		b.Run("full-rebuild-reference/"+name, func(b *testing.B) {
			_, accumulator := benchmarkSeedOwnership(b, fileCount)

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				err := accumulator.rebuild()
				if err != nil {
					b.Fatal(err)
				}

				benchmarkOwnershipSnapshot = accumulator.snapshot()
			}

			b.ReportMetric(float64(fileCount), "rescanned-files/op")
			b.ReportMetric(benchmarkOwnershipAuthors, "snapshot-author-entries/op")
			b.ReportMetric(float64(fileCount), "stable-live-files")
			b.ReportMetric(
				float64(fileCount)*benchmarkOwnershipLinesPerFile,
				"stable-live-lines",
			)
		})
	}
}

func benchmarkSeedOwnership(
	b *testing.B, fileCount int,
) (ownershipTestResolver, ownershipSnapshotAccumulator) {
	b.Helper()

	resolver := make(ownershipTestResolver, fileCount)
	touched := make([]core.FileId, fileCount)

	for file := range fileCount {
		id := core.FileId(file + 1)
		author := core.AuthorId(file % benchmarkOwnershipAuthors)
		resolver[id] = ownershipFile(
			fmt.Sprintf("file-%d", id), benchmarkOwnershipLinesPerFile, ownershipTestRun{0, author, 0},
		)
		touched[file] = id
	}

	accumulator := ownershipSnapshotAccumulator{}
	accumulator.reset()

	changes := ownershipChanges(touched...)
	changes.Resolver = resolver

	err := accumulator.consume(0, changes)
	if err != nil {
		b.Fatal(err)
	}

	return resolver, accumulator
}
