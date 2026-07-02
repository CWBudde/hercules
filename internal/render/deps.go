//go:build renderdeps

// Package render will host the Go renderer (labours-go) starting with
// PLAN.md Phase 4. Until then, this file anchors the render-stack modules
// in the module graph (Phase 2) so that `go mod tidy` and `go mod vendor`
// retain them before any renderer code is moved. The renderdeps build tag
// is never set, so nothing here is ever compiled.
package render

import (
	_ "github.com/cwbudde/matplotlib-go/backends"
	_ "github.com/cwbudde/matplotlib-go/backends/agg"
	_ "github.com/cwbudde/matplotlib-go/backends/svg"
	_ "github.com/cwbudde/matplotlib-go/color"
	_ "github.com/cwbudde/matplotlib-go/core"
	_ "github.com/cwbudde/matplotlib-go/render"
	_ "github.com/cwbudde/matplotlib-go/style"
)
