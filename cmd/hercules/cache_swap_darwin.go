//go:build darwin

package main

import "golang.org/x/sys/unix"

func atomicInstallCacheDirectory(stagingPath, destinationPath string) error {
	return unix.RenameatxNp(
		unix.AT_FDCWD,
		stagingPath,
		unix.AT_FDCWD,
		destinationPath,
		unix.RENAME_EXCL,
	)
}

func atomicSwapCacheDirectories(stagingPath, destinationPath string) error {
	return unix.RenameatxNp(
		unix.AT_FDCWD,
		stagingPath,
		unix.AT_FDCWD,
		destinationPath,
		unix.RENAME_SWAP,
	)
}
