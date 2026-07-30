package render

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/hercules/internal/render/graphics"
	renderModes "github.com/cwbudde/hercules/internal/render/modes"
	"github.com/cwbudde/hercules/internal/render/readers"
)

func TestParallelRenderersKeepOppositeOptionsIsolated(t *testing.T) {
	oldHandler := modeHandlers["devs-parallel"]
	t.Cleanup(func() { modeHandlers["devs-parallel"] = oldHandler })

	type observation struct {
		output string
		opts   renderModes.Options
	}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	observations := make(chan observation, 2)
	modeHandlers["devs-parallel"] = func(
		_ readers.Reader,
		output string,
		_, _ *time.Time,
		opts renderModes.Options,
	) error {
		started <- struct{}{}
		<-release
		observations <- observation{output: output, opts: opts}
		return nil
	}

	first := DefaultOptions()
	first.Output = filepath.Join(t.TempDir(), "first")
	first.Backend = "svg"
	first.Quiet = true
	first.MaxPeople = 3
	first.DevsParallelFallback = true
	first.NoBurndownTitle = true
	first.Theme = cloneTheme(graphics.DarkTheme)

	second := DefaultOptions()
	second.Output = filepath.Join(t.TempDir(), "second")
	second.Backend = "png"
	second.Quiet = true
	second.MaxPeople = 91
	second.DevsParallelFallback = false
	second.Theme = graphics.MinimalTheme

	firstRenderer, err := NewRenderer(first)
	if err != nil {
		t.Fatal(err)
	}
	secondRenderer, err := NewRenderer(second)
	if err != nil {
		t.Fatal(err)
	}

	// NewRenderer snapshots mutable option members instead of retaining them.
	expectedDarkRed := first.Theme.ColorPalette[0].R
	first.Theme.ColorPalette[0].R = 0

	var wait sync.WaitGroup
	wait.Add(2)
	results := make(chan Result, 2)
	go func() {
		defer wait.Done()
		results <- firstRenderer.Render(stubReader{}, []string{"devs-parallel"})
	}()
	go func() {
		defer wait.Done()
		results <- secondRenderer.Render(stubReader{}, []string{"devs-parallel"})
	}()

	<-started
	<-started
	close(release)
	wait.Wait()
	close(results)
	for result := range results {
		err := result.Err()
		if err != nil {
			t.Fatalf("parallel render failed: %v", err)
		}
	}

	seen := make(map[string]observation, 2)
	for range 2 {
		observation := <-observations
		seen[filepath.Ext(observation.output)] = observation
	}

	svg := seen[".svg"]
	if !svg.opts.DevsParallelFallback || svg.opts.MaxPeople != 3 ||
		svg.opts.Graphics.Theme.Name != "dark" || !svg.opts.Graphics.HideTitle {
		t.Fatalf("SVG renderer options leaked: %+v", svg.opts)
	}
	if svg.opts.Graphics.Theme.ColorPalette[0].R != expectedDarkRed {
		t.Fatal("renderer retained the caller's mutable theme palette")
	}

	png := seen[".png"]
	if png.opts.DevsParallelFallback || png.opts.MaxPeople != 91 ||
		png.opts.Graphics.Theme.Name != "minimal" || png.opts.Graphics.HideTitle {
		t.Fatalf("PNG renderer options leaked: %+v", png.opts)
	}
}
