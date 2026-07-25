package imports

import (
	"fmt"
	"runtime"
	"sync"

	"github.com/go-git/go-git/v5"
	gitplumbing "github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/imports/lang"
)

// Extractor reports the imports in the changed files.
type Extractor struct {
	core.NoopMerger

	// Goroutines is the number of goroutines to run for imports extraction.
	Goroutines int
	// MaxFileSize is the file size threshold. Files that exceed it are ignored.
	MaxFileSize int

	l core.Logger
}

const (
	// DependencyImports is the name of the dependency provided by Extractor.
	DependencyImports = "imports"
	// ConfigImportsGoroutines is the name of the configuration option for
	// Extractor.Configure() to set the number of parallel goroutines for imports extraction.
	ConfigImportsGoroutines = "Imports.Goroutines"
	// ConfigMaxFileSize is the name of the configuration option for
	// Extractor.Configure() to set the file size threshold after which they are ignored.
	ConfigMaxFileSize = "Imports.MaxFileSize"
	// DefaultMaxFileSize is the default value for Extractor.MaxFileSize.
	DefaultMaxFileSize = 1 << 20
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (ex *Extractor) Name() string {
	return "Imports"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (ex *Extractor) Provides() []string {
	return []string{DependencyImports}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (ex *Extractor) Requires() []string {
	return []string{plumbing.DependencyTreeChanges, plumbing.DependencyBlobCache}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (ex *Extractor) ListConfigurationOptions() []core.ConfigurationOption {
	return []core.ConfigurationOption{
		{
			Name:        ConfigImportsGoroutines,
			Description: "Specifies the number of goroutines to run in parallel for the imports extraction.",
			Flag:        "import-goroutines",
			Type:        core.IntConfigurationOption,
			Default:     runtime.NumCPU(),
		}, {
			Name:        ConfigMaxFileSize,
			Description: "Specifies the file size threshold. Files that exceed it are ignored.",
			Flag:        "import-max-file-size",
			Type:        core.IntConfigurationOption,
			Default:     DefaultMaxFileSize,
		},
	}
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (ex *Extractor) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		ex.l = l
	}

	if goroutineCount, exists := facts[ConfigImportsGoroutines].(int); exists {
		if goroutineCount < 1 {
			if ex.l != nil {
				ex.l.Warnf("invalid number of goroutines for the imports extraction: %d. Set to %d.",
					goroutineCount, runtime.NumCPU())
			}

			goroutineCount = runtime.NumCPU()
		}

		ex.Goroutines = goroutineCount
	}

	if size, exists := facts[ConfigMaxFileSize].(int); exists {
		if size <= 0 {
			if ex.l != nil {
				ex.l.Warnf("invalid maximum file size: %d. Set to %d.", size, DefaultMaxFileSize)
			}

			size = DefaultMaxFileSize
		}

		ex.MaxFileSize = size
	}

	return nil
}

func (*Extractor) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (ex *Extractor) Initialize(repository *git.Repository) error {
	ex.l = core.NewLogger()
	if ex.Goroutines < 1 {
		ex.Goroutines = runtime.NumCPU()
	}

	if ex.MaxFileSize == 0 {
		ex.MaxFileSize = DefaultMaxFileSize
	}

	return nil
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (ex *Extractor) Consume(deps map[string]any) (map[string]any, error) {
	changes := deps[plumbing.DependencyTreeChanges].(object.Changes)
	cache := deps[plumbing.DependencyBlobCache].(map[gitplumbing.Hash]*plumbing.CachedBlob)
	result := map[gitplumbing.Hash]lang.File{}
	jobs := make(chan *object.Change, ex.Goroutines)
	resultSync := sync.Mutex{}
	waitGroup := sync.WaitGroup{}
	waitGroup.Add(ex.Goroutines)

	for range ex.Goroutines {
		go ex.extractImports(jobs, cache, result, &resultSync, &waitGroup)
	}

	err := enqueueImportChanges(changes, jobs)
	close(jobs)
	waitGroup.Wait()
	if err != nil {
		return nil, err
	}

	return map[string]any{DependencyImports: result}, nil
}

func (ex *Extractor) extractImports(
	jobs <-chan *object.Change,
	cache map[gitplumbing.Hash]*plumbing.CachedBlob,
	result map[gitplumbing.Hash]lang.File,
	resultSync *sync.Mutex,
	waitGroup *sync.WaitGroup,
) {
	defer waitGroup.Done()

	for change := range jobs {
		blob := cache[change.To.TreeEntry.Hash]
		if blob.Size > int64(ex.MaxFileSize) {
			ex.l.Warnf("skipped %s %s: size is too big: %d > %d",
				change.To.TreeEntry.Name, change.To.TreeEntry.Hash.String(),
				blob.Size, ex.MaxFileSize)

			continue
		}

		file, err := lang.Extract(change.To.TreeEntry.Name, blob.Data)
		if err != nil {
			ex.l.Errorf("failed to extract imports from %s %s: %v",
				change.To.TreeEntry.Name, change.To.TreeEntry.Hash.String(), err)

			continue
		}

		resultSync.Lock()
		result[change.To.TreeEntry.Hash] = *file
		resultSync.Unlock()
	}
}

func enqueueImportChanges(changes object.Changes, jobs chan<- *object.Change) error {
	for _, change := range changes {
		action, err := change.Action()
		if err != nil {
			return fmt.Errorf(
				"determine action for import change %q -> %q: %w",
				change.From.Name,
				change.To.Name,
				err,
			)
		}

		switch action {
		case merkletrie.Modify, merkletrie.Insert:
			jobs <- change
		case merkletrie.Delete:
			continue
		}
	}

	return nil
}

// Fork clones this PipelineItem.
func (ex *Extractor) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(ex, n)
}

var _ = core.RegisterPipelineItem(&Extractor{})
