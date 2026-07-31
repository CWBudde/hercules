package modes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlotTopCouplingPairsWritesPNG(t *testing.T) {
	output := t.TempDir()
	err := plotTopCouplingPairs(sampleFileCouplingAnalysis(), output)
	if err != nil {
		t.Fatalf("plotTopCouplingPairs() failed: %v", err)
	}

	outputFile := filepath.Join(output, "top_file_coupling_pairs.png")
	_, err = os.Stat(outputFile)
	if err != nil {
		t.Fatalf("expected plot file %q: %v", outputFile, err)
	}
}

func sampleFileCouplingAnalysis() FileCouplingAnalysis {
	pairs := []FileCouplingPair{
		{File1: "pipeline.go", File2: "pipeline_test.go", CouplingScore: 18, CooccuranceCount: 18},
		{File1: "burndown.go", File2: "burndown_test.go", CouplingScore: 15, CooccuranceCount: 15},
		{File1: "identity.go", File2: "identity_test.go", CouplingScore: 14, CooccuranceCount: 14},
		{File1: "burndown.go", File2: "pipeline.go", CouplingScore: 12, CooccuranceCount: 12},
		{File1: "file.go", File2: "file_test.go", CouplingScore: 12, CooccuranceCount: 12},
		{File1: "README.md", File2: "labours.py", CouplingScore: 10, CooccuranceCount: 10},
		{File1: "burndown.go", File2: "couples.go", CouplingScore: 10, CooccuranceCount: 10},
		{File1: "blob_cache.go", File2: "burndown.go", CouplingScore: 9, CooccuranceCount: 9},
		{File1: "burndown.go", File2: "diff.go", CouplingScore: 9, CooccuranceCount: 9},
		{File1: "burndown.go", File2: "identity.go", CouplingScore: 9, CooccuranceCount: 9},
		{File1: "burndown.go", File2: "renames.go", CouplingScore: 9, CooccuranceCount: 9},
		{File1: "burndown.go", File2: "uast.go", CouplingScore: 9, CooccuranceCount: 9},
		{File1: "couples.go", File2: "identity.go", CouplingScore: 9, CooccuranceCount: 9},
		{File1: "couples.go", File2: "pipeline.go", CouplingScore: 9, CooccuranceCount: 9},
		{File1: "identity.go", File2: "renames.go", CouplingScore: 9, CooccuranceCount: 9},
	}
	return FileCouplingAnalysis{
		TopCoupling: pairs,
		Statistics: CouplingStatistics{
			TotalFiles:      18,
			TotalCoupling:   172,
			AverageCoupling: 11.47,
			MaxCoupling:     18,
			MinCoupling:     9,
		},
	}
}
