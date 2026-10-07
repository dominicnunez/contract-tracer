package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func rejectOutputSnapshotAlias(output, snapshot, snapshotFlag string) error {
	if output == "" || snapshot == "" {
		return nil
	}
	same, err := pathsAlias(output, snapshot)
	if err != nil {
		return fmt.Errorf("cannot safely compare -output %q with -%s %q: %w", output, snapshotFlag, snapshot, err)
	}
	if same {
		return fmt.Errorf("-output %q aliases -%s %q; choose distinct paths", output, snapshotFlag, snapshot)
	}
	return nil
}

func pathsAlias(first, second string) (bool, error) {
	firstPath, firstInfo, firstExists, err := canonicalProspectivePath(first)
	if err != nil {
		return false, err
	}
	secondPath, secondInfo, secondExists, err := canonicalProspectivePath(second)
	if err != nil {
		return false, err
	}
	if firstExists && secondExists && os.SameFile(firstInfo, secondInfo) {
		return true, nil
	}
	if firstExists && secondExists {
		return false, nil
	}
	if firstPath == secondPath {
		return true, nil
	}
	if runtime.GOOS == "windows" && strings.EqualFold(firstPath, secondPath) {
		return true, nil
	}
	return false, nil
}

// canonicalProspectivePath resolves existing symlinked ancestors even when
// the final file has not been created yet. The caller relies on a stable
// filesystem namespace while it preflights and writes the selected paths.
func canonicalProspectivePath(path string) (string, os.FileInfo, bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, false, err
	}
	abs = filepath.Clean(abs)
	if info, statErr := os.Stat(abs); statErr == nil {
		resolved, resolveErr := filepath.EvalSymlinks(abs)
		if resolveErr != nil {
			return "", nil, false, resolveErr
		}
		return filepath.Clean(resolved), info, true, nil
	} else if !os.IsNotExist(statErr) {
		return "", nil, false, statErr
	}

	current := abs
	var suffix []string
	for {
		if _, lstatErr := os.Lstat(current); lstatErr == nil {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", nil, false, resolveErr
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil, false, nil
		} else if !os.IsNotExist(lstatErr) {
			return "", nil, false, lstatErr
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", nil, false, fmt.Errorf("no existing ancestor for %q", path)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
