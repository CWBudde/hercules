package main

import _ "embed"

// PluginTemplateSource is the source code template of a Hercules plugin.
// It is embedded at build time from plugin.template, so no `go generate`
// step is required before building.
//
//go:embed plugin.template
var PluginTemplateSource string
