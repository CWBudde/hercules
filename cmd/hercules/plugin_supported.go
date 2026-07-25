//go:build cgo && (linux || darwin || freebsd)

package main

import (
	"log"
	"plugin"
)

func loadPlugin(path string) {
	_, err := plugin.Open(path)
	if err != nil {
		log.Printf("Failed to load plugin from %s %s\n", path, err)
	}
}
