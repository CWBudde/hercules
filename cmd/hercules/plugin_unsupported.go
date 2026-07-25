//go:build !cgo || (!linux && !darwin && !freebsd)

package main

import "log"

func loadPlugin(path string) {
	log.Printf("Failed to load plugin from %s: Go plugins are not supported by this build\n", path)
}
