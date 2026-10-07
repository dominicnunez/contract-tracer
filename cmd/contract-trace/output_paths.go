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
	firstPath, err := inspectOutputPath(first)
	if err != nil {
		return false, err
	}
	secondPath, err := inspectOutputPath(second)
	if err != nil {
		return false, err
	}
	if firstPath.exists && secondPath.exists {
		return os.SameFile(firstPath.file, secondPath.file), nil
	}
	if firstPath.exists || secondPath.exists {
		return false, nil
	}
	if !os.SameFile(firstPath.parent, secondPath.parent) {
		return false, nil
	}
	if firstPath.leaf == secondPath.leaf {
		return true, nil
	}
	if runtime.GOOS == "windows" && strings.EqualFold(firstPath.leaf, secondPath.leaf) {
		return true, nil
	}
	return false, nil
}

type outputPathIdentity struct {
	file   os.FileInfo
	parent os.FileInfo
	leaf   string
	exists bool
}

// inspectOutputPath lets the operating system resolve the original path,
// including symlink/.. components. A missing leaf is identified by its
// existing parent directory and leaf name; missing parents cannot be written
// by the current output and snapshot writers, so those paths fail closed.
func inspectOutputPath(path string) (outputPathIdentity, error) {
	if runtime.GOOS == "windows" {
		_, leaf := filepath.Split(path)
		if strings.HasSuffix(leaf, ".") || strings.HasSuffix(leaf, " ") || strings.Contains(leaf, ":") {
			return outputPathIdentity{}, fmt.Errorf("%q has an ambiguous Windows file name; use a name without a trailing dot, trailing space, or colon", path)
		}
	}

	if info, err := os.Stat(path); err == nil {
		return outputPathIdentity{file: info, exists: true}, nil
	} else if !os.IsNotExist(err) {
		return outputPathIdentity{}, err
	}

	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return outputPathIdentity{}, fmt.Errorf("%q is a dangling or unresolvable symbolic link", path)
		}
	} else if !os.IsNotExist(err) {
		return outputPathIdentity{}, err
	}

	directory, leaf := filepath.Split(path)
	if leaf == "" {
		return outputPathIdentity{}, fmt.Errorf("%q has no file name", path)
	}
	if directory == "" {
		directory = "."
	}
	parent, err := os.Stat(directory)
	if err != nil {
		return outputPathIdentity{}, fmt.Errorf("resolve parent of %q: %w", path, err)
	}
	if !parent.IsDir() {
		return outputPathIdentity{}, fmt.Errorf("parent of %q is not a directory", path)
	}
	return outputPathIdentity{parent: parent, leaf: leaf}, nil
}
