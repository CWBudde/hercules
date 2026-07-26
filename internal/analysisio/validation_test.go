package analysisio

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/pb"
)

func TestReadAllEnforcesInputLimit(t *testing.T) {
	limits := Limits{MaxInputBytes: 4}
	data, err := ReadAll(bytes.NewReader([]byte("1234")), limits)
	if err != nil {
		t.Fatalf("exact-limit input failed: %v", err)
	}
	if string(data) != "1234" {
		t.Fatalf("data = %q, want 1234", data)
	}

	_, err = ReadAll(bytes.NewReader([]byte("12345")), limits)
	if !errors.Is(err, ErrAnalysisTooLarge) {
		t.Fatalf("oversized error = %v, want ErrAnalysisTooLarge", err)
	}
}

func TestValidateDimensionsRejectsNegativeOverflowAndLimits(t *testing.T) {
	limits := Limits{
		MaxDecodedCells: 100,
		MaxRows:         10,
		MaxColumns:      10,
	}
	tests := []struct {
		name          string
		rows, columns int64
	}{
		{"negative rows", -1, 1},
		{"negative columns", 1, -1},
		{"too many rows", 11, 1},
		{"too many columns", 1, 11},
		{"too many cells", 10, 11},
		{"overflow", math.MaxInt64, 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDimensions("matrix", test.rows, test.columns, limits)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateDenseMatrixRejectsRaggedRows(t *testing.T) {
	err := ValidateDenseMatrix("dense", [][]int{{1, 2}, {3}}, DefaultLimits())
	if !errors.Is(err, ErrAnalysisMalformed) {
		t.Fatalf("error = %v, want ErrAnalysisMalformed", err)
	}
}

func TestValidateCSRRejectsInconsistentStructures(t *testing.T) {
	valid := func() *pb.CompressedSparseRowMatrix {
		return &pb.CompressedSparseRowMatrix{
			NumberOfRows:    2,
			NumberOfColumns: 2,
			Data:            []int64{1, 2},
			Indices:         []int32{0, 1},
			Indptr:          []int64{0, 1, 2},
		}
	}
	mutations := map[string]func(*pb.CompressedSparseRowMatrix){
		"negative dimension": func(matrix *pb.CompressedSparseRowMatrix) {
			matrix.NumberOfRows = -1
		},
		"truncated pointers": func(matrix *pb.CompressedSparseRowMatrix) {
			matrix.Indptr = matrix.GetIndptr()[:2]
		},
		"nonzero first pointer": func(matrix *pb.CompressedSparseRowMatrix) {
			matrix.Indptr[0] = 1
		},
		"nonmonotonic pointers": func(matrix *pb.CompressedSparseRowMatrix) {
			matrix.Indptr = []int64{0, 2, 1}
		},
		"wrong terminal pointer": func(matrix *pb.CompressedSparseRowMatrix) {
			matrix.Indptr[2] = 1
		},
		"short indices": func(matrix *pb.CompressedSparseRowMatrix) {
			matrix.Indices = matrix.GetIndices()[:1]
		},
		"negative column": func(matrix *pb.CompressedSparseRowMatrix) {
			matrix.Indices[0] = -1
		},
		"column out of bounds": func(matrix *pb.CompressedSparseRowMatrix) {
			matrix.Indices[0] = 2
		},
	}
	err := ValidateCSR("valid", valid(), DefaultLimits())
	if err != nil {
		t.Fatalf("valid CSR failed: %v", err)
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			matrix := valid()
			mutate(matrix)
			err := ValidateCSR(name, matrix, DefaultLimits())
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateCSRLargeLogicalMatrixUsesNonzeroLimit(t *testing.T) {
	matrix := &pb.CompressedSparseRowMatrix{
		NumberOfRows:    10_000,
		NumberOfColumns: 10_000,
		Data:            []int64{7},
		Indices:         []int32{9_999},
		Indptr:          make([]int64, 10_001),
	}
	pointers := matrix.GetIndptr()
	for index := 1; index < len(pointers); index++ {
		pointers[index] = 1
	}
	limits := Limits{
		MaxDecodedCells: 1,
		MaxRows:         10_000,
		MaxColumns:      10_000,
	}
	err := ValidateCSR("large sparse", matrix, limits)
	if err != nil {
		t.Fatalf("large sparse matrix failed: %v", err)
	}

	matrix.Data = append(matrix.Data, 8)
	matrix.Indices = append(matrix.Indices, 9_998)
	pointers[len(pointers)-1] = 2
	err = ValidateCSR("too many nonzeros", matrix, limits)
	if !errors.Is(err, ErrAnalysisTooLarge) {
		t.Fatalf("error = %v, want ErrAnalysisTooLarge", err)
	}
}

func TestValidateBurndownRejectsHugeDeclaredMatrixBeforeAllocation(t *testing.T) {
	message := &pb.BurndownAnalysisResults{
		Project: &pb.BurndownSparseMatrix{
			NumberOfRows:    math.MaxInt32,
			NumberOfColumns: math.MaxInt32,
		},
	}
	encoded, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.BurndownAnalysisResults
	err = Unmarshal(encoded, &decoded, DefaultLimits())
	if !errors.Is(err, ErrAnalysisTooLarge) {
		t.Fatalf("error = %v, want ErrAnalysisTooLarge", err)
	}
}

func TestValidateParallelPayloadArrays(t *testing.T) {
	refactoring := &pb.RefactoringProxyResults{
		Ticks:         []int32{1},
		RenameRatios:  []float32{0.5},
		IsRefactoring: []bool{true},
	}
	err := ValidateRefactoringResults(refactoring, DefaultLimits())
	if !errors.Is(err, ErrAnalysisMalformed) {
		t.Fatalf("refactoring error = %v, want ErrAnalysisMalformed", err)
	}

	burndown := &pb.BurndownAnalysisResults{
		Files: []*pb.BurndownSparseMatrix{{
			NumberOfRows: 0,
		}},
	}
	err = ValidateBurndownResults(burndown, DefaultLimits())
	if !errors.Is(err, ErrAnalysisMalformed) {
		t.Fatalf("burndown error = %v, want ErrAnalysisMalformed", err)
	}
}

func TestUnmarshalEnforcesAggregateNestedRecordLimit(t *testing.T) {
	encoded, err := proto.Marshal(&pb.HotspotRiskResults{
		Files: []*pb.FileRisk{{Path: "first"}, {Path: "second"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var decoded pb.HotspotRiskResults
	err = Unmarshal(encoded, &decoded, Limits{MaxNestedRecords: 1})
	if !errors.Is(err, ErrAnalysisTooLarge) {
		t.Fatalf("error = %v, want ErrAnalysisTooLarge", err)
	}
}

func FuzzUnmarshalBurndown(f *testing.F) {
	seeds := [][]byte{{}, {0xff}, {0x08, 0xff, 0xff, 0xff, 0xff, 0x0f}}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var message pb.BurndownAnalysisResults
		_ = Unmarshal(data, &message, Limits{MaxInputBytes: 1 << 20})
	})
}

func FuzzUnmarshalCSR(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		var matrix pb.CompressedSparseRowMatrix

		err := Unmarshal(data, &matrix, Limits{MaxInputBytes: 1 << 20})
		if err == nil {
			_ = ValidateCSR("fuzz CSR", &matrix, Limits{
				MaxInputBytes:   1 << 20,
				MaxDecodedCells: 1 << 20,
				MaxRows:         1 << 10,
				MaxColumns:      1 << 10,
			})
		}
	})
}
