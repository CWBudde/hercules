package modes

import "strings"

const unknownContributorLabel = "Contributor"

// peopleChartLabel returns the public display name for an identity record.
//
// Hercules keeps every known name and email in pipe-separated identity records
// so downstream data exports can preserve the complete identity mapping. Charts
// should expose only the canonical name (the first non-email alias). Exact
// signature mode uses Git's "Name <email>" representation, which is handled
// here as well.
func peopleChartLabel(identity string) string {
	for alias := range strings.SplitSeq(identity, "|") {
		if label := publicIdentityAlias(alias); label != "" {
			return label
		}
	}

	return unknownContributorLabel
}

func publicIdentityAlias(alias string) string {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return ""
	}

	open := strings.LastIndex(alias, "<")
	if open >= 0 && strings.HasSuffix(alias, ">") {
		address := strings.TrimSpace(alias[open+1 : len(alias)-1])
		if strings.Contains(address, "@") {
			name := strings.TrimSpace(alias[:open])
			if name != "" && !strings.Contains(name, "@") {
				return name
			}

			return ""
		}
	}

	if strings.Contains(alias, "@") {
		return ""
	}

	return alias
}

func peopleChartLabels(identities []string) []string {
	labels := make([]string, len(identities))
	for i, identity := range identities {
		labels[i] = peopleChartLabel(identity)
	}

	return labels
}
