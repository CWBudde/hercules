package leaves

import (
	"slices"
	"testing"

	"github.com/cwbudde/hercules/internal/core"
)

// TestQualifyPathsPrefixesEveryPathKey covers the analyses whose results are keyed by a
// repository-local path. Combine applies this before merging so that equal paths in different
// repositories do not fuse into one key.
func TestQualifyPathsPrefixesEveryPathKey(t *testing.T) {
	for _, test := range []struct {
		name  string
		item  core.RepositoryQualifiablePipelineItem
		input any
		check func(t *testing.T, qualified any)
	}{
		{
			name: "Couples",
			item: &CouplesAnalysis{},
			input: CouplesResult{
				Files:      []string{testReadmePath, "main.go"},
				FilesLines: []int{1, 2},
			},
			check: func(t *testing.T, qualified any) {
				t.Helper()
				result, ok := qualified.(CouplesResult)
				if !ok {
					t.Fatalf("QualifyPaths returned %T", qualified)
				}
				if want := []string{"alpha:" + testReadmePath, "alpha:main.go"}; !slices.Equal(result.Files, want) {
					t.Fatalf("Files = %v, want %v", result.Files, want)
				}
				if want := []int{1, 2}; !slices.Equal(result.FilesLines, want) {
					t.Fatalf("FilesLines = %v, want %v", result.FilesLines, want)
				}
			},
		},
		{
			name: "HotspotRisk",
			item: &HotspotRiskAnalysis{},
			input: HotspotRiskResult{
				Files:      []FileRisk{{Path: testReadmePath, RiskScore: 0.5}},
				WindowDays: 90,
			},
			check: func(t *testing.T, qualified any) {
				t.Helper()
				result, ok := qualified.(HotspotRiskResult)
				if !ok {
					t.Fatalf("QualifyPaths returned %T", qualified)
				}
				if result.Files[0].Path != "alpha:"+testReadmePath {
					t.Fatalf("Path = %q", result.Files[0].Path)
				}
				if result.Files[0].RiskScore != 0.5 || result.WindowDays != 90 {
					t.Fatalf("QualifyPaths altered more than the path: %+v", result)
				}
			},
		},
		{
			name: "CodeChurn",
			item: &CodeChurnAnalysis{},
			input: CodeChurnResult{
				Authors: []CodeChurnAuthorResult{
					{Files: map[string]CodeChurnFileResult{testReadmePath: {InsertedLines: 7}}},
					{Files: nil},
				},
			},
			check: func(t *testing.T, qualified any) {
				t.Helper()
				result, ok := qualified.(CodeChurnResult)
				if !ok {
					t.Fatalf("QualifyPaths returned %T", qualified)
				}
				if file, exists := result.Authors[0].Files["alpha:"+testReadmePath]; !exists ||
					file.InsertedLines != 7 {
					t.Fatalf("Files = %v", result.Authors[0].Files)
				}
				if result.Authors[1].Files != nil {
					t.Fatalf("an author without files gained a map: %v", result.Authors[1].Files)
				}
			},
		},
		{
			name: "KnowledgeDiffusion",
			item: &KnowledgeDiffusionAnalysis{},
			input: KnowledgeDiffusionResult{
				Files:        map[string]*KnowledgeDiffusionFileResult{testReadmePath: {RecentEditorsCount: 3}},
				Distribution: map[int]int{1: 1},
			},
			check: func(t *testing.T, qualified any) {
				t.Helper()
				result, ok := qualified.(KnowledgeDiffusionResult)
				if !ok {
					t.Fatalf("QualifyPaths returned %T", qualified)
				}
				if file, exists := result.Files["alpha:"+testReadmePath]; !exists ||
					file.RecentEditorsCount != 3 {
					t.Fatalf("Files = %v", result.Files)
				}
				if result.Distribution[1] != 1 {
					t.Fatalf("Distribution = %v", result.Distribution)
				}
			},
		},
		{
			name:  "BusFactor",
			item:  &BusFactorAnalysis{},
			input: BusFactorResult{SubsystemBusFactor: map[string]int{"cmd": 2}},
			check: func(t *testing.T, qualified any) {
				t.Helper()
				result, ok := qualified.(BusFactorResult)
				if !ok {
					t.Fatalf("QualifyPaths returned %T", qualified)
				}
				if result.SubsystemBusFactor["alpha:cmd"] != 2 {
					t.Fatalf("SubsystemBusFactor = %v", result.SubsystemBusFactor)
				}
			},
		},
		{
			name: "OwnershipConcentration",
			item: &OwnershipConcentrationAnalysis{},
			input: OwnershipConcentrationResult{
				SubsystemConcentration: map[string]*SubsystemConcentration{"cmd": {Gini: 0.25}},
			},
			check: func(t *testing.T, qualified any) {
				t.Helper()
				result, ok := qualified.(OwnershipConcentrationResult)
				if !ok {
					t.Fatalf("QualifyPaths returned %T", qualified)
				}
				concentration, exists := result.SubsystemConcentration["alpha:cmd"]
				if !exists || concentration.Gini != 0.25 {
					t.Fatalf("SubsystemConcentration = %v", result.SubsystemConcentration)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.check(t, test.item.QualifyPaths(test.input, "alpha"))
		})
	}
}

// TestQualifyPathsWithoutRepositoryKeepsBarePaths guards the single-repository case: a result whose
// origin is unknown must not gain a separator with nothing in front of it.
func TestQualifyPathsWithoutRepositoryKeepsBarePaths(t *testing.T) {
	qualified := (&CouplesAnalysis{}).QualifyPaths(
		CouplesResult{Files: []string{testReadmePath}}, "",
	)

	result, ok := qualified.(CouplesResult)
	if !ok {
		t.Fatalf("QualifyPaths returned %T", qualified)
	}
	if want := []string{testReadmePath}; !slices.Equal(result.Files, want) {
		t.Fatalf("Files = %v, want %v", result.Files, want)
	}
}

func TestQualifyPathsRejectsForeignResult(t *testing.T) {
	if _, isErr := (&CouplesAnalysis{}).QualifyPaths(BusFactorResult{}, "alpha").(error); !isErr {
		t.Fatal("QualifyPaths accepted a result of another analysis")
	}
}
