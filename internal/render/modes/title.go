package modes

import (
	"fmt"
	"strings"
)

// repositoryJoiner is how `hercules combine` labels a merged analysis: it joins
// every input repository with " & " (cmd/hercules/combine.go).
const repositoryJoiner = " & "

// titleRepositoryName shortens the repository label that chart titles are built
// from. A 36-repository combined run produced a title several times wider than
// the canvas, spilling off both edges and pushing the plot itself out of view.
//
// A single repository - the Python-parity case, and every per-repository chart -
// passes through untouched.
func titleRepositoryName(name string) string {
	parts := strings.Split(name, repositoryJoiner)
	if len(parts) < 2 {
		return name
	}

	noun := "repositories"
	if len(parts) == 2 {
		noun = "repository"
	}

	return fmt.Sprintf("%s & %d more %s", parts[0], len(parts)-1, noun)
}
