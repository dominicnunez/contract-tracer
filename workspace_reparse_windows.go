//go:build windows

package contracttrace

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func hasPathReparsePoint(path string) bool {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	volume := filepath.VolumeName(absolute)
	if volume == "" {
		return true
	}
	current := volume + string(filepath.Separator)
	rootInfo, err := os.Lstat(current)
	if err != nil || isReparsePoint(rootInfo) {
		return true
	}
	remainder := strings.TrimLeft(absolute[len(volume):], string(filepath.Separator))
	for _, component := range strings.Split(remainder, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || isReparsePoint(info) {
			return true
		}
	}
	return false
}

func isReparsePoint(info os.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return !ok || data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
