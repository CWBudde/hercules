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

		aliases, signature, ok := parseMailmapLine(line)
		if !ok {
			continue
		}

		if mailmapAliasesConflict(signatures, aliases, signature) {
			continue
		}

		for _, alias := range aliases {
			signatures[alias] = signature
		}
	}

	return signatures
}

func parseMailmapLine(line string) ([]string, object.Signature, bool) {
	lastLeft := strings.LastIndexByte(line, '<')
	if lastLeft < 0 || line[len(line)-1] != '>' {
		return nil, object.Signature{}, false
	}

	fromEmail := strings.TrimSpace(line[lastLeft+1 : len(line)-1])
	if fromEmail == "" || strings.ContainsAny(fromEmail, "<>") {
		return nil, object.Signature{}, false
	}

	prefix := strings.TrimSpace(line[:lastLeft])

	toName, toEmail, fromName, ok := parseMailmapCanonical(prefix)
	if !ok {
		return nil, object.Signature{}, false
	}

	aliases := []string{fromEmail}
	if fromName != "" {
		aliases = append(aliases, fromName)
	}

	return aliases, object.Signature{Name: toName, Email: toEmail}, true
}

func parseMailmapCanonical(prefix string) (string, string, string, bool) {
	previousRight := strings.LastIndexByte(prefix, '>')
	if previousRight < 0 {
		return prefix, "", "", !strings.ContainsAny(prefix, "<>") && prefix != ""
	}

	previousLeft := strings.LastIndexByte(prefix[:previousRight], '<')
	if previousLeft < 0 {
		return "", "", "", false
	}

	toName := strings.TrimSpace(prefix[:previousLeft])
	toEmail := strings.TrimSpace(prefix[previousLeft+1 : previousRight])
	fromName := strings.TrimSpace(prefix[previousRight+1:])
	valid := !mailmapContainsDelimiter(toName, toEmail, fromName) && (toName != "" || toEmail != "")

	return toName, toEmail, fromName, valid
}

func mailmapContainsDelimiter(values ...string) bool {
	for _, value := range values {
		if strings.ContainsAny(value, "<>") {
			return true
		}
	}

	return false
}

func mailmapAliasesConflict(
	signatures map[string]object.Signature,
	aliases []string,
	signature object.Signature,
) bool {
	for _, alias := range aliases {
		if existing, found := signatures[alias]; found && existing != signature {
			return true
		}
	}

	return false
}
