package readers

import (
	"bytes"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/pb"
	"github.com/cwbudde/hercules/internal/tickgrid"
)

const (
	secondsPerTestDay = int64(24 * 60 * 60)
	// onboardingFirstCommitUnix is the merge anchor used by the onboarding
	// payloads: 2023-11-14T22:13:20Z.
	onboardingFirstCommitUnix = int64(1700000000)
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
		"Onboarding": marshalProto(t, &pb.OnboardingResults{
			Authors: map[int32]*pb.AuthorOnboardingData{
				0: {
					FirstCommitTick: 5,
					JoinCohort:      "2023-11",
					FirstCommitUnix: onboardingFirstCommitUnix,
					Snapshots: map[int32]*pb.OnboardingSnapshot{
						30: {
							DaysSinceJoin: 30, TotalCommits: 12, TotalFiles: 7, TotalLines: 400,
							MeaningfulCommits: 4, MeaningfulFiles: 3, MeaningfulLines: 260,
						},
						90: {
							DaysSinceJoin: 90, TotalCommits: 41, TotalFiles: 19, TotalLines: 1500,
							MeaningfulCommits: 15, MeaningfulFiles: 11, MeaningfulLines: 980,
						},
					},
					// Deliberately unsorted, and the meaningful entries are not the
					// earliest ones, so the reader has to pick the earliest entry that
					// actually carries a meaningful commit.
					Trail: []*pb.OnboardingTrailEntry{
						{UnixTime: onboardingFirstCommitUnix + 10*secondsPerTestDay, Commits: 2, MeaningfulCommits: 1, Lines: 80},
						{UnixTime: onboardingFirstCommitUnix, Commits: 1, NewFiles: 1, Lines: 3},
						{
							UnixTime:           onboardingFirstCommitUnix + 3*secondsPerTestDay + 3600,
							Commits:            3,
							NewFiles:           2,
							Lines:              120,
							MeaningfulCommits:  2,
							NewMeaningfulFiles: 1,
							MeaningfulLines:    110,
						},
					},
				},
				1: {
					FirstCommitTick: 9,
					JoinCohort:      "2023-12",
					FirstCommitUnix: onboardingFirstCommitUnix + 40*secondsPerTestDay,
					Snapshots: map[int32]*pb.OnboardingSnapshot{
						30: {DaysSinceJoin: 30, TotalCommits: 3, TotalFiles: 2, TotalLines: 20},
					},
					// Trail present but nothing meaningful in it.
					Trail: []*pb.OnboardingTrailEntry{
						{UnixTime: onboardingFirstCommitUnix + 40*secondsPerTestDay, Commits: 1, Lines: 4},
						{UnixTime: onboardingFirstCommitUnix + 44*secondsPerTestDay, Commits: 2, Lines: 9},
					},
				},
				// The author-missing bucket, written without any trail at all.
				-1: {
					FirstCommitTick: 0,
					JoinCohort:      "2023-11",
					Snapshots: map[int32]*pb.OnboardingSnapshot{
						30: {DaysSinceJoin: 30, TotalCommits: 1, TotalLines: 6},
					},
				},
			},
			Cohorts: map[string]*pb.CohortStats{
				"2023-11": {
					Cohort:      "2023-11",
					AuthorCount: 2,
					AverageSnapshots: map[int32]*pb.OnboardingAverageSnapshot{
						30: {
							DaysSinceJoin: 30, AvgTotalCommits: 6.5, AvgTotalFiles: 3.5, AvgTotalLines: 203,
							AvgMeaningfulCommits: 2, AvgMeaningfulFiles: 1.5, AvgMeaningfulLines: 130,
						},
						90: {
							DaysSinceJoin: 90, AvgTotalCommits: 20.5, AvgTotalFiles: 9.5, AvgTotalLines: 750,
							AvgMeaningfulCommits: 7.5, AvgMeaningfulFiles: 5.5, AvgMeaningfulLines: 490,
						},
					},
				},
			},
			WindowDays:          []int32{30, 90},
			MeaningfulThreshold: 10,
			DevIndex:            []string{"dev-a", "dev-b"},
			TickSize:            int64(86400 * 1_000_000_000),
			TrailDays:           90,
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
			Version:       pb.SchemaVersion,
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
	require.InDelta(t, float32(0.75), sentiment[3].Value, 1e-6)

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

	onboarding, err := reader.GetOnboarding()
	require.NoError(t, err)
	require.Equal(t, []int{30, 90}, onboarding.WindowDays)
	require.Equal(t, 10, onboarding.MeaningfulThreshold)
	require.Equal(t, []string{"dev-a", "dev-b"}, onboarding.People)
	require.Equal(t, int64(86400*1_000_000_000), onboarding.TickSize)

	require.Len(t, onboarding.Authors, 3)
	firstAuthor := onboarding.Authors[0]
	require.Equal(t, 5, firstAuthor.FirstCommitTick)
	require.Equal(t, "2023-11", firstAuthor.JoinCohort)
	require.Equal(t, 3, firstAuthor.DaysToFirstMeaningfulCommit)
	require.Len(t, firstAuthor.Snapshots, 2)
	require.Equal(t, OnboardingSnapshotData{
		DaysSinceJoin: 30, TotalCommits: 12, TotalFiles: 7, TotalLines: 400,
		MeaningfulCommits: 4, MeaningfulFiles: 3, MeaningfulLines: 260,
	}, firstAuthor.Snapshots[30])
	require.Equal(t, OnboardingSnapshotData{
		DaysSinceJoin: 90, TotalCommits: 41, TotalFiles: 19, TotalLines: 1500,
		MeaningfulCommits: 15, MeaningfulFiles: 11, MeaningfulLines: 980,
	}, firstAuthor.Snapshots[90])

	// A trail without a single meaningful commit, and a payload without any
	// trail, both report the "no meaningful commit yet" sentinel.
	require.Equal(t, -1, onboarding.Authors[1].DaysToFirstMeaningfulCommit)
	require.Equal(t, "2023-12", onboarding.Authors[1].JoinCohort)
	missingAuthor, ok := onboarding.Authors[-1]
	require.True(t, ok, "the author-missing bucket survives the int32->int key conversion")
	require.Equal(t, -1, missingAuthor.DaysToFirstMeaningfulCommit)
	require.Equal(t, 1, missingAuthor.Snapshots[30].TotalCommits)

	require.Len(t, onboarding.Cohorts, 1)
	cohort := onboarding.Cohorts["2023-11"]
	require.Equal(t, "2023-11", cohort.Cohort)
	require.Equal(t, 2, cohort.AuthorCount)
	require.Len(t, cohort.AverageSnapshots, 2)
	require.Equal(t, OnboardingAverageSnapshotData{
		DaysSinceJoin: 30, AvgTotalCommits: 6.5, AvgTotalFiles: 3.5, AvgTotalLines: 203,
		AvgMeaningfulCommits: 2, AvgMeaningfulFiles: 1.5, AvgMeaningfulLines: 130,
	}, cohort.AverageSnapshots[30])
	require.InDelta(t, 490.0, cohort.AverageSnapshots[90].AvgMeaningfulLines, 1e-9)

	ownership, err := reader.GetOwnershipConcentration()
	require.NoError(t, err)
	require.InDelta(t, 0.3, ownership.Snapshots[1].Gini, 1e-9)
	require.InDelta(t, 0.5, ownership.SubsystemHHI["cmd"], 1e-9)

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
	require.InDelta(t, float32(0.4), refactoring.Ticks[0].RefactoringRate, 1e-6)
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
			Header:   &pb.Metadata{Version: pb.SchemaVersion, Repository: "repo"},
			Contents: map[string][]byte{},
		}
		require.NoError(t, reader.Read(bytes.NewReader(marshalProto(t, payload))))

		_, err := reader.GetTemporalActivity()
		require.Error(t, err)
		require.ErrorIs(t, err, ErrAnalysisMissing)
	})

	t.Run("malformed", func(t *testing.T) {
		reader := &ProtobufReader{}
		payload := &pb.AnalysisResults{
			Header: &pb.Metadata{Version: pb.SchemaVersion, Repository: "repo"},
			Contents: map[string][]byte{
				"TemporalActivity": []byte("not protobuf"),
			},
		}
		require.NoError(t, reader.Read(bytes.NewReader(marshalProto(t, payload))))

		_, err := reader.GetTemporalActivity()
		require.Error(t, err)
		require.ErrorIs(t, err, ErrAnalysisMalformed)
	})
}

// TestProtobufReader_OnboardingDaysToFirstMeaningfulCommit pins the contract of
// the one derived onboarding field: whole days from the author's first commit to
// the earliest trail entry carrying a meaningful commit, and -1 when the trail
// holds none (which also covers reports written before the trail existed).
func TestProtobufReader_OnboardingDaysToFirstMeaningfulCommit(t *testing.T) {
	const anchor = onboardingFirstCommitUnix

	for _, testCase := range []struct {
		name   string
		author *pb.AuthorOnboardingData
		want   int
	}{
		{
			name: "whole days are truncated, not rounded",
			author: &pb.AuthorOnboardingData{
				FirstCommitUnix: anchor,
				Trail: []*pb.OnboardingTrailEntry{
					{UnixTime: anchor, Commits: 1},
					{UnixTime: anchor + 5*secondsPerTestDay + 23*3600, Commits: 1, MeaningfulCommits: 1},
				},
			},
			want: 5,
		},
		{
			name: "earliest meaningful entry wins regardless of trail order",
			author: &pb.AuthorOnboardingData{
				FirstCommitUnix: anchor,
				Trail: []*pb.OnboardingTrailEntry{
					{UnixTime: anchor + 9*secondsPerTestDay, Commits: 1, MeaningfulCommits: 4},
					{UnixTime: anchor + 2*secondsPerTestDay, Commits: 1, MeaningfulCommits: 1},
					{UnixTime: anchor + 6*secondsPerTestDay, Commits: 1, MeaningfulCommits: 2},
				},
			},
			want: 2,
		},
		{
			name: "meaningful on the very first day is day zero",
			author: &pb.AuthorOnboardingData{
				FirstCommitUnix: anchor,
				Trail: []*pb.OnboardingTrailEntry{
					{UnixTime: anchor, Commits: 1, MeaningfulCommits: 1},
				},
			},
			want: 0,
		},
		{
			name: "trail without a meaningful commit yields the sentinel",
			author: &pb.AuthorOnboardingData{
				FirstCommitUnix: anchor,
				Trail: []*pb.OnboardingTrailEntry{
					{UnixTime: anchor, Commits: 1, Lines: 2},
					{UnixTime: anchor + 3*secondsPerTestDay, Commits: 2, Lines: 9},
				},
			},
			want: -1,
		},
		{
			name: "absent trail yields the sentinel",
			author: &pb.AuthorOnboardingData{
				FirstCommitUnix: anchor,
				Snapshots: map[int32]*pb.OnboardingSnapshot{
					30: {DaysSinceJoin: 30, TotalCommits: 4, MeaningfulCommits: 2},
				},
			},
			want: -1,
		},
		{
			name:   "empty author yields the sentinel",
			author: &pb.AuthorOnboardingData{},
			want:   -1,
		},
		{
			name: "a missing anchor falls back to the earliest trail entry",
			author: &pb.AuthorOnboardingData{
				Trail: []*pb.OnboardingTrailEntry{
					{UnixTime: anchor + 4*secondsPerTestDay, Commits: 1, MeaningfulCommits: 1},
					{UnixTime: anchor, Commits: 1},
				},
			},
			want: 4,
		},
		{
			name: "a meaningful commit before the anchor clamps to zero",
			author: &pb.AuthorOnboardingData{
				FirstCommitUnix: anchor,
				Trail: []*pb.OnboardingTrailEntry{
					{UnixTime: anchor - 7*secondsPerTestDay, Commits: 1, MeaningfulCommits: 1},
				},
			},
			want: 0,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			reader := &ProtobufReader{}
			require.NoError(t, reader.Read(bytes.NewReader(marshalProto(t, &pb.AnalysisResults{
				Header: &pb.Metadata{Version: pb.SchemaVersion, Repository: "repo"},
				Contents: map[string][]byte{
					"Onboarding": marshalProto(t, &pb.OnboardingResults{
						Authors:    map[int32]*pb.AuthorOnboardingData{0: testCase.author},
						WindowDays: []int32{30},
						TrailDays:  90,
					}),
				},
			}))))

			onboarding, err := reader.GetOnboarding()
			require.NoError(t, err)
			require.Equal(t, testCase.want, onboarding.Authors[0].DaysToFirstMeaningfulCommit)
		})
	}
}

func TestProtobufReader_OnboardingPayloadErrorsAreTyped(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		reader := &ProtobufReader{}
		payload := &pb.AnalysisResults{
			Header:   &pb.Metadata{Version: pb.SchemaVersion, Repository: "repo"},
			Contents: map[string][]byte{},
		}
		require.NoError(t, reader.Read(bytes.NewReader(marshalProto(t, payload))))

		_, err := reader.GetOnboarding()
		require.Error(t, err)
		require.ErrorIs(t, err, ErrAnalysisMissing)
	})

	t.Run("malformed", func(t *testing.T) {
		reader := &ProtobufReader{}
		payload := &pb.AnalysisResults{
			Header: &pb.Metadata{Version: pb.SchemaVersion, Repository: "repo"},
			Contents: map[string][]byte{
				"Onboarding": []byte("not protobuf"),
			},
		}
		require.NoError(t, reader.Read(bytes.NewReader(marshalProto(t, payload))))

		_, err := reader.GetOnboarding()
		require.Error(t, err)
		require.ErrorIs(t, err, ErrAnalysisMalformed)
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
		require.Contains(t, payload.GetContents(), key)
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
	require.Positive(t, fileCooccurrence.NonZeroCount())

	peopleIndex, peopleCooccurrence, err := reader.GetPeopleCooccurrence()
	require.NoError(t, err)
	require.NotEmpty(t, peopleIndex)
	require.Positive(t, peopleCooccurrence.NonZeroCount())

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
	require.Contains(t, payload.GetContents(), "Shotness")

	reader := &ProtobufReader{}
	require.NoError(t, reader.Read(bytes.NewReader(data)))

	records, err := reader.GetShotnessRecords()
	require.NoError(t, err)
	require.NotEmpty(t, records)

	index, cooccurrence, err := reader.GetShotnessCooccurrence()
	require.NoError(t, err)
	require.Len(t, index, len(records))
	require.Equal(t, len(records), cooccurrence.Rows)
	requireShotnessCooccurrenceMatchesAlignedProfileDotProducts(t, records, cooccurrence.Dense())
}

func TestShotnessCooccurrenceUsesAlignedEntityProfilesWithFormatParity(t *testing.T) {
	payload := &pb.AnalysisResults{
		Header: &pb.Metadata{Version: pb.SchemaVersion},
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

	pbIndex, pbCooccurrence, err := reader.GetShotnessCooccurrence()
	require.NoError(t, err)
	require.Equal(t, []string{"a.go:alpha", "b.go:beta", "c.go:gamma"}, pbIndex)

	// Hand calculation over aligned entity dimensions:
	// alpha=(3,0,1), beta=(2,5,0), gamma=(0,4,1).
	// Each cell is the corresponding dot product. The diagonal therefore
	// records squared self-activity: 10, 29, and 17.
	want := [][]int{
		{10, 6, 1},
		{6, 29, 20},
		{1, 20, 17},
	}
	require.Equal(t, want, pbCooccurrence.Dense())

	yamlReader := &YamlReader{}
	require.NoError(t, yamlReader.Read(bytes.NewBufferString(`
hercules:
  version: 2
Shotness:
  - type: function
    name: alpha
    file: a.go
    counters: {0: 3, 2: 1}
  - type: function
    name: beta
    file: b.go
    counters: {0: 2, 1: 5}
  - type: function
    name: gamma
    file: c.go
    counters: {1: 4, 2: 1}
`)))
	yamlIndex, yamlCooccurrence, err := yamlReader.GetShotnessCooccurrence()
	require.NoError(t, err)
	require.Equal(t, pbIndex, yamlIndex)
	require.Equal(t, pbCooccurrence, yamlCooccurrence)
}

func TestShotnessCouplingMatrixValidatesEntityDimensionsAndIdentity(t *testing.T) {
	t.Run("kind disambiguates otherwise equal labels", func(t *testing.T) {
		index, matrix, err := shotnessCouplingMatrix([]ShotnessRecord{
			{
				Type: "function", Name: "run", File: "demo.go",
				Counters: map[int32]int32{0: 1},
			},
			{
				Type: "method", Name: "run", File: "demo.go",
				Counters: map[int32]int32{1: 1},
			},
		})
		require.NoError(t, err)
		require.Equal(t, []string{"demo.go:run [function]", "demo.go:run [method]"}, index)
		require.Equal(t, [][]int{{1, 0}, {0, 1}}, matrix.Dense())
	})

	t.Run("duplicate stable identity", func(t *testing.T) {
		_, _, err := shotnessCouplingMatrix([]ShotnessRecord{
			{Type: "function", Name: "run", File: "demo.go"},
			{Type: "function", Name: "run", File: "demo.go"},
		})
		require.ErrorContains(t, err, "duplicate shotness entity identity")
	})

	for _, dimension := range []int32{-1, 2} {
		t.Run("out-of-range counter dimension", func(t *testing.T) {
			_, _, err := shotnessCouplingMatrix([]ShotnessRecord{
				{
					Type: "function", Name: "alpha", File: "a.go",
					Counters: map[int32]int32{dimension: 1},
				},
				{Type: "function", Name: "beta", File: "b.go"},
			})
			require.ErrorContains(t, err, "out-of-range counter dimension")
		})
	}

	t.Run("negative counter", func(t *testing.T) {
		_, _, err := shotnessCouplingMatrix([]ShotnessRecord{
			{
				Type: "function", Name: "alpha", File: "a.go",
				Counters: map[int32]int32{0: -1},
			},
		})
		require.ErrorContains(t, err, "negative co-occurrence")
	})

	t.Run("dot product overflow", func(t *testing.T) {
		const maxInt32 = int32(1<<31 - 1)
		profile := map[int32]int32{0: maxInt32, 1: maxInt32, 2: maxInt32}
		_, _, err := shotnessCouplingMatrix([]ShotnessRecord{
			{Type: "function", Name: "alpha", File: "a.go", Counters: profile},
			{Type: "function", Name: "beta", File: "b.go", Counters: profile},
			{Type: "function", Name: "gamma", File: "c.go"},
		})
		require.ErrorContains(t, err, "overflows int")
	})

	t.Run("pair total overflow", func(t *testing.T) {
		if strconv.IntSize < 64 {
			t.Skip("individual dot products overflow int before their total on 32-bit systems")
		}

		const maxInt32 = int32(1<<31 - 1)
		profile := map[int32]int32{0: maxInt32}
		_, _, err := shotnessCouplingMatrix([]ShotnessRecord{
			{Type: "function", Name: "alpha", File: "a.go", Counters: profile},
			{Type: "function", Name: "beta", File: "b.go", Counters: profile},
			{Type: "function", Name: "gamma", File: "c.go", Counters: profile},
		})
		require.ErrorContains(t, err, "total shotness coupling score overflows int")
	})
}

func TestShotnessReadersAgreeOnPresentEmptyPayload(t *testing.T) {
	pbReader := &ProtobufReader{}
	require.NoError(t, pbReader.Read(bytes.NewReader(marshalProto(t, &pb.AnalysisResults{
		Header: &pb.Metadata{Version: pb.SchemaVersion},
		Contents: map[string][]byte{
			"Shotness": marshalProto(t, &pb.ShotnessAnalysisResults{}),
		},
	}))))
	pbIndex, pbMatrix, err := pbReader.GetShotnessCooccurrence()
	require.NoError(t, err)

	yamlReader := &YamlReader{}
	require.NoError(t, yamlReader.Read(bytes.NewBufferString(
		"hercules:\n  version: 2\nShotness: []\n",
	)))
	yamlIndex, yamlMatrix, err := yamlReader.GetShotnessCooccurrence()
	require.NoError(t, err)

	require.Equal(t, pbIndex, yamlIndex)
	require.Equal(t, pbMatrix, yamlMatrix)
	require.Empty(t, pbIndex)
	require.Zero(t, pbMatrix.Rows)
}

func marshalProto(t *testing.T, message proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(message)
	require.NoError(t, err)
	return data
}

func requireShotnessCooccurrenceMatchesAlignedProfileDotProducts(
	t *testing.T, records []ShotnessRecord, matrix [][]int,
) {
	t.Helper()
	for i, record := range records {
		require.Len(t, matrix[i], len(records))
		for j, other := range records {
			expected := 0
			for tick, count := range record.Counters {
				expected += int(count) * int(other.Counters[tick])
			}
			require.Equalf(t, expected, matrix[i][j], "cooccurrence[%d][%d]", i, j)
		}
	}
}

// TestProtobufReader_RefactoringProxyTimestampsUseTheExactTickGrid pins the two
// halves of the tick-timestamp reconstruction.
//
// The analysis stores tick indices, so the reader has to rebuild the instants.
// A day-truncated step collapses every sub-day tick size to one day, and
// anchoring on the header's begin time puts every tick at the first commit's
// time of day - which is enough to drop the last tick from a date range that
// ends at midnight.
func TestProtobufReader_RefactoringProxyTimestampsUseTheExactTickGrid(t *testing.T) {
	const halfDay = int64(12 * time.Hour)

	// 2023-11-14T22:13:20Z: deliberately not on a tick boundary.
	const begin = int64(1700000000)

	reader := &ProtobufReader{}
	payload := &pb.AnalysisResults{
		Header: &pb.Metadata{Version: pb.SchemaVersion, Repository: "repo", BeginUnixTime: begin},
		Contents: map[string][]byte{
			"RefactoringProxy": marshalProto(t, &pb.RefactoringProxyResults{
				Ticks:         []int32{0, 1, 2},
				RenameRatios:  []float32{0.1, 0.2, 0.3},
				IsRefactoring: []bool{false, false, true},
				TotalChanges:  []int32{1, 2, 3},
				Threshold:     0.3,
				TickSize:      halfDay,
			}),
		},
	}
	require.NoError(t, reader.Read(bytes.NewReader(marshalProto(t, payload))))

	data, err := reader.GetRefactoringProxy()
	require.NoError(t, err)
	require.Len(t, data.Ticks, 3)

	// A half-day tick must advance by half a day, not by the day tickSizeDays
	// rounds it up to.
	require.Equal(t, halfDay/int64(time.Second), data.Ticks[1].Timestamp-data.Ticks[0].Timestamp)
	require.Equal(t, halfDay/int64(time.Second), data.Ticks[2].Timestamp-data.Ticks[1].Timestamp)

	// Tick 0 sits on the floored boundary at or before the first commit, so it
	// lands on a whole multiple of the tick size rather than at 22:13:20.
	first := data.Ticks[0].Timestamp
	require.LessOrEqual(t, first, begin)
	require.Greater(t, first, begin-halfDay/int64(time.Second))
	require.Equal(t, tickgrid.FloorTime(time.Unix(begin, 0).UTC(), time.Duration(halfDay)).Unix(), first)
}
