package identity

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/pkg/errors"

	"github.com/cwbudde/hercules/internal/core"
)

// PeopleDetector determines the author of a commit. Same person can commit under different
// signatures, and we apply some heuristics to merge those together.
// It is a PipelineItem.
type PeopleDetector struct {
	core.NoopMerger
	// PeopleDict maps email || name  -> developer id
	PeopleDict map[string]int
	// ReversedPeopleDict maps developer id -> description
	ReversedPeopleDict []string
	// ExactSignatures chooses the matching algorithm: opportunistic email || name
	// or exact email && name
	ExactSignatures bool
	Anonymity       bool
	MergeThreshold  float64

	audit                    IdentityAudit
	mergeThresholdConfigured bool

	l core.Logger
}

const (
	// FactIdentityDetectorReversedPeopleDict is the name of the fact which is inserted in
	// PeopleDetector.Configure(). It corresponds to PeopleDetector.ReversedPeopleDict -
	// the mapping from the author indices to the main signature.
	FactIdentityDetectorReversedPeopleDict = "IdentityDetector.ReversedPeopleDict"
	// ConfigIdentityDetectorPeopleDictPath is the name of the configuration option
	// (PeopleDetector.Configure()) which allows to set the external PeopleDict mapping from a file.
	ConfigIdentityDetectorPeopleDictPath = "PeopleDetector.PeopleDictPath"
	// ConfigIdentityDetectorExactSignatures is the name of the configuration option
	// (PeopleDetector.Configure()) which changes the matching algorithm to exact signature (name + email)
	// correspondence.
	ConfigIdentityDetectorExactSignatures = "PeopleDetector.ExactSignatures"

	ConfigIdentityDetectorAnonymity = "PeopleDetector.Anonymity"
	// ConfigIdentityDetectorMergeThreshold is the configuration option which controls
	// the minimum confidence for automatic heuristic identity merges.
	ConfigIdentityDetectorMergeThreshold = "PeopleDetector.MergeThreshold"

	defaultIdentityMergeThreshold = 0.92
	ambiguousIdentityThreshold    = 0.85
)

var _ core.IdentityResolver = peopleResolver{}

var coAuthorTrailerRE = regexp.MustCompile(`(?im)^Co-authored-by:\s*(.+?)\s*<([^<>@\s]+@[^<>\s]+)>`)

// IdentityAudit is the JSON-friendly report of detected identities and merge decisions.
type IdentityAudit struct {
	Threshold      float64                   `json:"threshold"`
	Identities     []IdentityAuditIdentity   `json:"identities"`
	MergeDecisions []IdentityMergeDecision   `json:"merge_decisions"`
	Ambiguous      []IdentityMergeSuggestion `json:"ambiguous"`
}

// IdentityAuditIdentity describes a resolved canonical identity.
type IdentityAuditIdentity struct {
	ID             int      `json:"id"`
	Primary        string   `json:"primary"`
	Names          []string `json:"names"`
	Emails         []string `json:"emails"`
	PeopleDictLine string   `json:"people_dict_line"`
	SourceCount    int      `json:"source_count"`
}

// IdentityMergeDecision describes one automatic merge.
type IdentityMergeDecision struct {
	Identity   int     `json:"identity"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence"`
}

// IdentityMergeSuggestion describes one candidate which was left for manual review.
type IdentityMergeSuggestion struct {
	Left       string  `json:"left"`
	Right      string  `json:"right"`
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence"`
}

type peopleResolver struct {
	identities *PeopleDetector
}

func (v peopleResolver) MaxCount() int {
	if v.identities == nil {
		return 0
	}
	return len(v.identities.ReversedPeopleDict)
}

func (v peopleResolver) Count() int {
	if v.identities == nil {
		return 0
	}
	return len(v.identities.ReversedPeopleDict)
}

func (v peopleResolver) nameOf(id core.AuthorId, anonymity bool) string {
	if id == core.AuthorMissing || id < 0 || v.identities == nil || int(id) >= len(v.identities.ReversedPeopleDict) {
		return core.AuthorMissingName
	}
	if !anonymity {
		return v.identities.ReversedPeopleDict[id]
	}
	return v.anonymizeName(id)
}

func (v peopleResolver) FriendlyNameOf(id core.AuthorId) string {
	return v.nameOf(id, v.identities.Anonymity)
}

func (v peopleResolver) PrivateNameOf(id core.AuthorId) string {
	return v.nameOf(id, false)
}

func (v peopleResolver) anonymizeName(id core.AuthorId) string {
	return fmt.Sprintf("Author %3d", id)
}

func (v peopleResolver) ForEachIdentity(callback func(core.AuthorId, string)) bool {
	if v.identities == nil {
		return false
	}
	for id, name := range v.identities.ReversedPeopleDict {
		if v.identities.Anonymity {
			name = v.anonymizeName(core.AuthorId(id))
		}
		callback(core.AuthorId(id), name)
	}
	return true
}

func (v peopleResolver) CopyNames(privateNames bool) []string {
	if v.identities == nil {
		return nil
	}
	if privateNames || !v.identities.Anonymity {
		return append([]string(nil), v.identities.ReversedPeopleDict...)
	}

	names := make([]string, len(v.identities.ReversedPeopleDict))
	for i := range names {
		names[i] = v.anonymizeName(core.AuthorId(i))
	}
	return names
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (detector *PeopleDetector) Name() string {
	return "PeopleDetector"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (detector *PeopleDetector) Provides() []string {
	return []string{DependencyAuthor}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (detector *PeopleDetector) Requires() []string {
	return []string{}
}

func (detector *PeopleDetector) Features() []string {
	return []string{core.FeatureGitCommits}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (detector *PeopleDetector) ListConfigurationOptions() []core.ConfigurationOption {
	return []core.ConfigurationOption{
		{
			Name:        ConfigIdentityDetectorPeopleDictPath,
			Description: "Path to the file with developer -> name|email associations.",
			Flag:        "people-dict",
			Type:        core.PathConfigurationOption,
			Default:     "",
		}, {
			Name: ConfigIdentityDetectorExactSignatures,
			Description: "Disable separate name/email matching. This will lead to considerbly more " +
				"identities and should not be normally used.",
			Flag:    "exact-signatures",
			Type:    core.BoolConfigurationOption,
			Default: false,
		}, {
			Name:        ConfigIdentityDetectorAnonymity,
			Description: "Replaces identity info with sequential number.",
			Flag:        "people-anonymity",
			Type:        core.BoolConfigurationOption,
			Default:     false,
		}, {
			Name: ConfigIdentityDetectorMergeThreshold,
			Description: "Minimum confidence for automatic heuristic identity merges. " +
				"Lower values merge more name variants; higher values leave more cases for audit.",
			Flag:    "identity-merge-threshold",
			Type:    core.FloatConfigurationOption,
			Default: float32(defaultIdentityMergeThreshold),
		},
	}
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (detector *PeopleDetector) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		detector.l = l
	} else {
		detector.l = core.NewLogger()
	}

	detector.PeopleDict = nil
	if val, exists := facts[FactIdentityDetectorReversedPeopleDict].([]string); exists {
		detector.ReversedPeopleDict = val
	}

	if val, exists := facts[ConfigIdentityDetectorExactSignatures].(bool); exists {
		detector.ExactSignatures = val
	}

	if val, exists := facts[ConfigIdentityDetectorAnonymity].(bool); exists {
		detector.Anonymity = val
	}

	if val, exists := identityMergeThresholdFromFacts(facts); exists {
		if val < 0 || val > 1 {
			return errors.Errorf("%s must be between 0 and 1: %f", ConfigIdentityDetectorMergeThreshold, val)
		}
		detector.MergeThreshold = val
		detector.mergeThresholdConfigured = true
	}

	if peopleDictPath, ok := facts[ConfigIdentityDetectorPeopleDictPath].(string); ok && peopleDictPath != "" {
		err := detector.LoadPeopleDict(peopleDictPath)
		if err != nil {
			return errors.Errorf("failed to load %s: %v", peopleDictPath, err)
		}
	}

	if detector.ReversedPeopleDict == nil {
		if _, exists := facts[core.ConfigPipelineCommits]; !exists {
			panic("PeopleDetector needs a list of commits to initialize.")
		}
		detector.GeneratePeopleDict(facts[core.ConfigPipelineCommits].([]*object.Commit))
	}
	facts[FactIdentityDetectorReversedPeopleDict] = detector.ReversedPeopleDict

	if detector.PeopleDict == nil {
		detector.PeopleDict = make(map[string]int, len(detector.ReversedPeopleDict))
		for k, v := range detector.ReversedPeopleDict {
			detector.PeopleDict[v] = k
		}
	}
	if len(detector.audit.Identities) == 0 && len(detector.ReversedPeopleDict) > 0 {
		detector.ensureMergeThreshold()
		detector.rebuildAuditFromState(nil, nil, nil)
	}

	var resolver core.IdentityResolver = peopleResolver{detector}
	facts[core.FactIdentityResolver] = resolver
	return nil
}

func (*PeopleDetector) ConfigureUpstream(map[string]interface{}) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (detector *PeopleDetector) Initialize(*git.Repository) error {
	detector.l = core.NewLogger()
	return nil
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (detector *PeopleDetector) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	commit := deps[core.DependencyCommit].(*object.Commit)
	var authorID int
	var exists bool
	signature := commit.Author
	if !detector.ExactSignatures {
		authorID, exists = detector.PeopleDict[strings.ToLower(signature.Email)]
		if !exists {
			authorID, exists = detector.PeopleDict[strings.ToLower(signature.Name)]
		}
	} else {
		authorID, exists = detector.PeopleDict[strings.ToLower(signature.String())]
	}
	if !exists {
		authorID = core.AuthorMissing
	}
	return map[string]interface{}{DependencyAuthor: authorID}, nil
}

// Fork clones this PipelineItem.
func (detector *PeopleDetector) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(detector, n)
}

// LoadPeopleDict loads author signatures from a text file.
// The format is one signature per line, and the signature consists of several
// keys separated by "|". The first key is the main one and used to reference all the rest.
func (detector *PeopleDetector) LoadPeopleDict(path string) error {
	detector.ensureMergeThreshold()
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	dict := make(map[string]int)
	var reverseDict []string
	size := 0
	for scanner.Scan() {
		ids := strings.Split(scanner.Text(), "|")
		canon := ids[0]
		var exists bool
		var canonIndex int
		// lookup or create a new canonical value
		if canonIndex, exists = dict[strings.ToLower(canon)]; !exists {
			reverseDict = append(reverseDict, canon)
			canonIndex = size
			size++
		}
		for _, id := range ids {
			dict[strings.ToLower(id)] = canonIndex
		}
	}
	detector.PeopleDict = dict
	detector.ReversedPeopleDict = reverseDict
	detector.rebuildAuditFromState(nil, nil, nil)
	return nil
}

// GeneratePeopleDict loads author signatures from the specified list of Git commits.
func (detector *PeopleDetector) GeneratePeopleDict(commits []*object.Commit) {
	detector.ensureMergeThreshold()
	dict := map[string]int{}
	emails := map[int][]string{}
	names := map[int][]string{}
	sourceCounts := map[int]int{}
	var decisions []IdentityMergeDecision
	var ambiguous []IdentityMergeSuggestion
	size := 0

	// TODO(vmarkovtsev): properly handle .mailmap if ExactSignatures
	if !detector.ExactSignatures {
		if mailMapContents, ok := lastCommitMailmapContents(commits); ok {
			mailmap := ParseMailmap(mailMapContents)
			for key, val := range mailmap {
				key = strings.ToLower(key)
				toEmail := strings.ToLower(val.Email)
				toName := strings.ToLower(val.Name)
				id, exists := dict[toEmail]
				if !exists {
					id, exists = dict[toName]
				}
				if exists {
					dict[key] = id
				} else {
					id = size
					size++
					if toEmail != "" {
						dict[toEmail] = id
						emails[id] = append(emails[id], toEmail)
					}
					if toName != "" {
						dict[toName] = id
						names[id] = append(names[id], toName)
					}
					dict[key] = id
				}
				if strings.Contains(key, "@") {
					exists := false
					for _, val := range emails[id] {
						if key == val {
							exists = true
							break
						}
					}
					if !exists {
						emails[id] = append(emails[id], key)
					}
				} else {
					exists := false
					for _, val := range names[id] {
						if key == val {
							exists = true
							break
						}
					}
					if !exists {
						names[id] = append(names[id], key)
					}
				}
			}
		}
	}

	for _, commit := range commits {
		if !detector.ExactSignatures {
			_, addedDecision := detector.addSignature(
				commit.Author.Name, commit.Author.Email, "commit", &size, dict, names, emails, sourceCounts, &ambiguous,
			)
			if addedDecision != nil {
				decisions = append(decisions, *addedDecision)
			}
			for _, signature := range parseCoAuthors(commit.Message) {
				_, addedDecision = detector.addSignature(
					signature.Name, signature.Email, "co-authored-by", &size, dict, names, emails, sourceCounts, &ambiguous,
				)
				if addedDecision != nil {
					decisions = append(decisions, *addedDecision)
				}
			}
		} else { // !detector.ExactSignatures
			sig := strings.ToLower(commit.Author.String())
			if _, exists := dict[sig]; !exists {
				dict[sig] = size
				sourceCounts[size]++
				size++
			} else {
				sourceCounts[dict[sig]]++
			}
		}
	}
	reverseDict := make([]string, size)
	if !detector.ExactSignatures {
		for _, val := range dict {
			sort.Strings(names[val])
			sort.Strings(emails[val])
			reverseDict[val] = strings.Join(names[val], "|") + "|" + strings.Join(emails[val], "|")
		}
	} else {
		for key, val := range dict {
			reverseDict[val] = key
		}
	}
	detector.PeopleDict = dict
	detector.ReversedPeopleDict = reverseDict
	detector.rebuildAuditFromState(names, emails, sourceCounts, decisions, ambiguous)
}

func (detector *PeopleDetector) ensureMergeThreshold() {
	if detector.MergeThreshold == 0 && !detector.mergeThresholdConfigured {
		detector.MergeThreshold = defaultIdentityMergeThreshold
	}
}

func identityMergeThresholdFromFacts(facts map[string]interface{}) (float64, bool) {
	switch val := facts[ConfigIdentityDetectorMergeThreshold].(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	default:
		return 0, false
	}
}

func (detector *PeopleDetector) addSignature(name, email, reason string, size *int,
	dict map[string]int, names, emails map[int][]string, sourceCounts map[int]int,
	ambiguous *[]IdentityMergeSuggestion,
) (int, *IdentityMergeDecision) {
	name = normalizeIdentityKey(name)
	email = normalizeIdentityKey(email)
	if name == "" && email == "" {
		return core.AuthorMissing, nil
	}
	if email != "" {
		if id, exists := dict[email]; exists {
			sourceCounts[id]++
			to := peopleDictLine(names[id], emails[id])
			addIdentityKey(id, name, false, dict, names, emails)
			return id, mergeDecisionForReason(id, name, email, reason, to, 1)
		}
	}
	if name != "" {
		if id, exists := dict[name]; exists {
			sourceCounts[id]++
			to := peopleDictLine(names[id], emails[id])
			addIdentityKey(id, email, true, dict, names, emails)
			return id, mergeDecisionForReason(id, name, email, reason, to, 1)
		}
	}

	if reason == "commit" {
		if candidate, confidence, tied := detector.bestFuzzyCandidate(name, email, names, emails); candidate != core.AuthorMissing {
			suggestion := IdentityMergeSuggestion{
				Left:       detector.identityLine(candidate, names, emails),
				Right:      peopleDictLine([]string{name}, []string{email}),
				Reason:     "fuzzy-name",
				Confidence: confidence,
			}
			if !tied && confidence >= detector.MergeThreshold {
				sourceCounts[candidate]++
				addIdentityKey(candidate, name, false, dict, names, emails)
				addIdentityKey(candidate, email, true, dict, names, emails)
				return candidate, &IdentityMergeDecision{
					Identity:   candidate,
					From:       suggestion.Right,
					To:         suggestion.Left,
					Reason:     "fuzzy-name",
					Confidence: confidence,
				}
			}
			*ambiguous = append(*ambiguous, suggestion)
		}
	}

	id := *size
	*size++
	sourceCounts[id]++
	addIdentityKey(id, name, false, dict, names, emails)
	addIdentityKey(id, email, true, dict, names, emails)
	return id, nil
}

func mergeDecisionForReason(id int, name, email, reason, to string, confidence float64) *IdentityMergeDecision {
	if reason != "co-authored-by" {
		return nil
	}
	return &IdentityMergeDecision{
		Identity:   id,
		From:       peopleDictLine([]string{name}, []string{email}),
		To:         to,
		Reason:     reason,
		Confidence: confidence,
	}
}

func (detector *PeopleDetector) bestFuzzyCandidate(name, email string, names map[int][]string,
	emails map[int][]string,
) (int, float64, bool) {
	if name == "" {
		return core.AuthorMissing, 0, false
	}
	ids := make([]int, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	bestID := core.AuthorMissing
	bestScore := 0.0
	tied := false
	for _, id := range ids {
		if !sameEmailDomain(email, emails[id]) {
			continue
		}
		for _, candidateName := range names[id] {
			score := jaroWinkler(name, candidateName)
			if score < ambiguousIdentityThreshold {
				continue
			}
			if score > bestScore {
				bestScore = score
				bestID = id
				tied = false
			} else if score == bestScore {
				tied = true
			}
		}
	}
	return bestID, bestScore, tied
}

func addIdentityKey(id int, key string, isEmail bool, dict map[string]int, names map[int][]string,
	emails map[int][]string,
) {
	if key == "" {
		return
	}
	dict[key] = id
	if isEmail {
		emails[id] = appendUnique(emails[id], key)
		return
	}
	names[id] = appendUnique(names[id], key)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func normalizeIdentityKey(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.Join(strings.Fields(value), " ")
}

func parseCoAuthors(message string) []object.Signature {
	matches := coAuthorTrailerRE.FindAllStringSubmatch(message, -1)
	signatures := make([]object.Signature, 0, len(matches))
	for _, match := range matches {
		signatures = append(signatures, object.Signature{
			Name:  strings.TrimSpace(match[1]),
			Email: strings.TrimSpace(match[2]),
		})
	}
	return signatures
}

func lastCommitMailmapContents(commits []*object.Commit) (contents string, ok bool) {
	if len(commits) == 0 {
		return "", false
	}
	defer func() {
		if recover() != nil {
			contents = ""
			ok = false
		}
	}()
	mailmapFile, err := commits[len(commits)-1].File(".mailmap")
	if err != nil {
		return "", false
	}
	contents, err = mailmapFile.Contents()
	if err != nil {
		return "", false
	}
	return contents, true
}

func peopleDictLine(names, emails []string) string {
	names = append([]string(nil), names...)
	emails = append([]string(nil), emails...)
	sort.Strings(names)
	sort.Strings(emails)
	parts := append(names, emails...)
	compact := parts[:0]
	for _, part := range parts {
		if part != "" {
			compact = append(compact, part)
		}
	}
	return strings.Join(compact, "|")
}

func sameEmailDomain(email string, existing []string) bool {
	domain := emailDomain(email)
	if domain == "" {
		return false
	}
	for _, candidate := range existing {
		if emailDomain(candidate) == domain {
			return true
		}
	}
	return false
}

func emailDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

func jaroWinkler(a, b string) float64 {
	a = normalizeForSimilarity(a)
	b = normalizeForSimilarity(b)
	if a == b {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	jaro := jaroSimilarity(a, b)
	prefix := 0
	maxPrefix := minInt(4, minInt(len(a), len(b)))
	for prefix < maxPrefix && a[prefix] == b[prefix] {
		prefix++
	}
	return jaro + float64(prefix)*0.1*(1-jaro)
}

func normalizeForSimilarity(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func jaroSimilarity(a, b string) float64 {
	if len(a) > len(b) {
		a, b = b, a
	}
	matchDistance := maxInt(len(b)/2-1, 0)
	aMatches := make([]bool, len(a))
	bMatches := make([]bool, len(b))
	matches := 0
	for i := range a {
		start := maxInt(i-matchDistance, 0)
		end := minInt(i+matchDistance+1, len(b))
		for j := start; j < end; j++ {
			if bMatches[j] || a[i] != b[j] {
				continue
			}
			aMatches[i] = true
			bMatches[j] = true
			matches++
			break
		}
	}
	if matches == 0 {
		return 0
	}
	transpositions := 0
	k := 0
	for i := range a {
		if !aMatches[i] {
			continue
		}
		for !bMatches[k] {
			k++
		}
		if a[i] != b[k] {
			transpositions++
		}
		k++
	}
	m := float64(matches)
	return (m/float64(len(a)) + m/float64(len(b)) + (m-float64(transpositions)/2)/m) / 3
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// IdentityAudit returns a copy of the detector's identity audit report.
func (detector *PeopleDetector) IdentityAudit() IdentityAudit {
	audit := detector.audit
	audit.Identities = append([]IdentityAuditIdentity(nil), detector.audit.Identities...)
	audit.MergeDecisions = append([]IdentityMergeDecision(nil), detector.audit.MergeDecisions...)
	audit.Ambiguous = append([]IdentityMergeSuggestion(nil), detector.audit.Ambiguous...)
	return audit
}

// GeneratePeopleDictTemplate returns the current detected identities in people-dict format.
func (detector *PeopleDetector) GeneratePeopleDictTemplate() string {
	if len(detector.audit.Identities) == 0 && len(detector.ReversedPeopleDict) > 0 {
		detector.rebuildAuditFromState(nil, nil, nil)
	}
	var builder strings.Builder
	for _, identity := range detector.audit.Identities {
		builder.WriteString(identity.PeopleDictLine)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func (detector *PeopleDetector) identityLine(id int, names, emails map[int][]string) string {
	return peopleDictLine(names[id], emails[id])
}

func (detector *PeopleDetector) rebuildAuditFromState(names, emails map[int][]string,
	sourceCounts map[int]int, decisions ...interface{},
) {
	mergeDecisions, ambiguous := identityAuditDecisions(decisions)
	names, emails = detector.identityAuditMaps(names, emails)
	identities := detector.identityAuditIdentities(names, emails, sourceCounts)

	sortIdentityAuditDecisions(mergeDecisions, ambiguous)
	detector.audit = IdentityAudit{
		Threshold:      detector.MergeThreshold,
		Identities:     identities,
		MergeDecisions: mergeDecisions,
		Ambiguous:      ambiguous,
	}
}

func identityAuditDecisions(decisions []any) ([]IdentityMergeDecision, []IdentityMergeSuggestion) {
	var mergeDecisions []IdentityMergeDecision
	var ambiguous []IdentityMergeSuggestion
	if len(decisions) > 0 {
		mergeDecisions, _ = decisions[0].([]IdentityMergeDecision)
	}
	if len(decisions) > 1 {
		ambiguous, _ = decisions[1].([]IdentityMergeSuggestion)
	}

	return mergeDecisions, ambiguous
}

func (detector *PeopleDetector) identityAuditMaps(names, emails map[int][]string,
) (map[int][]string, map[int][]string) {
	if names != nil && emails != nil {
		return names, emails
	}

	names = map[int][]string{}
	emails = map[int][]string{}

	for id, line := range detector.ReversedPeopleDict {
		for part := range strings.SplitSeq(line, "|") {
			if strings.Contains(part, "@") {
				emails[id] = append(emails[id], part)
			} else if part != "" {
				names[id] = append(names[id], part)
			}
		}
	}

	return names, emails
}

func (detector *PeopleDetector) identityAuditIdentities(names, emails map[int][]string,
	sourceCounts map[int]int,
) []IdentityAuditIdentity {
	identities := make([]IdentityAuditIdentity, 0, len(detector.ReversedPeopleDict))
	for id := range detector.ReversedPeopleDict {
		line := peopleDictLine(names[id], emails[id])
		identityNames := append([]string(nil), names[id]...)
		identityEmails := append([]string(nil), emails[id]...)
		sort.Strings(identityNames)
		sort.Strings(identityEmails)
		identities = append(identities, IdentityAuditIdentity{
			ID:             id,
			Primary:        primaryIdentityValue(identityNames, identityEmails),
			Names:          identityNames,
			Emails:         identityEmails,
			PeopleDictLine: line,
			SourceCount:    sourceCounts[id],
		})
	}

	return identities
}

func sortIdentityAuditDecisions(mergeDecisions []IdentityMergeDecision,
	ambiguous []IdentityMergeSuggestion,
) {
	sort.Slice(mergeDecisions, func(i, j int) bool {
		if mergeDecisions[i].Identity != mergeDecisions[j].Identity {
			return mergeDecisions[i].Identity < mergeDecisions[j].Identity
		}
		if mergeDecisions[i].Reason != mergeDecisions[j].Reason {
			return mergeDecisions[i].Reason < mergeDecisions[j].Reason
		}
		return mergeDecisions[i].From < mergeDecisions[j].From
	})
	sort.Slice(ambiguous, func(i, j int) bool {
		if ambiguous[i].Confidence != ambiguous[j].Confidence {
			return ambiguous[i].Confidence > ambiguous[j].Confidence
		}
		if ambiguous[i].Left != ambiguous[j].Left {
			return ambiguous[i].Left < ambiguous[j].Left
		}
		return ambiguous[i].Right < ambiguous[j].Right
	})
}

func primaryIdentityValue(names, emails []string) string {
	if len(names) > 0 {
		return names[0]
	}
	if len(emails) > 0 {
		return emails[0]
	}
	return ""
}

func init() {
	core.Registry.RegisterPreferred(&PeopleDetector{}, true)
}
