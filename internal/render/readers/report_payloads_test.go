package readers

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/cwbudde/hercules/internal/pb"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/require"
)

func TestProtobufReader_CurrentHerculesReportPayloads(t *testing.T) {
	contents := map[string][]byte{
		"Burndown": marshalProto(t, &pb.BurndownAnalysisResults{
			Project: &pb.BurndownSparseMatrix{
				Name:            "project",
				NumberOfRows:    1,
				NumberOfColumns: 2,
				Rows: []*pb.BurndownSparseMatrixRow{
					{Columns: []uint32{10, 8}},
				},
			},
			RepositorySequence: []string{"repo-a", "repo-b"},
			Repositories: []*pb.BurndownSparseMatrix{
				{
					Name:            "repo-a",
					NumberOfRows:    1,
					NumberOfColumns: 2,
					Rows: []*pb.BurndownSparseMatrixRow{
						{Columns: []uint32{7, 5}},
					},
				},
			},
		}),
		"Sentiment": marshalProto(t, &pb.CommentSentimentResults{
			SentimentByTick: map[int32]*pb.Sentiment{
				3: {
					Value:    0.75,
					Comments: []string{"looks good"},
					Commits:  []string{"abc123"},
				},
			},
		}),
		"TemporalActivity": marshalProto(t, &pb.TemporalActivityResults{
			Activities: map[int32]*pb.DeveloperTemporalActivity{
				0: {
					Weekdays: &pb.TemporalDimension{Commits: []int32{1, 2}, Lines: []int32{10, 20}},
					Hours:    &pb.TemporalDimension{Commits: []int32{3}, Lines: []int32{30}},
					Months:   &pb.TemporalDimension{Commits: []int32{4}, Lines: []int32{40}},
					Weeks:    &pb.TemporalDimension{Commits: []int32{5}, Lines: []int32{50}},
				},
			},
			DevIndex: []string{"dev-a"},
			Ticks: map[int32]*pb.TemporalActivityTickDevs{
				1: {
					Devs: map[int32]*pb.TemporalActivityTick{
						0: {Commits: 2, Lines: 12, Weekday: 1, Hour: 9, Month: 4, Week: 17},
					},
				},
			},
			TickSize: 86400,
		}),
		"Devs": marshalProto(t, &pb.DevsAnalysisResults{
			DevIndex: []string{"dev-a", "dev-b"},
			TickSize: int64(86400 * 1_000_000_000),
			Ticks: map[int32]*pb.TickDevs{
				0: {
					Devs: map[int32]*pb.DevTick{
						0: {
							Commits: 2,
							Stats:   &pb.LineStats{Added: 10, Removed: 1, Changed: 3},
							Languages: map[string]*pb.LineStats{
								"Go": {Added: 10, Removed: 1, Changed: 3},
							},
						},
					},
				},
				1: {
					Devs: map[int32]*pb.DevTick{
						0: {
							Commits: 1,
							Stats:   &pb.LineStats{Added: 4, Removed: 2, Changed: 1},
							Languages: map[string]*pb.LineStats{
								"Go": {Added: 4, Removed: 2, Changed: 1},
							},
						},
						1: {
							Commits: 3,
							Stats:   &pb.LineStats{Added: 7, Removed: 0, Changed: 2},
							Languages: map[string]*pb.LineStats{
								"Python": {Added: 7, Removed: 0, Changed: 2},
							},
						},
					},
				},
			},
		}),
		"BusFactor": marshalProto(t, &pb.BusFactorAnalysisResults{
			Snapshots: map[int32]*pb.BusFactorTickSnapshot{
				1: {BusFactor: 2, TotalLines: 100, AuthorLines: map[int32]int64{0: 60, 1: 40}},
			},
			SubsystemBusFactor: map[string]int32{"cmd": 1},
			DevIndex:           []string{"dev-a", "dev-b"},
			TickSize:           86400,
			Threshold:          0.8,
		}),
		"OwnershipConcentration": marshalProto(t, &pb.OwnershipConcentrationResults{
			Snapshots: map[int32]*pb.OwnershipConcentrationTickSnapshot{
				1: {Gini: 0.3, Hhi: 0.6, TotalLines: 100, AuthorLines: map[int32]int64{0: 60, 1: 40}},
			},
			SubsystemGini: map[string]float64{"cmd": 0.2},
			SubsystemHhi:  map[string]float64{"cmd": 0.5},
			DevIndex:      []string{"dev-a", "dev-b"},
			TickSize:      86400,
		}),
		"KnowledgeDiffusion": marshalProto(t, &pb.KnowledgeDiffusionResults{
			Files: map[string]*pb.KnowledgeDiffusionFileData{
				"main.go": {
					UniqueEditorsCount:    2,
					UniqueEditorsOverTime: map[int32]int32{1: 1, 2: 2},
					RecentEditorsCount:    1,
					Authors:               []int32{0, 1},
				},
			},
			Distribution: map[int32]int32{2: 1},
			WindowMonths: 6,
			DevIndex:     []string{"dev-a", "dev-b"},
			TickSize:     86400,
		}),
		"HotspotRisk": marshalProto(t, &pb.HotspotRiskResults{
			WindowDays: 90,
			Files: []*pb.FileRisk{
				{
					Path:                "main.go",
					RiskScore:           0.9,
					Size_:               100,
					Churn:               12,
					CouplingDegree:      3,
					OwnershipGini:       0.4,
					SizeNormalized:      0.5,
					ChurnNormalized:     0.6,
					CouplingNormalized:  0.7,
					OwnershipNormalized: 0.8,
				},
			},
		}),
		"RefactoringProxy": marshalProto(t, &pb.RefactoringProxyResults{
			Ticks:         []int32{1},
			RenameRatios:  []float32{0.4},
			IsRefactoring: []bool{true},
			TotalChanges:  []int32{10},
			Threshold:     0.3,
			TickSize:      int64(86400 * 1_000_000_000),
		}),
		"CommitsStat": marshalProto(t, &pb.CommitsAnalysisResults{
			Commits: []*pb.Commit{
				{
					Hash:         "abc123",
					WhenUnixTime: 1700000100,
					Author:       0,
					Files: []*pb.CommitFile{
						{Name: "main.go", Language: "Go", Stats: &pb.LineStats{Added: 5, Removed: 1, Changed: 2}},
					},
				},
			},
			AuthorIndex: []string{"dev-a"},
		}),
		"FileHistoryAnalysis": marshalProto(t, &pb.FileHistoryResultMessage{
			Files: map[string]*pb.FileHistory{
				"main.go": {
					Commits: []string{"abc123"},
					ChangesByDeveloper: map[int32]*pb.LineStats{
						0: {Added: 5, Removed: 1, Changed: 2},
					},
				},
			},
		}),
	}

	payload := &pb.AnalysisResults{
		Header: &pb.Metadata{
			Repository:    "repo",
			BeginUnixTime: 1700000000,
			EndUnixTime:   1700864000,
		},
		Contents: contents,
	}

	reader := &ProtobufReader{}
	require.NoError(t, reader.Read(bytes.NewReader(marshalProto(t, payload))))

	repos, err := reader.GetRepositoriesBurndown()
	require.NoError(t, err)
	require.Len(t, repos, 1)
	require.Equal(t, "repo-a", repos[0].Repository)

	names, err := reader.GetRepositoryNames()
	require.NoError(t, err)
	require.Equal(t, []string{"repo-a", "repo-b"}, names)

	sentiment, err := reader.GetSentimentByTick()
	require.NoError(t, err)
	require.Equal(t, float32(0.75), sentiment[3].Value)

	temporal, err := reader.GetTemporalActivity()
	require.NoError(t, err)
	require.Equal(t, []string{"dev-a"}, temporal.People)
	require.Equal(t, 2, temporal.Ticks[1][0].Commits)

	developerStats, err := reader.GetDeveloperStats()
	require.NoError(t, err)
	require.Len(t, developerStats, 2)
	require.Equal(t, "dev-a", developerStats[0].Name)
	require.Equal(t, 3, developerStats[0].Commits)
	require.Equal(t, 14, developerStats[0].LinesAdded)
	require.Equal(t, 3, developerStats[0].LinesRemoved)
	require.Equal(t, 4, developerStats[0].LinesModified)
	require.Equal(t, map[string]int{"Go": 21}, developerStats[0].Languages)
	require.Equal(t, "dev-b", developerStats[1].Name)
	require.Equal(t, 3, developerStats[1].Commits)
	require.Equal(t, 7, developerStats[1].LinesAdded)
	require.Equal(t, map[string]int{"Python": 9}, developerStats[1].Languages)

	busFactor, err := reader.GetBusFactor()
	require.NoError(t, err)
	require.Equal(t, 2, busFactor.Snapshots[1].BusFactor)
	require.Equal(t, 1, busFactor.SubsystemBusFactor["cmd"])

	ownership, err := reader.GetOwnershipConcentration()
	require.NoError(t, err)
	require.Equal(t, 0.3, ownership.Snapshots[1].Gini)
	require.Equal(t, 0.5, ownership.SubsystemHHI["cmd"])

	diffusion, err := reader.GetKnowledgeDiffusion()
	require.NoError(t, err)
	require.Equal(t, 2, diffusion.Files["main.go"].UniqueEditors)
	require.Equal(t, 1, diffusion.Distribution[2])

	hotspot, err := reader.GetHotspotRisk()
	require.NoError(t, err)
	require.Equal(t, 90, hotspot.WindowDays)
	require.Equal(t, "main.go", hotspot.Files[0].Path)

	refactoring, err := reader.GetRefactoringProxy()
	require.NoError(t, err)
	require.Equal(t, float32(0.4), refactoring.Ticks[0].RefactoringRate)
	require.True(t, refactoring.Ticks[0].IsRefactoring)

	commits, err := reader.GetCommits()
	require.NoError(t, err)
	require.Equal(t, "abc123", commits.Commits[0].Hash)
	require.Equal(t, "Go", commits.Commits[0].Files[0].Language)

	history, err := reader.GetFileHistory()
	require.NoError(t, err)
	require.Equal(t, 5, history.Files["main.go"].ChangesByDeveloper[0].Added)
}

func TestProtobufReader_ReportPayloadErrorsAreTyped(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		reader := &ProtobufReader{}
		payload := &pb.AnalysisResults{
			Header:   &pb.Metadata{Repository: "repo"},
			Contents: map[string][]byte{},
		}
		require.NoError(t, reader.Read(bytes.NewReader(marshalProto(t, payload))))

		_, err := reader.GetTemporalActivity()
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrAnalysisMissing))
	})

	t.Run("malformed", func(t *testing.T) {
		reader := &ProtobufReader{}
		payload := &pb.AnalysisResults{
			Header: &pb.Metadata{Repository: "repo"},
			Contents: map[string][]byte{
				"TemporalActivity": []byte("not protobuf"),
			},
		}
		require.NoError(t, reader.Read(bytes.NewReader(marshalProto(t, payload))))

		_, err := reader.GetTemporalActivity()
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrAnalysisMalformed))
	})
}

func TestProtobufReader_CurrentHerculesReportDefaultFixture(t *testing.T) {
	data, err := os.ReadFile("../testdata/hercules/report_default.pb")
	require.NoError(t, err)

	var payload pb.AnalysisResults
	require.NoError(t, proto.Unmarshal(data, &payload))

	requiredContents := []string{
		"Burndown",
		"Couples",
		"Devs",
		"TemporalActivity",
		"BusFactor",
		"OwnershipConcentration",
		"KnowledgeDiffusion",
		"HotspotRisk",
	}
	for _, key := range requiredContents {
		require.Contains(t, payload.Contents, key)
	}

	reader := &ProtobufReader{}
	require.NoError(t, reader.Read(bytes.NewReader(data)))

	_, _, projectBurndown, err := reader.GetProjectBurndownWithHeader()
	require.NoError(t, err)
	require.NotEmpty(t, projectBurndown)

	files, err := reader.GetFilesBurndown()
	require.NoError(t, err)
	require.NotEmpty(t, files)

	people, err := reader.GetPeopleBurndown()
	require.NoError(t, err)
	require.NotEmpty(t, people)

	_, interaction, err := reader.GetPeopleInteraction()
	require.NoError(t, err)
	require.NotEmpty(t, interaction)

	fileIndex, fileCooccurrence, err := reader.GetFileCooccurrence()
	require.NoError(t, err)
	require.NotEmpty(t, fileIndex)
	require.NotEmpty(t, fileCooccurrence)

	peopleIndex, peopleCooccurrence, err := reader.GetPeopleCooccurrence()
	require.NoError(t, err)
	require.NotEmpty(t, peopleIndex)
	require.NotEmpty(t, peopleCooccurrence)

	devs, err := reader.GetDeveloperTimeSeriesData()
	require.NoError(t, err)
	require.NotEmpty(t, devs.People)
	require.NotEmpty(t, devs.Days)

	temporal, err := reader.GetTemporalActivity()
	require.NoError(t, err)
	require.NotEmpty(t, temporal.People)

	busFactor, err := reader.GetBusFactor()
	require.NoError(t, err)
	require.NotEmpty(t, busFactor.Snapshots)

	ownership, err := reader.GetOwnershipConcentration()
	require.NoError(t, err)
	require.NotEmpty(t, ownership.Snapshots)

	diffusion, err := reader.GetKnowledgeDiffusion()
	require.NoError(t, err)
	require.NotEmpty(t, diffusion.Files)

	hotspot, err := reader.GetHotspotRisk()
	require.NoError(t, err)
	require.NotEmpty(t, hotspot.Files)
}

func TestProtobufReader_CurrentHerculesShotnessFixture(t *testing.T) {
	data, err := os.ReadFile("../testdata/hercules/shotness.pb")
	require.NoError(t, err)

	var payload pb.AnalysisResults
	require.NoError(t, proto.Unmarshal(data, &payload))
	require.Contains(t, payload.Contents, "Shotness")

	reader := &ProtobufReader{}
	require.NoError(t, reader.Read(bytes.NewReader(data)))

	records, err := reader.GetShotnessRecords()
	require.NoError(t, err)
	require.NotEmpty(t, records)

	index, cooccurrence, err := reader.GetShotnessCooccurrence()
	require.NoError(t, err)
	require.Len(t, index, len(records))
	require.Len(t, cooccurrence, len(records))
	requireShotnessCooccurrenceMatchesPythonCounterMatrix(t, records, cooccurrence)
}

func TestProtobufReader_ShotnessCooccurrenceUsesPythonCounterMatrix(t *testing.T) {
	payload := &pb.AnalysisResults{
		Contents: map[string][]byte{
			"Shotness": marshalProto(t, &pb.ShotnessAnalysisResults{
				Records: []*pb.ShotnessRecord{
					{
						Type:     "function",
						Name:     "alpha",
						File:     "a.go",
						Counters: map[int32]int32{0: 3, 2: 1},
					},
					{
						Type:     "function",
						Name:     "beta",
						File:     "b.go",
						Counters: map[int32]int32{0: 2, 1: 5},
					},
					{
						Type:     "function",
						Name:     "gamma",
						File:     "c.go",
						Counters: map[int32]int32{1: 4, 2: 1},
					},
				},
			}),
		},
	}
	data := marshalProto(t, payload)

	reader := &ProtobufReader{}
	require.NoError(t, reader.Read(bytes.NewReader(data)))

	index, cooccurrence, err := reader.GetShotnessCooccurrence()
	require.NoError(t, err)
	require.Equal(t, []string{"a.go:alpha", "b.go:beta", "c.go:gamma"}, index)
	require.Equal(t, [][]int{
		{3, 0, 1},
		{2, 5, 0},
		{0, 4, 1},
	}, cooccurrence)
}

func marshalProto(t *testing.T, message proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(message)
	require.NoError(t, err)
	return data
}

func requireShotnessCooccurrenceMatchesPythonCounterMatrix(t *testing.T, records []ShotnessRecord, matrix [][]int) {
	t.Helper()
	for i, record := range records {
		require.Len(t, matrix[i], len(records))
		for j := range records {
			expected := int32(0)
			j32, ok := checkedInt32(int64(j))
			require.True(t, ok, "record index %d overflows int32", j)
			if count, ok := record.Counters[j32]; ok {
				expected = count
			}
			require.Equalf(t, int(expected), matrix[i][j], "cooccurrence[%d][%d]", i, j)
		}
	}
}
