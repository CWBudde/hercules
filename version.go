package hercules

import (
	"reflect"
	"strconv"
	"strings"
)

// BinaryGitHash is the Git hash of the Hercules binary file which is executing.
var BinaryGitHash = "<unknown>"

// BinaryVersion is Hercules' API version. It matches the package name.
var BinaryVersion = detectBinaryVersion()

type versionProbe struct{}

func detectBinaryVersion() int {
	parts := strings.Split(reflect.TypeFor[versionProbe]().PkgPath(), ".")
	version, _ := strconv.Atoi(parts[len(parts)-1][1:])

	return version
}
