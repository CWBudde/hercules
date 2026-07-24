package readers

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/pb"
)

func TestProtobufReaderGetLanguageStatsFromDevsTicks(t *testing.T) {
	reader := readProtobufAnalysis(t, &pb.AnalysisResults{
		Contents: map[string][]byte{
			"Devs": marshalLanguageStatsProto(t, &pb.DevsAnalysisResults{
				DevIndex: []string{"alice", "bob"},
				Ticks: map[int32]*pb.TickDevs{
					0: {
						Devs: map[int32]*pb.DevTick{
							0: {
								Stats: &pb.LineStats{Added: 10, Removed: 2, Changed: 3},
								Languages: map[string]*pb.LineStats{
									"Go":       {Added: 10, Removed: 2, Changed: 3},
									"Markdown": {Added: 4, Removed: 1, Changed: 0},
								},
							},
							1: {
								Stats: &pb.LineStats{Added: 5, Removed: 1, Changed: 1},
								Languages: map[string]*pb.LineStats{
									"Go": {Added: 5, Removed: 1, Changed: 1},
								},
							},
						},
					},
					1: {
						Devs: map[int32]*pb.DevTick{
							0: {
								Stats: &pb.LineStats{Added: 3, Removed: 0, Changed: 2},
								Languages: map[string]*pb.LineStats{
									"Go": {Added: 3, Removed: 0, Changed: 2},
									"":   {Added: 99},
								},
							},
						},
					},
				},
			}),
		},
	})

	stats, err := reader.GetLanguageStats()
	if err != nil {
		t.Fatalf("GetLanguageStats() unexpected error: %v", err)
	}

	if got := languageLines(stats, "Go"); got != 27 {
		t.Fatalf("Go lines = %d, want 27", got)
	}
	if got := languageLines(stats, "Markdown"); got != 5 {
		t.Fatalf("Markdown lines = %d, want 5", got)
	}
	if got := languageLines(stats, ""); got != 0 {
		t.Fatalf("empty language should be ignored, got %d", got)
	}
}

func TestYamlReaderGetLanguageStatsFromCompactDevsTicks(t *testing.T) {
	path := filepath.Join("..", "testdata", "labours-go_devs.yaml")
	file, err := os.Open(path) // #nosec G304 - test fixture path is fixed above.
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = file.Close() }()

	reader := &YamlReader{}
	if err := reader.Read(file); err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}

	stats, err := reader.GetLanguageStats()
	if err != nil {
		t.Fatalf("GetLanguageStats() unexpected error: %v", err)
	}

	if got := languageLines(stats, "Go"); got != 12529 {
		t.Fatalf("Go lines = %d, want 12529", got)
	}
	if got := languageLines(stats, "Markdown"); got != 2545 {
		t.Fatalf("Markdown lines = %d, want 2545", got)
	}
}

func TestProtobufReaderGetLanguageStatsFromRealHerculesFixture(t *testing.T) {
	path := filepath.Join("..", "testdata", "hercules", "report_default.pb")
	file, err := os.Open(path) // #nosec G304 - test fixture path is fixed above.
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = file.Close() }()

	reader := &ProtobufReader{}
	if err := reader.Read(file); err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}

	stats, err := reader.GetLanguageStats()
	if err != nil {
		t.Fatalf("GetLanguageStats() unexpected error: %v", err)
	}
	if len(stats) == 0 {
		t.Fatal("expected language stats from real Hercules fixture")
	}
	if stats[0].Language == "" || stats[0].Lines <= 0 {
		t.Fatalf("unexpected top language stat: %#v", stats[0])
	}
}

func readProtobufAnalysis(t *testing.T, results *pb.AnalysisResults) *ProtobufReader {
	t.Helper()

	reader := &ProtobufReader{}
	if err := reader.Read(bytes.NewReader(marshalLanguageStatsProto(t, results))); err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}
	return reader
}

func marshalLanguageStatsProto(t *testing.T, message proto.Message) []byte {
	t.Helper()

	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatalf("proto.Marshal() unexpected error: %v", err)
	}
	return data
}

func languageLines(stats []LanguageStat, language string) int {
	for _, stat := range stats {
		if stat.Language == language {
			return stat.Lines
		}
	}
	return 0
}
