package readers

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

type fixtureExtractionSummary struct {
	Repository             string            `json:"repository"`
	BeginUnixTime          int64             `json:"beginUnixTime"`
	EndUnixTime            int64             `json:"endUnixTime"`
	ProjectBurndown        matrixSummary     `json:"projectBurndown"`
	FilesBurndown          collectionSummary `json:"filesBurndown"`
	PeopleBurndown         collectionSummary `json:"peopleBurndown"`
	PeopleInteraction      matrixSummary     `json:"peopleInteraction"`
	FileCooccurrence       indexedMatrix     `json:"fileCooccurrence"`
	PeopleCooccurrence     indexedMatrix     `json:"peopleCooccurrence"`
	Developers             developersSummary `json:"developers"`
	TemporalActivity       temporalSummary   `json:"temporalActivity"`
	BusFactor              busFactorSummary  `json:"busFactor"`
	OwnershipConcentration ownershipSummary  `json:"ownershipConcentration"`
	KnowledgeDiffusion     diffusionSummary  `json:"knowledgeDiffusion"`
	HotspotRisk            hotspotSummary    `json:"hotspotRisk"`
}

type shotnessExtractionSummary struct {
	Records      int           `json:"records"`
	CounterTotal int           `json:"counterTotal"`
	Cooccurrence indexedMatrix `json:"cooccurrence"`
	FirstRecord  string        `json:"firstRecord"`
}

type matrixSummary struct {
	Rows  int `json:"rows"`
	Cols  int `json:"cols"`
	Total int `json:"total"`
}

type indexedMatrix struct {
	Items  int           `json:"items"`
	Matrix matrixSummary `json:"matrix"`
}

type collectionSummary struct {
	Count       int           `json:"count"`
	FirstName   string        `json:"firstName"`
	FirstMatrix matrixSummary `json:"firstMatrix"`
}

type developersSummary struct {
	People        int            `json:"people"`
	Days          int            `json:"days"`
	Commits       int            `json:"commits"`
	LinesAdded    int            `json:"linesAdded"`
	LinesRemoved  int            `json:"linesRemoved"`
	LinesModified int            `json:"linesModified"`
	Languages     map[string]int `json:"languages"`
}

type temporalSummary struct {
	People  int `json:"people"`
	Ticks   int `json:"ticks"`
	Commits int `json:"commits"`
	Lines   int `json:"lines"`
}

type busFactorSummary struct {
	Snapshots  int     `json:"snapshots"`
	Subsystems int     `json:"subsystems"`
	Threshold  float32 `json:"threshold"`
	MaxFactor  int     `json:"maxFactor"`
	MaxLines   int64   `json:"maxLines"`
}

type ownershipSummary struct {
	Snapshots     int     `json:"snapshots"`
	SubsystemGini int     `json:"subsystemGini"`
	SubsystemHHI  int     `json:"subsystemHhi"`
	MaxGini       float64 `json:"maxGini"`
	MaxHHI        float64 `json:"maxHhi"`
	MaxTotalLines int64   `json:"maxTotalLines"`
}

type diffusionSummary struct {
	Files        int `json:"files"`
	Distribution int `json:"distribution"`
	WindowMonths int `json:"windowMonths"`
	MaxEditors   int `json:"maxEditors"`
}

type hotspotSummary struct {
	Files      int     `json:"files"`
	WindowDays int     `json:"windowDays"`
	TopPath    string  `json:"topPath"`
	TopRisk    float64 `json:"topRisk"`
}

func TestProtobufReader_ReportDefaultExtractionGolden(t *testing.T) {
	reader := readProtobufFixture(t, "report_default.pb")
	actual := summarizeReportDefaultExtraction(t, reader)
	requireGoldenJSON(t, "report_default_summary.golden.json", actual)
}

func TestProtobufReader_ShotnessExtractionGolden(t *testing.T) {
	reader := readProtobufFixture(t, "shotness.pb")
	actual := summarizeShotnessExtraction(t, reader)
	requireGoldenJSON(t, "shotness_summary.golden.json", actual)
}

func readProtobufFixture(t *testing.T, name string) *ProtobufReader {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("../testdata/hercules", name)) // #nosec G304 - test fixture directory is fixed.
	require.NoError(t, err)

	reader := &ProtobufReader{}
	require.NoError(t, reader.Read(bytes.NewReader(data)))
	return reader
}

func summarizeReportDefaultExtraction(t *testing.T, reader *ProtobufReader) fixtureExtractionSummary {
	t.Helper()

	begin, end := reader.GetHeader()
	_, _, projectBurndown, err := reader.GetProjectBurndownWithHeader()
	require.NoError(t, err)

	files, err := reader.GetFilesBurndown()
	require.NoError(t, err)

	people, err := reader.GetPeopleBurndown()
	require.NoError(t, err)

	_, peopleInteraction, err := reader.GetPeopleInteraction()
	require.NoError(t, err)

	fileIndex, fileCooccurrence, err := reader.GetFileCooccurrence()
	require.NoError(t, err)

	peopleIndex, peopleCooccurrence, err := reader.GetPeopleCooccurrence()
	require.NoError(t, err)

	devs, err := reader.GetDeveloperTimeSeriesData()
	require.NoError(t, err)

	temporal, err := reader.GetTemporalActivity()
	require.NoError(t, err)

	busFactor, err := reader.GetBusFactor()
	require.NoError(t, err)

	ownership, err := reader.GetOwnershipConcentration()
	require.NoError(t, err)

	diffusion, err := reader.GetKnowledgeDiffusion()
	require.NoError(t, err)

	hotspot, err := reader.GetHotspotRisk()
	require.NoError(t, err)

	return fixtureExtractionSummary{
		Repository:        reader.GetName(),
		BeginUnixTime:     begin,
		EndUnixTime:       end,
		ProjectBurndown:   summarizeMatrix(projectBurndown),
		FilesBurndown:     summarizeFiles(files),
		PeopleBurndown:    summarizePeople(people),
		PeopleInteraction: summarizeMatrix(peopleInteraction),
		FileCooccurrence: indexedMatrix{
			Items:  len(fileIndex),
			Matrix: summarizeMatrix(fileCooccurrence.Dense()),
		},
		PeopleCooccurrence: indexedMatrix{
			Items:  len(peopleIndex),
			Matrix: summarizeMatrix(peopleCooccurrence.Dense()),
		},
		Developers:             summarizeDevelopers(devs),
		TemporalActivity:       summarizeTemporal(temporal),
		BusFactor:              summarizeBusFactor(busFactor),
		OwnershipConcentration: summarizeOwnership(ownership),
		KnowledgeDiffusion:     summarizeDiffusion(diffusion),
		HotspotRisk:            summarizeHotspot(hotspot),
	}
}

func summarizeShotnessExtraction(t *testing.T, reader *ProtobufReader) shotnessExtractionSummary {
	t.Helper()

	records, err := reader.GetShotnessRecords()
	require.NoError(t, err)

	index, cooccurrence, err := reader.GetShotnessCooccurrence()
	require.NoError(t, err)

	counterTotal := 0
	for _, record := range records {
		for _, count := range record.Counters {
			counterTotal += int(count)
		}
	}

	first := ""
	if len(records) > 0 {
		first = records[0].File + ":" + records[0].Name
	}

	return shotnessExtractionSummary{
		Records:      len(records),
		CounterTotal: counterTotal,
		Cooccurrence: indexedMatrix{
			Items:  len(index),
			Matrix: summarizeMatrix(cooccurrence.Dense()),
		},
		FirstRecord: first,
	}
}

func requireGoldenJSON(t *testing.T, name string, actual any) {
	t.Helper()

	actualJSON, err := json.MarshalIndent(actual, "", "  ")
	require.NoError(t, err)
	actualJSON = append(actualJSON, '\n')

	path := filepath.Join("../testdata/hercules", name)
	if os.Getenv("LABOURS_GO_UPDATE_GOLDENS") == "1" {
		require.NoError(t, os.WriteFile(path, actualJSON, 0o600))
	}

	expectedJSON, err := os.ReadFile(path) // #nosec G304 - golden path is built by this test helper.
	require.NoError(t, err)
	require.JSONEq(t, string(expectedJSON), string(actualJSON))
}

func summarizeFiles(files []FileBurndown) collectionSummary {
	return collectionSummary{
		Count:       len(files),
		FirstName:   files[0].Filename,
		FirstMatrix: summarizeMatrix(files[0].Matrix),
	}
}

func summarizePeople(people []PeopleBurndown) collectionSummary {
	return collectionSummary{
		Count:       len(people),
		FirstName:   people[0].Person,
		FirstMatrix: summarizeMatrix(people[0].Matrix),
	}
}

func summarizeMatrix(matrix [][]int) matrixSummary {
	return matrixSummary{
		Rows:  len(matrix),
		Cols:  matrixCols(matrix),
		Total: matrixTotal(matrix),
	}
}

func matrixCols(matrix [][]int) int {
	if len(matrix) == 0 {
		return 0
	}
	return len(matrix[0])
}

func matrixTotal(matrix [][]int) int {
	total := 0
	for _, row := range matrix {
		for _, value := range row {
			total += value
		}
	}
	return total
}

func summarizeDevelopers(devs *DeveloperTimeSeriesData) developersSummary {
	summary := developersSummary{
		People:    len(devs.People),
		Days:      len(devs.Days),
		Languages: map[string]int{},
	}
	for _, day := range devs.Days {
		for _, stats := range day {
			summary.Commits += stats.Commits
			summary.LinesAdded += stats.LinesAdded
			summary.LinesRemoved += stats.LinesRemoved
			summary.LinesModified += stats.LinesModified
			for language, values := range stats.Languages {
				for _, value := range values {
					summary.Languages[language] += value
				}
			}
		}
	}
	return summary
}

func summarizeTemporal(temporal *TemporalActivityData) temporalSummary {
	summary := temporalSummary{
		People: len(temporal.People),
		Ticks:  len(temporal.Ticks),
	}
	for _, tick := range temporal.Ticks {
		for _, activity := range tick {
			summary.Commits += activity.Commits
			summary.Lines += activity.Lines
		}
	}
	return summary
}

func summarizeBusFactor(data *BusFactorData) busFactorSummary {
	summary := busFactorSummary{
		Snapshots:  len(data.Snapshots),
		Subsystems: len(data.SubsystemBusFactor),
		Threshold:  data.Threshold,
	}
	for _, snapshot := range data.Snapshots {
		if snapshot.BusFactor > summary.MaxFactor {
			summary.MaxFactor = snapshot.BusFactor
		}
		if snapshot.TotalLines > summary.MaxLines {
			summary.MaxLines = snapshot.TotalLines
		}
	}
	return summary
}

func summarizeOwnership(data *OwnershipConcentrationData) ownershipSummary {
	summary := ownershipSummary{
		Snapshots:     len(data.Snapshots),
		SubsystemGini: len(data.SubsystemGini),
		SubsystemHHI:  len(data.SubsystemHHI),
	}
	for _, snapshot := range data.Snapshots {
		if snapshot.Gini > summary.MaxGini {
			summary.MaxGini = snapshot.Gini
		}
		if snapshot.HHI > summary.MaxHHI {
			summary.MaxHHI = snapshot.HHI
		}
		if snapshot.TotalLines > summary.MaxTotalLines {
			summary.MaxTotalLines = snapshot.TotalLines
		}
	}
	return summary
}

func summarizeDiffusion(data *KnowledgeDiffusionData) diffusionSummary {
	summary := diffusionSummary{
		Files:        len(data.Files),
		Distribution: len(data.Distribution),
		WindowMonths: data.WindowMonths,
	}
	for _, file := range data.Files {
		if file.UniqueEditors > summary.MaxEditors {
			summary.MaxEditors = file.UniqueEditors
		}
	}
	return summary
}

func summarizeHotspot(data *HotspotRiskData) hotspotSummary {
	summary := hotspotSummary{
		Files:      len(data.Files),
		WindowDays: data.WindowDays,
	}
	files := append([]HotspotRiskFile(nil), data.Files...)
	sort.Slice(files, func(i, j int) bool {
		if files[i].RiskScore == files[j].RiskScore {
			return files[i].Path < files[j].Path
		}
		return files[i].RiskScore > files[j].RiskScore
	})
	if len(files) > 0 {
		summary.TopPath = files[0].Path
		summary.TopRisk = files[0].RiskScore
	}
	return summary
}
