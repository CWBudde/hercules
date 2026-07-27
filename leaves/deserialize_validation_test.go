package leaves

import (
	"errors"
	"math"
	"testing"

	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/analysisio"
	"github.com/cwbudde/hercules/internal/pb"
)

func TestLeafDeserializersRejectMalformedStructures(t *testing.T) {
	t.Run("burndown dimensions", func(t *testing.T) {
		data, err := proto.Marshal(&pb.BurndownAnalysisResults{
			Project: &pb.BurndownSparseMatrix{
				NumberOfRows:    math.MaxInt32,
				NumberOfColumns: math.MaxInt32,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&BurndownAnalysis{}).Deserialize(data)
		if !errors.Is(err, analysisio.ErrAnalysisTooLarge) {
			t.Fatalf("error = %v, want ErrAnalysisTooLarge", err)
		}
	})

	t.Run("couples CSR", func(t *testing.T) {
		data, err := proto.Marshal(&pb.CouplesAnalysisResults{
			FileCouples: &pb.Couples{
				Index: []string{"source.go"},
				Matrix: &pb.CompressedSparseRowMatrix{
					NumberOfRows:    1,
					NumberOfColumns: 1,
					Data:            []int64{1},
					Indices:         []int32{1},
					Indptr:          []int64{0, 1},
				},
			},
			FilesLines: []int32{1},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&CouplesAnalysis{}).Deserialize(data)
		if !errors.Is(err, analysisio.ErrAnalysisMalformed) {
			t.Fatalf("error = %v, want ErrAnalysisMalformed", err)
		}
	})

	t.Run("refactoring parallel arrays", func(t *testing.T) {
		data, err := proto.Marshal(&pb.RefactoringProxyResults{
			Ticks:        []int32{1},
			RenameRatios: []float32{},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&RefactoringProxy{}).Deserialize(data)
		if !errors.Is(err, analysisio.ErrAnalysisMalformed) {
			t.Fatalf("error = %v, want ErrAnalysisMalformed", err)
		}
	})
}

func FuzzLeafDeserializers(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0xff})
	f.Add([]byte{0x08, 0xff, 0xff, 0xff, 0xff, 0x0f})
	f.Add([]byte{0x42, 0x00}) // people_files without people_couples metadata

	f.Fuzz(func(t *testing.T, data []byte) {
		deserializers := []interface {
			Deserialize(data []byte) (any, error)
		}{
			&BurndownAnalysis{},
			&LegacyBurndownAnalysis{},
			&CouplesAnalysis{},
			&DevsAnalysis{},
			&ImportsPerDeveloper{},
			&TemporalActivityAnalysis{},
			&BusFactorAnalysis{},
			&OwnershipConcentrationAnalysis{},
			&KnowledgeDiffusionAnalysis{},
			&HotspotRiskAnalysis{},
			&RefactoringProxy{},
			&CodeChurnAnalysis{},
			&OnboardingAnalysis{},
		}
		for _, deserializer := range deserializers {
			_, _ = deserializer.Deserialize(data)
		}
	})
}
