package core

import (
	"maps"
	"math"
)

type (
	FileId     int32
	AuthorId   int32
	TickNumber int32
)

const (
	FactIdentityResolver    = "Identity.Resolver"
	FactLineHistoryResolver = "LineHistory.Resolver"
)

const (
	// AuthorMissing is the internal author index which denotes any unmatched identities
	// (Detector.Consume()). It may *not* be (1 << 18) - 1, see BurndownAnalysis.packPersonWithDay().
	AuthorMissing = (1 << 18) - 1
	// AuthorMissingName is the string name which corresponds to AuthorMissing.
	AuthorMissingName = "<unmatched>"
)

type IdentityResolver interface {
	MaxCount() int
	Count() int
	FriendlyNameOf(id AuthorId) string
	PrivateNameOf(id AuthorId) string
	ForEachIdentity(callback func(AuthorId, string)) bool
	CopyNames(privateNames bool) []string
}

var _ IdentityResolver = identityResolver{}

func NewIdentityResolver(names []string, toIds map[string]int) IdentityResolver {
	resolver := identityResolver{}

	nameCount := len(names)
	if nameCount == 0 {
		return resolver
	}

	resolver.toNames = make([]string, nameCount)
	copy(resolver.toNames, names)

	if len(toIds) != 0 {
		nameCount = len(toIds)
	}

	resolver.toIds = make(map[string]int, nameCount)

	if len(toIds) != 0 {
		maps.Copy(resolver.toIds, toIds)
	} else {
		for k, v := range names {
			resolver.toIds[v] = k
		}
	}

	return resolver
}

type identityResolver struct {
	toIds   map[string]int
	toNames []string
}

func (v identityResolver) MaxCount() int {
	return len(v.toNames)
}

func (v identityResolver) Count() int {
	return len(v.toNames)
}

func (v identityResolver) PrivateNameOf(id AuthorId) string {
	return v.FriendlyNameOf(id)
}

func (v identityResolver) FriendlyNameOf(id AuthorId) string {
	if id == AuthorMissing || id < 0 || int(id) >= len(v.toNames) {
		return AuthorMissingName
	}

	return v.toNames[id]
}

func (v identityResolver) FindIdOf(name string) AuthorId {
	if id, ok := v.toIds[name]; ok {
		if id < math.MinInt32 || id > math.MaxInt32 {
			return AuthorId(-1)
		}

		return AuthorId(id)
	}

	return AuthorId(-1)
}

func (v identityResolver) ForEachIdentity(callback func(AuthorId, string)) bool {
	for id, name := range v.toNames {
		callback(AuthorId(id), name)
	}

	return true
}

func (v identityResolver) CopyNames(bool) []string {
	return append([]string(nil), v.toNames...)
}

type LineHistoryChange struct {
	FileId

	CurrTick, PrevTick     TickNumber
	CurrAuthor, PrevAuthor AuthorId
	Delta                  int
}

func NewLineHistoryDeletion(id FileId, author AuthorId, tick TickNumber) LineHistoryChange {
	return LineHistoryChange{
		FileId:     id,
		CurrTick:   tick,
		CurrAuthor: author,
		PrevTick:   tick,
		PrevAuthor: AuthorMissing,
		Delta:      math.MinInt,
	}
}

func (v LineHistoryChange) IsDelete() bool {
	return v.PrevAuthor == AuthorMissing && v.Delta == math.MinInt
}

type LineHistoryChanges struct {
	Changes  []LineHistoryChange
	Resolver FileIdResolver
}

type FileIdResolver interface {
	NameOf(id FileId) string
	MergedWith(id FileId) (FileId, string, bool)
	ForEachFile(callback func(id FileId, name string)) bool
	ScanFile(id FileId, callback func(line int, tick TickNumber, author AuthorId)) bool
}
