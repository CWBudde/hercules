//go:build !linux && !darwin && !windows

package main

func atomicInstallCacheDirectory(_, _ string) error {
	return errAtomicCacheReplacement
}

func atomicSwapCacheDirectories(_, _ string) error {
	return errAtomicCacheReplacement
}
