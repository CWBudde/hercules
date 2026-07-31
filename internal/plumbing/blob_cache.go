package plumbing

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"sync"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"

	"github.com/cwbudde/hercules/internal"
	"github.com/cwbudde/hercules/internal/core"
)

// ErrBinary is raised in CachedBlob.CountLines() if the file is binary.
var ErrBinary = errors.New("binary")

var (
	errIncompleteBlobRead         = errors.New("incomplete blob read")
	errInvalidBlobCacheDependency = errors.New("invalid blob cache dependency")
	errInvalidBlobSize            = errors.New("invalid blob size")
	// ErrBlobTooLarge indicates that a blob exceeds the configured per-blob cache limit.
	ErrBlobTooLarge = errors.New("blob exceeds cache size limit")
	// ErrBlobCacheBudgetExceeded indicates that the changed blobs exceed the configured
	// aggregate cache budget for one commit.
	ErrBlobCacheBudgetExceeded = errors.New("commit blob cache budget exceeded")
)

// CachedBlob allows to explicitly cache the binary data associated with the Blob object.
type CachedBlob struct {
	object.Blob

	// Data is the read contents of the blob object.
	Data []byte
	// Oversized marks a placeholder which stands in for a blob that did not fit the configured
	// cache limits. Its contents were never read, so it is reported as opaque binary content
	// rather than as empty text - see CountLines().
	Oversized bool
	// OriginalSize is the declared size of the blob which Oversized replaced. It exists only for
	// diagnostics; Size is deliberately left at 0 so that this placeholder cannot be mistaken for
	// a real payload by size-driven heuristics.
	OriginalSize int64
}

// Reader returns a reader allow the access to the content of the blob.
func (b *CachedBlob) Reader() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.Data)), nil
}

// Cache reads the underlying blob object and sets CachedBlob.Data using the default safety limit.
func (b *CachedBlob) Cache() error {
	return b.CacheWithLimit(DefaultBlobCacheMaxBlobSize)
}

// CacheWithLimit reads the underlying blob object without allocating more than maxBytes for Data.
// A non-positive maxBytes uses the default safety limit.
func (b *CachedBlob) CacheWithLimit(maxBytes int64) error {
	size, err := b.validatedCacheSize(maxBytes)
	if err != nil {
		return err
	}

	reader, err := b.Blob.Reader()
	if err != nil {
		return fmt.Errorf("open blob %s for caching: %w", b.Hash.String(), err)
	}

	data, readErr := readExactBlob(reader, size, b.Hash)
	closeErr := reader.Close()

	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}

	b.Data = data

	return nil
}

func (b *CachedBlob) validatedCacheSize(maxBytes int64) (int, error) {
	if b.Size < 0 {
		return 0, fmt.Errorf("%w for %s: %d", errInvalidBlobSize, b.Hash.String(), b.Size)
	}

	if maxBytes <= 0 {
		maxBytes = DefaultBlobCacheMaxBlobSize
	}

	if b.Size > maxBytes {
		return 0, fmt.Errorf(
			"%w for %s: %d bytes exceeds %d",
			ErrBlobTooLarge, b.Hash.String(), b.Size, maxBytes,
		)
	}

	size := int(b.Size)
	if int64(size) != b.Size {
		return 0, fmt.Errorf(
			"%w for %s: %d cannot fit in an int", errInvalidBlobSize, b.Hash, b.Size,
		)
	}

	return size, nil
}

func readExactBlob(reader io.Reader, size int, hash plumbing.Hash) ([]byte, error) {
	data := make([]byte, size)

	read, err := io.ReadFull(reader, data)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("%w for %s: got %d bytes, expected %d",
				errIncompleteBlobRead, hash.String(), read, size)
		}

		return nil, fmt.Errorf("read blob %s into cache: %w", hash.String(), err)
	}

	var extra [1]byte

	extraRead, err := io.ReadFull(reader, extra[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("verify blob %s cache size: %w", hash.String(), err)
	}

	if extraRead != 0 {
		return nil, fmt.Errorf(
			"%w for %s: got more than %d bytes", errIncompleteBlobRead, hash.String(), size,
		)
	}

	return data, nil
}

// CountLines returns the number of lines in the blob or (0, ErrBinary) if it is binary.
func (b *CachedBlob) CountLines() (int, error) {
	// An oversized placeholder holds no contents at all. Reporting it as empty text would make
	// every line of the real file look like a deletion, so it is reported as binary: opaque,
	// excluded from line counts and diffs, exactly like a real binary file.
	if b.Oversized {
		return 0, ErrBinary
	}

	if len(b.Data) == 0 {
		return 0, nil
	}
	// 8000 was taken from go-git's utils/binary.IsBinary()
	sniffLen := 8000

	sniff := b.Data
	if len(sniff) > sniffLen {
		sniff = sniff[:sniffLen]
	}

	if bytes.IndexByte(sniff, 0) >= 0 {
		return 0, ErrBinary
	}

	lines := bytes.Count(b.Data, []byte{'\n'})
	if b.Data[len(b.Data)-1] != '\n' {
		lines++
	}

	return lines, nil
}

// BlobCache loads the blobs which correspond to the changed files in a commit.
// It is a PipelineItem.
// It must provide the old and the new objects; "blobCache" rotates and allows to not load
// the same blobs twice. Outdated objects are removed so "blobCache" never grows big.
type BlobCache struct {
	core.NoopMerger

	// Specifies how to handle the situation when we encounter a git submodule - an object
	// without the blob. If true, we look inside .gitmodules and if we don't find it,
	// raise an error. If false, we do not look inside .gitmodules and always succeed.
	FailOnMissingSubmodules bool
	// MaxBlobSize is the maximum number of bytes cached for one blob.
	MaxBlobSize int64
	// MaxCommitSize is the maximum aggregate size of distinct blobs cached for one commit.
	MaxCommitSize int64
	// FailOnOversizedBlobs restores the fail-closed behaviour of both cache limits. By default a
	// blob which does not fit is recorded as binary content and the run continues; setting this
	// aborts the run instead.
	FailOnOversizedBlobs bool

	repository *git.Repository
	cache      map[plumbing.Hash]*CachedBlob
	oversize   *oversizeReport

	l core.Logger
}

const (
	// ConfigBlobCacheFailOnMissingSubmodules is the name of the configuration option for
	// BlobCache.Configure() to check if the referenced submodules are registered in .gitignore.
	ConfigBlobCacheFailOnMissingSubmodules = "BlobCache.FailOnMissingSubmodules"
	// ConfigBlobCacheMaxBlobSize sets the maximum cached blob size in bytes.
	ConfigBlobCacheMaxBlobSize = "BlobCache.MaxBlobSize"
	// ConfigBlobCacheMaxCommitSize sets the aggregate per-commit blob cache budget in bytes.
	ConfigBlobCacheMaxCommitSize = "BlobCache.MaxCommitSize"
	// ConfigBlobCacheFailOnOversizedBlobs makes both cache limits fail-closed again.
	ConfigBlobCacheFailOnOversizedBlobs = "BlobCache.FailOnOversizedBlobs"
	// DefaultBlobCacheMaxBlobSize is the default per-blob cache limit (100 MiB).
	DefaultBlobCacheMaxBlobSize int64 = 100 * 1024 * 1024
	// DefaultBlobCacheMaxCommitSize is the default aggregate per-commit cache limit (512 MiB).
	DefaultBlobCacheMaxCommitSize int64 = 512 * 1024 * 1024
	// DependencyBlobCache identifies the dependency provided by BlobCache.
	DependencyBlobCache = "blob_cache"
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (blobCache *BlobCache) Name() string {
	return "BlobCache"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (blobCache *BlobCache) Provides() []string {
	return []string{DependencyBlobCache}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (blobCache *BlobCache) Requires() []string {
	return []string{DependencyTreeChanges}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (blobCache *BlobCache) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name: ConfigBlobCacheFailOnMissingSubmodules,
			Description: "Specifies whether to panic if any referenced submodule does " +
				"not exist in .gitmodules and thus the corresponding Git object cannot be loaded. " +
				"Override this if you want to ensure that your repository is integral.",
			Flag:    "fail-on-missing-submodules",
			Type:    core.BoolConfigurationOption,
			Default: false,
		},
		{
			Name: ConfigBlobCacheMaxBlobSize,
			Description: "Maximum size in bytes of a single cached blob; non-positive values use " +
				"the default. A blob above this size is not read: it is recorded as binary content " +
				"and thus excluded from line counts, diffs, language and rename detection, and the " +
				"run continues with a warning.",
			Flag:    "blob-cache-max-blob-size",
			Type:    core.IntConfigurationOption,
			Default: int(DefaultBlobCacheMaxBlobSize),
		},
		{
			Name: ConfigBlobCacheMaxCommitSize,
			Description: "Maximum aggregate size in bytes of distinct blobs cached for one commit; " +
				"non-positive values use the default. A blob which does not fit the remaining budget " +
				"of its commit is not read: it is recorded as binary content and thus excluded from " +
				"line counts, diffs, language and rename detection, and the run continues with a " +
				"warning.",
			Flag:    "blob-cache-max-commit-size",
			Type:    core.IntConfigurationOption,
			Default: int(DefaultBlobCacheMaxCommitSize),
		},
		{
			Name: ConfigBlobCacheFailOnOversizedBlobs,
			Description: "Abort the run instead of recording a blob as binary content when it " +
				"exceeds --blob-cache-max-blob-size or does not fit the remaining " +
				"--blob-cache-max-commit-size budget of its commit.",
			Flag:    "fail-on-oversized-blobs",
			Type:    core.BoolConfigurationOption,
			Default: false,
		},
	}

	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (blobCache *BlobCache) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		blobCache.l = l
	} else {
		blobCache.l = core.NewLogger()
	}

	if val, exists := facts[ConfigBlobCacheFailOnMissingSubmodules].(bool); exists {
		blobCache.FailOnMissingSubmodules = val
	}

	if val, exists := facts[ConfigBlobCacheFailOnOversizedBlobs].(bool); exists {
		blobCache.FailOnOversizedBlobs = val
	}

	blobCache.MaxBlobSize = configuredBlobLimit(
		facts, ConfigBlobCacheMaxBlobSize, DefaultBlobCacheMaxBlobSize,
	)
	blobCache.MaxCommitSize = configuredBlobLimit(
		facts, ConfigBlobCacheMaxCommitSize, DefaultBlobCacheMaxCommitSize,
	)

	return nil
}

func configuredBlobLimit(facts map[string]any, name string, fallback int64) int64 {
	value, exists := facts[name].(int)
	if !exists || value <= 0 {
		return fallback
	}

	return int64(value)
}

func (*BlobCache) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (blobCache *BlobCache) Initialize(repository *git.Repository) error {
	// Do not clobber a logger installed by Configure(): an embedder which supplied its own logger
	// must receive the oversized-blob warnings.
	if blobCache.l == nil {
		blobCache.l = core.NewLogger()
	}

	blobCache.repository = repository

	blobCache.cache = map[plumbing.Hash]*CachedBlob{}
	blobCache.oversize = &oversizeReport{}

	if blobCache.MaxBlobSize <= 0 {
		blobCache.MaxBlobSize = DefaultBlobCacheMaxBlobSize
	}

	if blobCache.MaxCommitSize <= 0 {
		blobCache.MaxCommitSize = DefaultBlobCacheMaxCommitSize
	}

	return nil
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents
// the analysed *object.Commit. This function returns the mapping with analysis
// results. The keys must be the same as in Provides(). If there was an error,
// nil is returned.
func (blobCache *BlobCache) Consume(deps map[string]any) (map[string]any, error) {
	commit, ok := deps[core.DependencyCommit].(*object.Commit)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errInvalidBlobCacheDependency, core.DependencyCommit)
	}

	changes, ok := deps[DependencyTreeChanges].(object.Changes)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errInvalidBlobCacheDependency, DependencyTreeChanges)
	}

	cache := map[plumbing.Hash]*CachedBlob{}
	newCache := map[plumbing.Hash]*CachedBlob{}
	budget := newCommitBlobBudget(blobCache.MaxCommitSize)

	for _, change := range changes {
		action, err := change.Action()
		if err != nil {
			blobCache.l.Errorf("no action in %s\n", change.To.TreeEntry.Hash)

			return nil, fmt.Errorf(
				"determine action for tree change %q -> %q: %w",
				change.From.Name, change.To.Name, err,
			)
		}

		switch action {
		case merkletrie.Insert:
			err = blobCache.cacheTo(change.To, commit, cache, newCache, budget)
		case merkletrie.Delete:
			err = blobCache.cacheFrom(change.From, commit, cache, budget, true)
		case merkletrie.Modify:
			err = blobCache.cacheTo(change.To, commit, cache, newCache, budget)
			if err == nil {
				err = blobCache.cacheFrom(change.From, commit, cache, budget, false)
			}
		}

		if err != nil {
			return nil, err
		}
	}

	blobCache.cache = newCache

	return map[string]any{DependencyBlobCache: cache}, nil
}

// Fork clones this PipelineItem.
func (blobCache *BlobCache) Fork(n int) []core.PipelineItem {
	caches := make([]core.PipelineItem, n)
	for i := range n {
		cache := map[plumbing.Hash]*CachedBlob{}
		maps.Copy(cache, blobCache.cache)

		caches[i] = &BlobCache{
			FailOnMissingSubmodules: blobCache.FailOnMissingSubmodules,
			MaxBlobSize:             blobCache.MaxBlobSize,
			MaxCommitSize:           blobCache.MaxCommitSize,
			FailOnOversizedBlobs:    blobCache.FailOnOversizedBlobs,
			repository:              blobCache.repository,
			cache:                   cache,
			// Shared by pointer: the oversized-blob diagnostic is once per run, not once per branch.
			oversize: blobCache.oversize,
			// Clones are never Initialize()d, so a missing logger here is a nil interface and any
			// diagnostic in a forked branch would panic.
			l: blobCache.l,
		}
	}

	return caches
}

// Dispose flushes the end-of-run summary of the blobs which were skipped because they exceeded
// the configured cache limits. It is idempotent and, because every clone shares one report, emits
// the summary exactly once per run.
func (blobCache *BlobCache) Dispose() {
	blobCache.oversizeReporter().summarise(blobCache.l)
}

func (blobCache *BlobCache) cacheTo(
	entry object.ChangeEntry,
	commit *object.Commit,
	cache, newCache map[plumbing.Hash]*CachedBlob,
	budget *commitBlobBudget,
) error {
	if cached, exists := cache[entry.TreeEntry.Hash]; exists {
		newCache[entry.TreeEntry.Hash] = cached
		return nil
	}

	if cached, exists := blobCache.cache[entry.TreeEntry.Hash]; exists {
		err := blobCache.validateCachedAndReserve(cached, budget)
		if blobCache.blobSkipped(err) {
			// Storing the placeholder in both maps is what actually releases the retained blob.
			return blobCache.storePlaceholder(entry, cached.Size, err, cache, newCache)
		}

		if err != nil {
			return err
		}

		cache[entry.TreeEntry.Hash] = cached
		newCache[entry.TreeEntry.Hash] = cached

		return nil
	}

	blob, err := blobCache.getBlob(&entry, commit.File)
	if err != nil {
		blobCache.l.Errorf("file to %s %s: %v\n", entry.Name, entry.TreeEntry.Hash, err)
		return err
	}

	err = blobCache.validateAndReserve(blob, budget)
	if blobCache.blobSkipped(err) {
		return blobCache.storePlaceholder(entry, blob.Size, err, cache, newCache)
	}

	if err != nil {
		return fmt.Errorf("cache new blob %q (%s): %w", entry.Name, entry.TreeEntry.Hash, err)
	}

	cached := &CachedBlob{Blob: *blob}

	err = cached.CacheWithLimit(blobCache.MaxBlobSize)
	if blobCache.blobSkipped(err) {
		// Defensive: validateAndReserve applies the same per-blob limit, so reaching this with an
		// oversize error would mean the two disagreed.
		return blobCache.storePlaceholder(entry, blob.Size, err, cache, newCache)
	}

	if err != nil {
		blobCache.l.Errorf("file to %s %s: %v\n", entry.Name, entry.TreeEntry.Hash, err)
		return err
	}

	cache[entry.TreeEntry.Hash] = cached
	newCache[entry.TreeEntry.Hash] = cached

	return nil
}

func (blobCache *BlobCache) cacheFrom(
	entry object.ChangeEntry,
	commit *object.Commit,
	cache map[plumbing.Hash]*CachedBlob,
	budget *commitBlobBudget,
	dummyWhenMissing bool,
) error {
	if cached, exists := blobCache.cache[entry.TreeEntry.Hash]; exists {
		err := blobCache.validateCachedAndReserve(cached, budget)
		if blobCache.blobSkipped(err) {
			return blobCache.storePlaceholder(entry, cached.Size, err, cache)
		}

		if err != nil {
			return err
		}

		cache[entry.TreeEntry.Hash] = cached

		return nil
	}

	blob, err := blobCache.getBlob(&entry, commit.File)
	if err != nil && dummyWhenMissing && errors.Is(err, plumbing.ErrObjectNotFound) {
		return blobCache.cacheDummyBlob(entry.TreeEntry.Hash, cache, budget)
	}

	if err != nil {
		blobCache.l.Errorf("file from %s %s: %v\n", entry.Name, entry.TreeEntry.Hash, err)
		return fmt.Errorf("load previous blob %q (%s): %w", entry.Name, entry.TreeEntry.Hash, err)
	}

	return blobCache.cacheFreshFrom(entry, blob, cache, budget)
}

func (blobCache *BlobCache) cacheFreshFrom(
	entry object.ChangeEntry,
	blob *object.Blob,
	cache map[plumbing.Hash]*CachedBlob,
	budget *commitBlobBudget,
) error {
	err := blobCache.validateAndReserve(blob, budget)
	if blobCache.blobSkipped(err) {
		return blobCache.storePlaceholder(entry, blob.Size, err, cache)
	}

	if err != nil {
		return fmt.Errorf("cache previous blob %q (%s): %w", entry.Name, entry.TreeEntry.Hash, err)
	}

	cached := &CachedBlob{Blob: *blob}

	err = cached.CacheWithLimit(blobCache.MaxBlobSize)
	if blobCache.blobSkipped(err) {
		// Defensive, as in cacheTo: the same per-blob limit was already applied above.
		return blobCache.storePlaceholder(entry, blob.Size, err, cache)
	}

	if err != nil {
		blobCache.l.Errorf("file from %s %s: %v\n", entry.Name, entry.TreeEntry.Hash, err)
		return err
	}

	cache[entry.TreeEntry.Hash] = cached

	return nil
}

func (blobCache *BlobCache) cacheDummyBlob(
	hash plumbing.Hash,
	cache map[plumbing.Hash]*CachedBlob,
	budget *commitBlobBudget,
) error {
	blob, err := internal.CreateDummyBlob(hash)
	if err != nil {
		return fmt.Errorf("create placeholder for missing blob %s: %w", hash, err)
	}

	err = blobCache.validateAndReserve(blob, budget)
	if err != nil {
		return err
	}

	cache[hash] = &CachedBlob{Blob: *blob}

	return nil
}

// blobSkipped decides whether err describes a blob which is too big to retain and which the
// configured policy tolerates. This is the single place where the skip-or-abort policy lives.
func (blobCache *BlobCache) blobSkipped(err error) bool {
	if err == nil || blobCache.FailOnOversizedBlobs {
		return false
	}

	return errors.Is(err, ErrBlobTooLarge) || errors.Is(err, ErrBlobCacheBudgetExceeded)
}

// oversizedBlobPlaceholder builds the stand-in which is stored instead of a blob that did not fit.
//
// Size is deliberately left at 0 rather than carrying the real size. RenameAnalysis classifies
// candidates by blob.Size (classifyRenameBlobs) and normalises binary similarity by
// Min64(blob1.Size, blob2.Size) (binaryBlobsAreClose); a huge declared size over a zero-byte
// payload would score ~100% similar against any file and manufacture bogus renames. At Size == 0 <
// RenameAnalysisMinimumSize the placeholder lands in the "small" bucket and is excluded from
// rename detection altogether. The true size is preserved in OriginalSize for diagnostics only.
func oversizedBlobPlaceholder(hash plumbing.Hash, originalSize int64) (*CachedBlob, error) {
	blob, err := internal.CreateDummyBlob(hash)
	if err != nil {
		return nil, fmt.Errorf("create placeholder for oversized blob %s: %w", hash, err)
	}

	return &CachedBlob{
		Blob:         *blob,
		Data:         nil,
		Oversized:    true,
		OriginalSize: originalSize,
	}, nil
}

// storePlaceholder records the skipped blob and installs its placeholder in every supplied map.
// No budget is reserved and the hash is not marked as seen, so smaller blobs later in the same
// commit still fit; change order is tree-diff order, which is deterministic.
func (blobCache *BlobCache) storePlaceholder(
	entry object.ChangeEntry,
	originalSize int64,
	cause error,
	caches ...map[plumbing.Hash]*CachedBlob,
) error {
	cached, err := oversizedBlobPlaceholder(entry.TreeEntry.Hash, originalSize)
	if err != nil {
		return err
	}

	blobCache.oversizeReporter().record(
		blobCache.l, entry.Name, entry.TreeEntry.Hash, originalSize, cause,
	)

	for _, cache := range caches {
		cache[entry.TreeEntry.Hash] = cached
	}

	return nil
}

// oversizeReporter lazily allocates the shared report so that a BlobCache constructed directly,
// as unit tests do, never dereferences nil.
func (blobCache *BlobCache) oversizeReporter() *oversizeReport {
	if blobCache.oversize == nil {
		blobCache.oversize = &oversizeReport{}
	}

	return blobCache.oversize
}

// oversizeReport counts the blobs skipped because they did not fit the cache limits.
//
// Following reportBurndownBalances, only the first offender is described in full: repeating an
// identical diagnostic at every site is noise. The remainder are counted and flushed once by
// Dispose(). The pointer is shared with every fork so a branching history still warns once; the
// mutex is insurance, since branch execution is sequential today but this package runs under -race.
type oversizeReport struct {
	mutex      sync.Mutex
	reported   bool
	summarised bool
	count      int
	totalBytes int64
	firstName  string
	firstHash  plumbing.Hash
	firstSize  int64
	firstCause error
}

func (report *oversizeReport) record(
	logger core.Logger, name string, hash plumbing.Hash, size int64, cause error,
) {
	report.mutex.Lock()

	report.count++
	report.totalBytes += size

	first := !report.reported
	if first {
		report.reported = true
		report.firstName = name
		report.firstHash = hash
		report.firstSize = size
		report.firstCause = cause
	}

	report.mutex.Unlock()

	if !first {
		return
	}

	if logger == nil {
		logger = core.NewLogger()
	}

	logger.Warnf(
		"blob %q (%s, %d bytes) %s; it was not read and is recorded as binary content, "+
			"so it is excluded from line counts, diffs, language detection and rename detection. "+
			"The run continues; pass --fail-on-oversized-blobs to abort instead.\n",
		name, hash.String(), size, oversizeLimitDescription(cause),
	)
}

func (report *oversizeReport) summarise(logger core.Logger) {
	report.mutex.Lock()

	if report.summarised || report.count <= 1 {
		report.mutex.Unlock()

		return
	}

	report.summarised = true
	count, totalBytes := report.count, report.totalBytes

	report.mutex.Unlock()

	if logger == nil {
		logger = core.NewLogger()
	}

	logger.Warnf(
		"%d blobs (%d bytes in total) exceeded the configured blob cache limits and were "+
			"recorded as binary content; the first one was reported above.\n",
		count, totalBytes,
	)
}

func oversizeLimitDescription(cause error) string {
	if errors.Is(cause, ErrBlobCacheBudgetExceeded) {
		return "does not fit the remaining --blob-cache-max-commit-size budget of its commit"
	}

	return "exceeds --blob-cache-max-blob-size"
}

func (blobCache *BlobCache) validateAndReserve(
	blob *object.Blob, budget *commitBlobBudget,
) error {
	if blob.Size < 0 {
		return fmt.Errorf("%w for %s: %d", errInvalidBlobSize, blob.Hash, blob.Size)
	}

	if blob.Size > blobCache.MaxBlobSize {
		return fmt.Errorf(
			"%w for %s: %d bytes exceeds %d",
			ErrBlobTooLarge, blob.Hash, blob.Size, blobCache.MaxBlobSize,
		)
	}

	return budget.reserve(blob.Hash, blob.Size)
}

func (blobCache *BlobCache) validateCachedAndReserve(
	blob *CachedBlob, budget *commitBlobBudget,
) error {
	if blob.Size > blobCache.MaxBlobSize {
		return fmt.Errorf(
			"%w for %s: %d bytes exceeds %d",
			ErrBlobTooLarge, blob.Hash, blob.Size, blobCache.MaxBlobSize,
		)
	}

	return budget.reserve(blob.Hash, blob.Size)
}

type commitBlobBudget struct {
	max  int64
	used int64
	seen map[plumbing.Hash]struct{}
}

func newCommitBlobBudget(maxBytes int64) *commitBlobBudget {
	return &commitBlobBudget{
		max:  maxBytes,
		seen: map[plumbing.Hash]struct{}{},
	}
}

func (budget *commitBlobBudget) reserve(hash plumbing.Hash, size int64) error {
	if _, exists := budget.seen[hash]; exists {
		return nil
	}

	if size < 0 {
		return fmt.Errorf("%w for %s: %d", errInvalidBlobSize, hash, size)
	}

	if size > budget.max-budget.used {
		return fmt.Errorf(
			"%w: adding %s (%d bytes) would exceed %d bytes",
			ErrBlobCacheBudgetExceeded, hash, size, budget.max,
		)
	}

	budget.used += size
	budget.seen[hash] = struct{}{}

	return nil
}

// FileGetter defines a function which loads the Git file by
// the specified path. The state can be arbitrary though here it always
// corresponds to the currently processed commit.
type FileGetter func(path string) (*object.File, error)

// Returns the blob which corresponds to the specified ChangeEntry.
func (blobCache *BlobCache) getBlob(entry *object.ChangeEntry, fileGetter FileGetter) (
	*object.Blob, error,
) {
	blob, err := blobCache.repository.BlobObject(entry.TreeEntry.Hash)
	if err == nil {
		return blob, nil
	}

	return blobCache.resolveMissingBlob(entry, fileGetter, err)
}

func (blobCache *BlobCache) resolveMissingBlob(
	entry *object.ChangeEntry, fileGetter FileGetter, blobErr error,
) (*object.Blob, error) {
	if !errors.Is(blobErr, plumbing.ErrObjectNotFound) {
		blobCache.l.Errorf("getBlob(%s)\n", entry.TreeEntry.Hash.String())

		return nil, fmt.Errorf("load blob %s: %w", entry.TreeEntry.Hash, blobErr)
	}

	if entry.TreeEntry.Mode != 0o160000 {
		return nil, fmt.Errorf("load blob %s: %w", entry.TreeEntry.Hash, blobErr)
	}

	if !blobCache.FailOnMissingSubmodules {
		return createPlaceholderBlob(entry.TreeEntry.Hash)
	}

	return resolveRegisteredSubmodule(entry, fileGetter, blobErr)
}

func resolveRegisteredSubmodule(
	entry *object.ChangeEntry, fileGetter FileGetter, blobErr error,
) (*object.Blob, error) {
	file, err := fileGetter(".gitmodules")
	if err != nil {
		return nil, fmt.Errorf(
			"load .gitmodules while resolving submodule %q: %w", entry.Name, err,
		)
	}

	contents, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf(
			"read .gitmodules while resolving submodule %q: %w", entry.Name, err,
		)
	}

	modules := config.NewModules()

	err = modules.Unmarshal([]byte(contents))
	if err != nil {
		return nil, fmt.Errorf(
			"parse .gitmodules while resolving submodule %q: %w", entry.Name, err,
		)
	}

	if _, exists := modules.Submodules[entry.Name]; exists {
		return createPlaceholderBlob(entry.TreeEntry.Hash)
	}

	return nil, fmt.Errorf(
		"submodule %q is not registered in .gitmodules: %w", entry.Name, blobErr,
	)
}

func createPlaceholderBlob(hash plumbing.Hash) (*object.Blob, error) {
	dummy, err := internal.CreateDummyBlob(hash)
	if err != nil {
		return nil, fmt.Errorf("create placeholder blob %s: %w", hash, err)
	}

	return dummy, nil
}

var _ = core.RegisterPipelineItem(&BlobCache{})
