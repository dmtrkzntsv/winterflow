//go:build !windows

package sysinfo

import (
	"strconv"
	"syscall"
)

// DiskTotalBytes returns the total size in bytes of the filesystem containing
// path ("" on failure). Uses statfs, so it reflects the mount the agent's
// data lives on.
func DiskTotalBytes(path string) string {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return ""
	}
	return strconv.FormatUint(fs.Blocks*uint64(fs.Bsize), 10)
}
