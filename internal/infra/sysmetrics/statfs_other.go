//go:build !linux

package sysmetrics

import "errors"

// statFS is Linux-only; elsewhere the sample simply has no disks.
func statFS(string) (fsInfo, error) { return fsInfo{}, errors.New("statfs: unsupported platform") }
