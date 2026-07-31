package yaml

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// SafeString returns a string which is sufficiently quoted and escaped for YAML.
func SafeString(str string) string {
	str = strings.ReplaceAll(str, "\\", "\\\\")
	str = strings.ReplaceAll(str, "\"", "\\\"")

	return "\"" + str + "\""
}

// PrintMatrix outputs a rectangular integer matrix in YAML text format.
//
// `indent` is the current YAML indentation level - the number of spaces.
// `name` is the name of the corresponding YAML block. If empty, no separate block is created.
// `fixNegative` changes all negative values to 0. It is retained only as a compatibility
// safeguard for legacy callers; analyses should validate their matrices and report invalid
// internal state instead of relying on output-time correction.
func PrintMatrix(writer io.Writer, matrix [][]int64, indent int, name string, fixNegative bool) {
	// An empty matrix has no rows to derive the column count from. Emit just the block
	// header (when named) so the YAML key stays present with an empty body.
	if len(matrix) == 0 {
		if name != "" {
			_, _ = fmt.Fprintf(writer, "%s%s: |-\n", strings.Repeat(" ", indent), SafeString(name))
		}

		return
	}

	width := matrixColumnWidth(matrix, fixNegative)
	last := len(matrix[len(matrix)-1])

	if name != "" {
		// PrintMatrix predates error-returning serializers; the explicit blank
		// assignment keeps the unavoidable writer limitation visible.
		_, _ = fmt.Fprintf(writer, "%s%s: |-\n", strings.Repeat(" ", indent), SafeString(name))
		indent += 2
	}

	printMatrixRows(writer, matrix, indent, last, width, fixNegative)
}

func matrixColumnWidth(matrix [][]int64, fixNegative bool) int {
	maxnum, minnum := matrixValueBounds(matrix)
	width := len(strconv.FormatInt(maxnum, 10))

	if !fixNegative && minnum < 0 {
		width = max(width, len(strconv.FormatInt(minnum, 10)))
	}

	return width
}

func matrixValueBounds(matrix [][]int64) (int64, int64) {
	var maxnum int64 = -(1 << 32)
	var minnum int64 = 1 << 32

	for _, status := range matrix {
		for _, val := range status {
			maxnum = max(maxnum, val)
			minnum = min(minnum, val)
		}
	}

	return maxnum, minnum
}

func printMatrixRows(
	writer io.Writer,
	matrix [][]int64,
	indent int,
	last int,
	width int,
	fixNegative bool,
) {
	first := true

	for _, status := range matrix {
		_, _ = fmt.Fprint(writer, strings.Repeat(" ", indent-1))

		for i := range last {
			val := printableMatrixValue(status, i, fixNegative)
			if first {
				printFirstMatrixValue(writer, val, width)

				first = false

				continue
			}

			_, _ = fmt.Fprintf(writer, " %[1]*[2]d", width, val)
		}

		_, _ = fmt.Fprintln(writer)
	}
}

func printableMatrixValue(status []int64, index int, fixNegative bool) int64 {
	if index >= len(status) {
		return 0
	}

	val := status[index]
	if fixNegative && val < 0 {
		return 0
	}

	return val
}

func printFirstMatrixValue(writer io.Writer, val int64, width int) {
	_, _ = fmt.Fprintf(writer, " %d%s", val, strings.Repeat(" ", width-len(strconv.FormatInt(val, 10))))
}
