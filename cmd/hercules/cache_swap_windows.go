//go:build windows

package main

import "os"

func atomicInstallCacheDirectory(stagingPath, destinationPath string) error {
	// os.Rename on Windows does not replace an existing directory.
	return os.Rename(stagingPath, destinationPath)
}

func atomicSwapCacheDirectories(_, _ string) error {
	return errAtomicCacheReplacement
}
