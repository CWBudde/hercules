package analysisio

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/pb"
)

const versionTestMainFile = "main.go"

func TestValidateAndMigrateAnalysisResultsVersions(t *testing.T) {
	tests := []struct {
		name        string
		results     *pb.AnalysisResults
		wantErr     error
		wantVersion int32
	}{
		{
			name:        "supported old",
			results:     &pb.AnalysisResults{Header: &pb.Metadata{Version: 1}},
			wantVersion: pb.SchemaVersion,
		},
		{
			name:        "current",
			results:     &pb.AnalysisResults{Header: &pb.Metadata{Version: pb.SchemaVersion}},
			wantVersion: pb.SchemaVersion,
		},
		{
			name:        "newer",
			results:     &pb.AnalysisResults{Header: &pb.Metadata{Version: pb.SchemaVersion + 1}},
			wantErr:     ErrAnalysisVersionUnsupported,
			wantVersion: pb.SchemaVersion + 1,
		},
		{
			name:        "missing version",
			results:     &pb.AnalysisResults{Header: &pb.Metadata{}},
			wantErr:     ErrAnalysisMalformed,
			wantVersion: 0,
		},
		{
			name:        "malformed version",
			results:     &pb.AnalysisResults{Header: &pb.Metadata{Version: -1}},
			wantErr:     ErrAnalysisMalformed,
			wantVersion: -1,
		},
		{
			name:    "missing header",
			results: &pb.AnalysisResults{},
			wantErr: ErrAnalysisMalformed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateAndMigrateAnalysisResults(test.results, DefaultLimits())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if test.results.GetHeader().GetVersion() != test.wantVersion {
				t.Fatalf(
					"version = %d, want %d",
					test.results.GetHeader().GetVersion(), test.wantVersion,
				)
			}
		})
	}
}

func TestMigrateProtobufV1Burndown(t *testing.T) {
	payload := marshalVersionTest(t, &pb.BurndownAnalysisResults{
		Project: legacyBurndownMatrix("project"),
		Files:   []*pb.BurndownSparseMatrix{legacyBurndownMatrix("main.go")},
	})
	results := &pb.AnalysisResults{
		Header: &pb.Metadata{
			Version:        1,
			Hash:           "hercules --burndown repo",
			Repository:     "repo",
			BeginUnixTime:  10,
			EndUnixTime:    20,
			Commits:        99,
			RunTime:        100,
			RunTimePerItem: map[string]float64{analysisBurndown: 1},
		},
		Contents: map[string][]byte{analysisBurndown: payload},
	}

	err := ValidateAndMigrateAnalysisResults(results, DefaultLimits())
	if err != nil {
		t.Fatalf("migrate v1 burndown: %v", err)
	}
	if results.GetHeader().GetVersion() != pb.SchemaVersion {
		t.Fatalf("version = %d, want %d", results.GetHeader().GetVersion(), pb.SchemaVersion)
	}
	if results.GetHeader().GetHash() != "" {
		t.Fatalf("schema-1 command line was relabelled as hash: %q", results.GetHeader().GetHash())
	}
	if results.GetHeader().GetCommits() != 0 ||
		results.GetHeader().GetRunTime() != 0 ||
		results.GetHeader().GetRunTimePerItem() != nil {
		t.Fatalf("schema-2-only metadata survived migration: %#v", results.GetHeader())
	}

	var migrated pb.BurndownAnalysisResults
	err = proto.Unmarshal(results.GetContents()[analysisBurndown], &migrated)
	if err != nil {
		t.Fatalf("decode migrated burndown: %v", err)
	}
	if len(migrated.GetFilesOwnership()) != 1 || migrated.GetFilesOwnership()[0] == nil {
		t.Fatalf("file ownership was not explicitly migrated: %#v", migrated.GetFilesOwnership())
	}
}

func TestMigrateProtobufV1Couples(t *testing.T) {
	legacy := &couplesAnalysisResultsV1{
		FileCouples:   versionTestCouples([]string{versionTestMainFile}),
		PeopleCouples: versionTestCouples([]string{"alice"}),
		TouchedFiles: &developerTouchedFilesV1{
			Developers: []*pb.TouchedFiles{{Files: []int32{0}}},
		},
	}
	results := &pb.AnalysisResults{
		Header:   &pb.Metadata{Version: 1},
		Contents: map[string][]byte{analysisCouples: marshalVersionTest(t, legacy)},
	}

	err := ValidateAndMigrateAnalysisResults(results, DefaultLimits())
	if err != nil {
		t.Fatalf("migrate v1 couples: %v", err)
	}

	var migrated pb.CouplesAnalysisResults
	err = proto.Unmarshal(results.GetContents()[analysisCouples], &migrated)
	if err != nil {
		t.Fatalf("decode migrated couples: %v", err)
	}
	if len(migrated.GetPeopleFiles()) != 1 ||
		len(migrated.GetPeopleFiles()[0].GetFiles()) != 1 ||
		migrated.GetPeopleFiles()[0].GetFiles()[0] != 0 {
		t.Fatalf("nested touched files were not migrated: %#v", migrated.GetPeopleFiles())
	}
	if len(migrated.GetFilesLines()) != 1 || migrated.GetFilesLines()[0] != 0 {
		t.Fatalf("unavailable file line counts were not initialized: %v", migrated.GetFilesLines())
	}
}

func TestMigrateProtobufV1UASTChanges(t *testing.T) {
	legacy := &uastChangesSaverResultsV1{
		Changes: []*uastChangeV1{{
			FileName:   versionTestMainFile,
			SrcBefore:  "before.go",
			SrcAfter:   "after.go",
			UASTBefore: "before.json",
			UASTAfter:  "after.json",
		}},
	}
	results := &pb.AnalysisResults{
		Header:   &pb.Metadata{Version: 1},
		Contents: map[string][]byte{analysisUAST: marshalVersionTest(t, legacy)},
	}

	err := ValidateAndMigrateAnalysisResults(results, DefaultLimits())
	if err != nil {
		t.Fatalf("migrate v1 UAST changes: %v", err)
	}

	var migrated struct {
		Changes []uastChangeRecordV2 `json:"changes"`
	}
	err = json.Unmarshal(results.GetContents()[analysisUAST], &migrated)
	if err != nil {
		t.Fatalf("decode migrated UAST changes: %v", err)
	}
	if len(migrated.Changes) != 1 || migrated.Changes[0].FileName != versionTestMainFile {
		t.Fatalf("UAST changes were not migrated: %#v", migrated.Changes)
	}
}

func TestMigrateProtobufV1DoesNotRelabelUnvalidatedContent(t *testing.T) {
	results := &pb.AnalysisResults{
		Header: &pb.Metadata{Version: 1},
		Contents: map[string][]byte{
			"Devs": marshalVersionTest(t, &pb.DevsAnalysisResults{}),
		},
	}

	err := ValidateAndMigrateAnalysisResults(results, DefaultLimits())
	if !errors.Is(err, ErrAnalysisVersionUnsupported) {
		t.Fatalf("error = %v, want ErrAnalysisVersionUnsupported", err)
	}
	if results.GetHeader().GetVersion() != 1 {
		t.Fatalf("unvalidated content was relabelled as version %d", results.GetHeader().GetVersion())
	}
}

func legacyBurndownMatrix(name string) *pb.BurndownSparseMatrix {
	return &pb.BurndownSparseMatrix{
		Name:            name,
		NumberOfRows:    1,
		NumberOfColumns: 1,
		Rows:            []*pb.BurndownSparseMatrixRow{{Columns: []uint32{1}}},
	}
}

func versionTestCouples(index []string) *pb.Couples {
	return &pb.Couples{
		Index: index,
		Matrix: &pb.CompressedSparseRowMatrix{
			NumberOfRows:    1,
			NumberOfColumns: 1,
			Indptr:          make([]int64, len(index)+1),
		},
	}
}

func marshalVersionTest(t *testing.T, message proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatalf("marshal test protobuf: %v", err)
	}
	return data
}
