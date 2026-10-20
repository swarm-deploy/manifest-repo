package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// actionOutputOwnership tracks only paths written into the GitHub Actions workspace.
// Files outside it and normal CLI invocations are deliberately left unchanged.
type actionOutputOwnership struct {
	workspace   string
	output      string
	createdDirs []string
	uid, gid    int
}

func prepareActionOutputOwnership(output string) (*actionOutputOwnership, error) {
	workspace := os.Getenv("GITHUB_WORKSPACE")
	if os.Getenv("GITHUB_ACTIONS") != "true" || workspace == "" || output == "" {
		return nil, nil
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve GitHub workspace: %w", err)
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return nil, fmt.Errorf("resolve output path: %w", err)
	}
	rel, err := filepath.Rel(workspace, output)
	if err != nil {
		return nil, err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, nil
	}

	info, err := os.Stat(workspace)
	if err != nil {
		return nil, fmt.Errorf("stat GitHub workspace: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return nil, fmt.Errorf("cannot read GitHub workspace ownership")
	}
	owner := &actionOutputOwnership{workspace: workspace, output: output, uid: int(stat.Uid), gid: int(stat.Gid)}
	for dir := filepath.Dir(output); dir != workspace; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		switch {
		case errors.Is(err, os.ErrNotExist):
			owner.createdDirs = append(owner.createdDirs, dir)
		case err != nil:
			return nil, fmt.Errorf("inspect output directory %q: %w", dir, err)
		case !info.IsDir():
			return nil, fmt.Errorf("output directory %q is not a directory or is a symlink", dir)
		}
	}
	if info, err := os.Lstat(output); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("output %q is a symlink", output)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect output %q: %w", output, err)
	}
	return owner, nil
}

// restore runs even after a failed render/publish. Never chown the workspace,
// existing parent directories or paths reached through symlinks.
func (o *actionOutputOwnership) restore() error {
	if o == nil {
		return nil
	}
	// Re-check every parent before modifying ownership, including parents that
	// existed before the operation; they must not have become symlinks.
	for dir := filepath.Dir(o.output); dir != o.workspace; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect output directory %q: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("output directory %q is not a directory or is a symlink", dir)
		}
	}
	for i := len(o.createdDirs) - 1; i >= 0; i-- {
		if err := o.chownIfPresent(o.createdDirs[i]); err != nil {
			return err
		}
	}
	return o.chownIfPresent(o.output)
}

func (o *actionOutputOwnership) chownIfPresent(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect output %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to change ownership of symlink %q", path)
	}
	if path == o.output && !info.Mode().IsRegular() {
		return fmt.Errorf("output %q is not a regular file", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read ownership of %q", path)
	}
	if int(stat.Uid) == o.uid && int(stat.Gid) == o.gid {
		return nil
	}
	if err := os.Chown(path, o.uid, o.gid); err != nil {
		return fmt.Errorf("restore GitHub workspace ownership of %q: %w", path, err)
	}
	return nil
}
