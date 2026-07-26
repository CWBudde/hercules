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

//nolint:gochecknoglobals // A package-level sink prevents benchmark work from being optimized away.
var benchmarkOwnershipSnapshot *ownershipTotals

// BenchmarkOwnershipSnapshotsStableLargeFiles compares the incremental snapshot path with the
// former full-rescan shape. Setup creates many large, stable files outside the timed section;
// each timed incremental tick changes only two ownership runs and copies the author-sized output.
func BenchmarkOwnershipSnapshotsStableLargeFiles(b *testing.B) {
	for _, fileCount := range []int{1_000, 50_000} {
		name := fmt.Sprintf("files=%d", fileCount)

		b.Run("incremental/"+name, func(b *testing.B) {
			accumulator := benchmarkSeedOwnership(b, fileCount)
			tick := 1

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				previousAuthor := core.AuthorId(0)
				currentAuthor := core.AuthorId(1)
				if tick%2 == 0 {
					previousAuthor, currentAuthor = currentAuthor, previousAuthor
				}

				_, snapshot, err := accumulator.consume(tick, core.LineHistoryChanges{
					Changes: []core.LineHistoryChange{
						ownershipChange(1, -1, previousAuthor, currentAuthor, core.TickNumber(tick)),
						ownershipChange(1, 1, currentAuthor, currentAuthor, core.TickNumber(tick)),
					},
				})
				if err != nil {
					b.Fatal(err)
				}

				benchmarkOwnershipSnapshot = snapshot
				tick++
			}

			b.ReportMetric(2, "changed-ownership-runs/op")
			b.ReportMetric(benchmarkOwnershipAuthors, "snapshot-author-entries/op")
			b.ReportMetric(float64(fileCount), "stable-live-files")
			b.ReportMetric(
				float64(fileCount)*benchmarkOwnershipLinesPerFile,
				"stable-live-lines",
			)
		})

		b.Run("full-rescan-reference/"+name, func(b *testing.B) {
			accumulator := benchmarkSeedOwnership(b, fileCount)

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				totals := map[int]int64{}
				var totalLines int64

				for _, fileAuthors := range accumulator.fileLines {
					for author, lines := range fileAuthors {
						totals[author] += lines
						totalLines += lines
					}
				}

				benchmarkOwnershipSnapshot = &ownershipTotals{
					TotalLines:  totalLines,
					AuthorLines: totals,
				}
			}

			b.ReportMetric(float64(fileCount), "rescanned-ownership-runs/op")
			b.ReportMetric(benchmarkOwnershipAuthors, "snapshot-author-entries/op")
			b.ReportMetric(float64(fileCount), "stable-live-files")
			b.ReportMetric(
				float64(fileCount)*benchmarkOwnershipLinesPerFile,
				"stable-live-lines",
			)
		})
	}
}

func benchmarkSeedOwnership(b *testing.B, fileCount int) ownershipSnapshotAccumulator {
	b.Helper()

	changes := make([]core.LineHistoryChange, fileCount)
	for file := range fileCount {
		author := core.AuthorId(file % benchmarkOwnershipAuthors)
		changes[file] = ownershipChange(
			core.FileId(file+1),
			benchmarkOwnershipLinesPerFile,
			author,
			author,
			0,
		)
	}

	accumulator := ownershipSnapshotAccumulator{}
	accumulator.reset()

	_, _, err := accumulator.consume(0, core.LineHistoryChanges{Changes: changes})
	if err != nil {
		b.Fatal(err)
	}

	return accumulator
}
