//go:build windows

package sysinfo

// DiskTotalBytes is unsupported on Windows; the capability is simply omitted.
func DiskTotalBytes(path string) string {
	return ""
}
