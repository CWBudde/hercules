//go:build linux

package main

import "golang.org/x/sys/unix"

func atomicInstallCacheDirectory(stagingPath, destinationPath string) error {
	return unix.Renameat2(
		unix.AT_FDCWD,
		stagingPath,
		unix.AT_FDCWD,
		destinationPath,
		unix.RENAME_NOREPLACE,
	)
}

func atomicSwapCacheDirectories(stagingPath, destinationPath string) error {
	return unix.Renameat2(
		unix.AT_FDCWD,
		stagingPath,
		unix.AT_FDCWD,
		destinationPath,
		unix.RENAME_EXCHANGE,
	)
}
