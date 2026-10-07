//go:build !windows

package contracttrace

func hasPathReparsePoint(string) bool { return false }
