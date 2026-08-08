package render

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cwbudde/hercules/internal/render/readers"
)

// errJSONOutputUnsupported reports a mode that has no JSON extractor.
var errJSONOutputUnsupported = errors.New("JSON output is not implemented")

// Keys and tag values of the JSON payload written when --output ends in
// ".json". They are the JSON wire contract and are deliberately kept separate
// from the render mode names in modenames.go: jsonTypeBurndown happens to
// spell the same as ModeBurndown, but it tags the payload family shared by the
// four burndown extractors below, not the mode that produced it.
const (
	jsonKeyType           = "type"
	jsonKeyTarget         = "target"
	jsonKeyCouplingMatrix = "coupling_matrix"

	jsonTypeBurndown = "burndown"
)

type jsonModeExtractor func(readers.Reader) (any, error)

var jsonModeExtractors = map[string]jsonModeExtractor{
	ModeDevs:                   extractDeveloperStatsJSON,
	ModeDevsEfforts:            extractDeveloperTimeSeriesJSON,
	ModeOldVsNew:               extractDeveloperTimeSeriesJSON,
	ModeDevsParallel:           extractDeveloperTimeSeriesJSON,
	ModeBurndownProject:        extractProjectBurndownJSON,
	ModeBurndownFile:           extractFileBurndownJSON,
	ModeBurndownPerson:         extractPeopleBurndownJSON,
	ModeBurndownRepository:     extractRepositoryBurndownJSON,
	ModeBurndownReposCombined:  extractRepositoryBurndownJSON,
	ModeOwnership:              extractOwnershipJSON,
	ModeOverwritesMatrix:       extractOverwritesJSON,
	ModeCouplesFiles:           extractFileCouplingJSON,
	ModeCouplesPeople:          extractPeopleCouplingJSON,
	ModeCouplesShotness:        extractShotnessCouplingJSON,
	ModeShotness:               extractShotnessJSON,
	ModeRunTimes:               extractRuntimeStatsJSON,
	ModeLanguages:              extractLanguageStatsJSON,
	ModeSentiment:              extractSentimentJSON,
	ModeTemporalActivity:       extractTemporalActivityJSON,
	ModeBusFactor:              extractBusFactorJSON,
	ModeOwnershipConcentration: extractOwnershipConcentrationJSON,
	ModeKnowledgeDiffusion:     extractKnowledgeDiffusionJSON,
	ModeHotspotRisk:            extractHotspotRiskJSON,
	ModeRefactoringProxy:       extractRefactoringProxyJSON,
}

// extractModeDataForJSON extracts raw reader data for JSON output without rendering plots.
func extractModeDataForJSON(reader readers.Reader, mode string) (any, error) {
	extract, ok := jsonModeExtractors[mode]
	if !ok {
		return nil, fmt.Errorf("%w for mode %s", errJSONOutputUnsupported, mode)
	}

	return extract(reader)
}

func extractDeveloperStatsJSON(reader readers.Reader) (any, error) {
	stats, err := reader.GetDeveloperStats()
	return jsonField("developer_stats", stats, err)
}

func extractDeveloperTimeSeriesJSON(reader readers.Reader) (any, error) {
	data, err := reader.GetDeveloperTimeSeriesData()
	return jsonField("developer_time_series", data, err)
}

func extractProjectBurndownJSON(reader readers.Reader) (any, error) {
	header, name, matrix, err := reader.GetProjectBurndownWithHeader()
	if err != nil {
		return nil, err
	}

	return map[string]any{
		jsonKeyType: jsonTypeBurndown, jsonKeyTarget: "project",
		"header": header, "name": name, "matrix": matrix,
	}, nil
}

func extractFileBurndownJSON(reader readers.Reader) (any, error) {
	files, err := reader.GetFilesBurndown()
	if err != nil {
		return nil, err
	}

	return map[string]any{jsonKeyType: jsonTypeBurndown, jsonKeyTarget: "file", "files": files}, nil
}

func extractPeopleBurndownJSON(reader readers.Reader) (any, error) {
	people, err := reader.GetPeopleBurndown()
	if err != nil {
		return nil, err
	}

	return map[string]any{jsonKeyType: jsonTypeBurndown, jsonKeyTarget: "person", "people": people}, nil
}

func extractRepositoryBurndownJSON(reader readers.Reader) (any, error) {
	repositoryReader, ok := reader.(readers.RepositoryBurndownReader)
	if !ok {
		return nil, fmt.Errorf("%w: repository burndown", readers.ErrAnalysisMissing)
	}

	repositories, err := repositoryReader.GetRepositoriesBurndown()
	if err != nil {
		return nil, err
	}

	return map[string]any{
		jsonKeyType: jsonTypeBurndown, jsonKeyTarget: "repository", "repositories": repositories,
	}, nil
}

func extractOwnershipJSON(reader readers.Reader) (any, error) {
	names, matrices, err := reader.GetOwnershipBurndown()
	if err != nil {
		return nil, err
	}

	return map[string]any{jsonKeyType: "ownership", "file_names": names, "matrices": matrices}, nil
}

func extractOverwritesJSON(reader readers.Reader) (any, error) {
	people, matrix, err := reader.GetPeopleInteraction()
	if err != nil {
		return nil, err
	}

	return map[string]any{jsonKeyType: "overwrites_matrix", "people": people, "matrix": matrix}, nil
}

func extractFileCouplingJSON(reader readers.Reader) (any, error) {
	names, matrix, err := reader.GetFileCooccurrence()
	if err != nil {
		return nil, err
	}

	return map[string]any{"file_names": names, jsonKeyCouplingMatrix: matrix}, nil
}

func extractPeopleCouplingJSON(reader readers.Reader) (any, error) {
	names, matrix, err := reader.GetPeopleCooccurrence()
	if err != nil {
		return nil, err
	}

	return map[string]any{"people_names": names, jsonKeyCouplingMatrix: matrix}, nil
}

func extractShotnessCouplingJSON(reader readers.Reader) (any, error) {
	names, matrix, err := reader.GetShotnessCooccurrence()
	if err != nil {
		return nil, err
	}

	return map[string]any{"entity_names": names, jsonKeyCouplingMatrix: matrix}, nil
}

func extractShotnessJSON(reader readers.Reader) (any, error) {
	records, err := reader.GetShotnessRecords()
	return jsonField("shotness_records", records, err)
}

func extractRuntimeStatsJSON(reader readers.Reader) (any, error) {
	stats, err := reader.GetRuntimeStats()
	return jsonField("runtime_stats", stats, err)
}

func extractLanguageStatsJSON(reader readers.Reader) (any, error) {
	stats, err := reader.GetLanguageStats()
	return jsonField("language_stats", stats, err)
}

func extractSentimentJSON(reader readers.Reader) (any, error) {
	sentimentReader, ok := reader.(readers.SentimentReader)
	if !ok {
		return nil, fmt.Errorf("%w: sentiment", readers.ErrAnalysisMissing)
	}

	data, err := sentimentReader.GetSentimentByTick()

	return jsonField("sentiment_by_tick", data, err)
}

func extractTemporalActivityJSON(reader readers.Reader) (any, error) {
	temporalReader, ok := reader.(readers.TemporalActivityReader)
	if !ok {
		return nil, fmt.Errorf("%w: temporal activity", readers.ErrAnalysisMissing)
	}

	data, err := temporalReader.GetTemporalActivity()

	return jsonField("temporal_activity", data, err)
}

func extractBusFactorJSON(reader readers.Reader) (any, error) {
	busFactorReader, ok := reader.(readers.BusFactorReader)
	if !ok {
		return nil, fmt.Errorf("%w: bus factor", readers.ErrAnalysisMissing)
	}

	data, err := busFactorReader.GetBusFactor()

	return jsonField("bus_factor", data, err)
}

func extractOwnershipConcentrationJSON(reader readers.Reader) (any, error) {
	ownershipReader, ok := reader.(readers.OwnershipConcentrationReader)
	if !ok {
		return nil, fmt.Errorf("%w: ownership concentration", readers.ErrAnalysisMissing)
	}

	data, err := ownershipReader.GetOwnershipConcentration()

	return jsonField("ownership_concentration", data, err)
}

func extractKnowledgeDiffusionJSON(reader readers.Reader) (any, error) {
	diffusionReader, ok := reader.(readers.KnowledgeDiffusionReader)
	if !ok {
		return nil, fmt.Errorf("%w: knowledge diffusion", readers.ErrAnalysisMissing)
	}

	data, err := diffusionReader.GetKnowledgeDiffusion()

	return jsonField("knowledge_diffusion", data, err)
}

func extractHotspotRiskJSON(reader readers.Reader) (any, error) {
	hotspotReader, ok := reader.(readers.HotspotRiskReader)
	if !ok {
		return nil, fmt.Errorf("%w: hotspot risk", readers.ErrAnalysisMissing)
	}

	data, err := hotspotReader.GetHotspotRisk()

	return jsonField("hotspot_risk", data, err)
}

func extractRefactoringProxyJSON(reader readers.Reader) (any, error) {
	refactoringReader, ok := reader.(readers.RefactoringProxyReader)
	if !ok {
		return nil, fmt.Errorf("%w: refactoring proxy", readers.ErrAnalysisMissing)
	}

	data, err := refactoringReader.GetRefactoringProxy()

	return jsonField("refactoring_proxy", data, err)
}

func jsonField(key string, value any, err error) (any, error) {
	if err != nil {
		return nil, err
	}

	return map[string]any{key: value}, nil
}

// saveJSONResults saves the analysis results as JSON.
func saveJSONResults(results map[string]any, outputPath string) error {
	file, err := os.Create(outputPath) // #nosec G304 - JSON output path is explicitly requested by caller.
	if err != nil {
		return fmt.Errorf("failed to create JSON output file: %w", err)
	}

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ") // Pretty print

	// Add metadata
	output := map[string]any{
		"meta": map[string]any{
			"generated_by":   "labours-go",
			"generated_at":   time.Now().Format(time.RFC3339),
			"modes_executed": len(results),
		},
		"results": results,
	}

	err = encoder.Encode(output)
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("encode JSON output: %w", err)
	}

	err = file.Close()
	if err != nil {
		return fmt.Errorf("close JSON output: %w", err)
	}

	return nil
}
