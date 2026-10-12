//go:build linux

package sysmetrics

import "syscall"

// statFS reports the filesystem holding path: its device (to dedupe two
// paths on one filesystem) and used/total bytes as df counts them.
func statFS(path string) (fsInfo, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return fsInfo{}, err
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return fsInfo{}, err
	}
	bsize := uint64(fs.Bsize)
	total := fs.Blocks * bsize
	used := (fs.Blocks - fs.Bfree) * bsize
	return fsInfo{dev: uint64(st.Dev), used: used, total: total}, nil
}
