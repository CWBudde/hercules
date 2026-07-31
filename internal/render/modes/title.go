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
// Naming the first repository and counting the rest ("../a & 35 more
// repositories") only reads as meaningful if that first one is special, and it
// is not - it is whichever path happened to be listed first. The count alone
// says what the chart actually covers.
//
// A single repository - the Python-parity case, and every per-repository chart -
// passes through untouched.
func titleRepositoryName(name string) string {
	parts := strings.Split(name, repositoryJoiner)
	if len(parts) < 2 {
		return name
	}

	return fmt.Sprintf("%d combined repositories", len(parts))
}
