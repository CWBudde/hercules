package modes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/hercules/internal/render/graphics"
)

const multiAddressIdentity = "vadim markovtsev|gmarkhor@gmail.com|vadim@athenian.co"

func TestPeopleChartLabelUsesSingleCanonicalName(t *testing.T) {
	tests := []struct {
		name     string
		identity string
		want     string
	}{
		{
			name:     "detected multi-address identity",
			identity: multiAddressIdentity,
			want:     "vadim markovtsev",
		},
		{
			name:     "multiple name aliases",
			identity: "Primary Name|Alternate Name|primary@example.com",
			want:     "Primary Name",
		},
		{
			name:     "exact signature",
			identity: "Exact Name <exact@example.com>",
			want:     "Exact Name",
		},
		{
			name:     "anonymized identity",
			identity: "Author   7",
			want:     "Author   7",
		},
		{
			name:     "email only",
			identity: "private@example.com",
			want:     unknownContributorLabel,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := peopleChartLabel(test.identity)
			if got != test.want {
				t.Fatalf("peopleChartLabel(%q) = %q, want %q", test.identity, got, test.want)
			}
			if strings.Contains(got, "@") {
				t.Fatalf("public chart label %q contains an email marker", got)
			}
		})
	}
}

func TestDevsEffortsRenderingDoesNotExposeIdentityEmails(t *testing.T) {
	output := filepath.Join(t.TempDir(), "devs-efforts.svg")
	data := devEffortsMatrix{
		Dates: []time.Time{
			time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		},
		Names: []string{
			multiAddressIdentity,
		},
		CumLayers: [][]float64{{10, 20}},
	}

	err := plotDevEffortsTimeSeries(data, output, graphics.DefaultOptions())
	if err != nil {
		t.Fatalf("render devs-efforts chart: %v", err)
	}
	content, err := os.ReadFile(output) // #nosec G304 - test path is under t.TempDir.
	if err != nil {
		t.Fatalf("read devs-efforts SVG: %v", err)
	}
	svg := string(content)
	if !strings.Contains(svg, ">vadim markovtsev</text>") {
		t.Fatalf("rendered SVG does not contain the canonical name")
	}
	if strings.Contains(svg, "@") {
		t.Fatalf("rendered SVG exposes an identity email")
	}
}

func TestPeopleJSONRetainsFullIdentity(t *testing.T) {
	output := filepath.Join(t.TempDir(), "overwrites.json")
	people, _, matrix := processOverwritesMatrix(
		[]string{multiAddressIdentity},
		[][]int{{10, -5, -5}},
		1,
		true,
	)
	if len(people) != 1 || people[0] != multiAddressIdentity {
		t.Fatalf("processed identity = %q, want full identity %q", people, multiAddressIdentity)
	}

	err := saveMatrixAsJSON(output, people, matrix)
	if err != nil {
		t.Fatalf("write overwrites JSON: %v", err)
	}
	content, err := os.ReadFile(output) // #nosec G304 - test path is under t.TempDir.
	if err != nil {
		t.Fatalf("read overwrites JSON: %v", err)
	}
	if !strings.Contains(string(content), multiAddressIdentity) {
		t.Fatalf("JSON output does not retain the full identity")
	}
}
