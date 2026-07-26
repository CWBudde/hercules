package analysisio

import (
	"encoding/json"
	"fmt"

	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/pb"
)

const (
	analysisBurndown = "Burndown"
	analysisCouples  = "Couples"
	analysisUAST     = "UASTChangesSaver"
)

// ValidateAndMigrateAnalysisResults validates the envelope version and
// migrates every supported legacy payload before marking it as current. The
// version is changed only after every content entry has been decoded and
// validated successfully.
func ValidateAndMigrateAnalysisResults(results *pb.AnalysisResults, limits Limits) error {
	if results == nil {
		return fmt.Errorf("%w: protobuf envelope is nil", ErrAnalysisMalformed)
	}

	header := results.GetHeader()
	if header == nil {
		return fmt.Errorf("%w: protobuf metadata header is missing", ErrAnalysisMalformed)
	}

	version := header.GetVersion()
	switch {
	case version == 0:
		return fmt.Errorf("%w: protobuf metadata schema version is missing", ErrAnalysisMalformed)
	case version < 0:
		return fmt.Errorf("%w: protobuf metadata schema version %d is malformed", ErrAnalysisMalformed, version)
	case !pb.IsSupportedSchemaVersion(version):
		return fmt.Errorf(
			"%w: protobuf schema version %d is outside the supported range %d..%d",
			ErrAnalysisVersionUnsupported, version, pb.OldestSupportedSchemaVersion, pb.SchemaVersion,
		)
	case version == pb.SchemaVersion:
		return ValidateAnalysisResults(results, limits)
	case version == 1:
		return migrateProtobufV1(results, limits)
	default:
		return fmt.Errorf(
			"%w: protobuf schema version %d has no migration",
			ErrAnalysisVersionUnsupported, version,
		)
	}
}

func migrateProtobufV1(results *pb.AnalysisResults, limits Limits) error {
	if results.GetRefactoringProxy() != nil {
		return fmt.Errorf(
			"%w: protobuf schema version 1 top-level refactoring proxy has no migration",
			ErrAnalysisVersionUnsupported,
		)
	}

	migrated := make(map[string][]byte, len(results.GetContents()))

	for name, data := range results.GetContents() {
		var (
			value []byte
			err   error
		)

		switch name {
		case analysisBurndown:
			value, err = migrateBurndownV1(data, limits)
		case analysisCouples:
			value, err = migrateCouplesV1(data, limits)
		case analysisUAST:
			value, err = migrateUASTChangesV1(data, limits)
		default:
			err = fmt.Errorf(
				"%w: protobuf schema version 1 analysis %q has no validated migration",
				ErrAnalysisVersionUnsupported, name,
			)
		}

		if err != nil {
			return fmt.Errorf("migrate protobuf schema version 1 %s: %w", name, err)
		}

		migrated[name] = value
	}

	candidate := &pb.AnalysisResults{
		Header: &pb.Metadata{
			Version:       pb.SchemaVersion,
			Repository:    results.GetHeader().GetRepository(),
			BeginUnixTime: results.GetHeader().GetBeginUnixTime(),
			EndUnixTime:   results.GetHeader().GetEndUnixTime(),
		},
		Contents: migrated,
	}

	err := ValidateAnalysisResults(candidate, limits)
	if err != nil {
		return fmt.Errorf("validate migrated protobuf schema version 1: %w", err)
	}

	// Metadata field 2 contained the command line in schema 1, so it must not
	// be relabelled as the schema-2 Git hash.
	results.Header.Hash = ""
	results.Header.Version = pb.SchemaVersion
	results.Header.Commits = 0
	results.Header.RunTime = 0
	results.Header.RunTimePerItem = nil
	results.Contents = migrated

	return nil
}

func migrateBurndownV1(data []byte, limits Limits) ([]byte, error) {
	message := &pb.BurndownAnalysisResults{}

	err := Unmarshal(data, message, limits)
	if err != nil {
		// Schema 1 did not contain file ownership. Validate the wire structure
		// first, then fill the explicitly unavailable values below.
		if len(message.GetFiles()) == 0 || len(message.GetFilesOwnership()) != 0 {
			return nil, err
		}

		ownership := make([]*pb.FilesOwnership, len(message.GetFiles()))
		for index := range ownership {
			ownership[index] = &pb.FilesOwnership{Value: map[int32]int32{}}
		}

		message.FilesOwnership = ownership

		err = ValidateMessage(message, limits)
		if err != nil {
			return nil, err
		}
	}

	return marshalMigrated(analysisBurndown, message)
}

func migrateCouplesV1(data []byte, limits Limits) ([]byte, error) {
	legacy := &couplesAnalysisResultsV1{}

	err := Unmarshal(data, legacy, limits)
	if err != nil {
		return nil, err
	}

	peopleFiles := []*pb.TouchedFiles(nil)
	if legacy.TouchedFiles != nil {
		peopleFiles = legacy.TouchedFiles.Developers
	}

	fileCount := 0
	if legacy.FileCouples != nil {
		fileCount = len(legacy.FileCouples.GetIndex())
	}

	message := &pb.CouplesAnalysisResults{
		FileCouples:   legacy.FileCouples,
		PeopleCouples: legacy.PeopleCouples,
		PeopleFiles:   peopleFiles,
		FilesLines:    make([]int32, fileCount),
	}

	err = ValidateMessage(message, limits)
	if err != nil {
		return nil, err
	}

	return marshalMigrated(analysisCouples, message)
}

func migrateUASTChangesV1(data []byte, limits Limits) ([]byte, error) {
	legacy := &uastChangesSaverResultsV1{}

	err := Unmarshal(data, legacy, limits)
	if err != nil {
		return nil, err
	}

	records := make([]uastChangeRecordV2, len(legacy.Changes))
	for index, change := range legacy.Changes {
		if change == nil {
			return nil, fmt.Errorf("%w: UAST change %d is nil", ErrAnalysisMalformed, index)
		}

		records[index] = uastChangeRecordV2{
			FileName:   change.FileName,
			SrcBefore:  change.SrcBefore,
			SrcAfter:   change.SrcAfter,
			UASTBefore: change.UASTBefore,
			UASTAfter:  change.UASTAfter,
		}
	}

	value, err := json.Marshal(map[string]any{"changes": records})
	if err != nil {
		return nil, fmt.Errorf("%w: encode migrated UAST changes: %w", ErrAnalysisMalformed, err)
	}

	return append(value, '\n'), nil
}

func marshalMigrated(name string, message proto.Message) ([]byte, error) {
	value, err := proto.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("%w: encode migrated %s: %w", ErrAnalysisMalformed, name, err)
	}

	return value, nil
}

type developerTouchedFilesV1 struct {
	Developers []*pb.TouchedFiles `protobuf:"bytes,1,rep,name=developers"`
}

func (message *developerTouchedFilesV1) Reset()         { *message = developerTouchedFilesV1{} }
func (message *developerTouchedFilesV1) String() string { return proto.CompactTextString(message) }
func (*developerTouchedFilesV1) ProtoMessage()          {}

type couplesAnalysisResultsV1 struct {
	FileCouples   *pb.Couples              `protobuf:"bytes,6,opt,name=file_couples,json=fileCouples"`
	PeopleCouples *pb.Couples              `protobuf:"bytes,7,opt,name=developer_couples,json=developerCouples"`
	TouchedFiles  *developerTouchedFilesV1 `protobuf:"bytes,8,opt,name=touched_files,json=touchedFiles"`
}

func (message *couplesAnalysisResultsV1) Reset()         { *message = couplesAnalysisResultsV1{} }
func (message *couplesAnalysisResultsV1) String() string { return proto.CompactTextString(message) }
func (*couplesAnalysisResultsV1) ProtoMessage()          {}

type uastChangeV1 struct {
	FileName   string `protobuf:"bytes,1,opt,name=file_name,json=fileName,proto3"`
	SrcBefore  string `protobuf:"bytes,2,opt,name=src_before,json=srcBefore,proto3"`
	SrcAfter   string `protobuf:"bytes,3,opt,name=src_after,json=srcAfter,proto3"`
	UASTBefore string `protobuf:"bytes,4,opt,name=uast_before,json=uastBefore,proto3"`
	UASTAfter  string `protobuf:"bytes,5,opt,name=uast_after,json=uastAfter,proto3"`
}

func (message *uastChangeV1) Reset()         { *message = uastChangeV1{} }
func (message *uastChangeV1) String() string { return proto.CompactTextString(message) }
func (*uastChangeV1) ProtoMessage()          {}

type uastChangesSaverResultsV1 struct {
	Changes []*uastChangeV1 `protobuf:"bytes,1,rep,name=changes"`
}

func (message *uastChangesSaverResultsV1) Reset()         { *message = uastChangesSaverResultsV1{} }
func (message *uastChangesSaverResultsV1) String() string { return proto.CompactTextString(message) }
func (*uastChangesSaverResultsV1) ProtoMessage()          {}

type uastChangeRecordV2 struct {
	FileName   string `json:"file"`
	SrcBefore  string `json:"src0"`
	SrcAfter   string `json:"src1"`
	UASTBefore string `json:"uast0"`
	UASTAfter  string `json:"uast1"`
}
