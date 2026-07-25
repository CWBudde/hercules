package identity

import (
	"strings"

	"github.com/go-git/go-git/v5/plumbing/object"
)

// ParseMailmap parses the contents of .mailmap and returns the mapping
// between signature parts. It does *not* follow the full signature
// matching convention, that is, developers are identified by email
// and by name independently.
func ParseMailmap(contents string) map[string]object.Signature {
	signatures := map[string]object.Signature{}

	lines := strings.SplitSeq(contents, "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		if strings.LastIndex(line, ">") != len(line)-1 {
			continue
		}

		ltp := strings.LastIndex(line, "<")
		fromEmail := line[ltp+1 : len(line)-1]
		line = strings.TrimSpace(line[:ltp])
		gtp := strings.LastIndex(line, ">")

		fromName := ""
		if gtp != len(line)-1 {
			fromName = strings.TrimSpace(line[gtp+1:])
		}

		toEmail := ""

		if gtp > 0 {
			line = line[:gtp]
			ltp = strings.LastIndex(line, "<")
			toEmail = line[ltp+1:]
			line = strings.TrimSpace(line[:ltp])
		}

		toName := line
		if fromEmail != "" {
			signatures[fromEmail] = object.Signature{Name: toName, Email: toEmail}
		}

		if fromName != "" {
			signatures[fromName] = object.Signature{Name: toName, Email: toEmail}
		}
	}

	return signatures
}
