package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestActionOutputOwnership(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", workspace)
	output := filepath.Join(workspace, ".manifest-repo", "nested", "rendered.yaml")
	restore, err := prepareActionOutputOwnership(output)
	if err != nil {
		t.Fatal(err)
	}
	if restore == nil {
		t.Fatal("missing ownership tracking")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := restore.restore(); err != nil {
		t.Fatal(err)
	}
	ws, _ := os.Stat(workspace)
	expected := ws.Sys().(*syscall.Stat_t)
	for _, p := range []string{filepath.Dir(output), filepath.Dir(filepath.Dir(output)), output} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		st := fi.Sys().(*syscall.Stat_t)
		if st.Uid != expected.Uid || st.Gid != expected.Gid {
			t.Fatalf("%s has %d:%d want %d:%d", p, st.Uid, st.Gid, expected.Uid, expected.Gid)
		}
	}
	// Emulates a second render overwriting an existing output.
	second, err := prepareActionOutputOwnership(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("again"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := second.restore(); err != nil {
		t.Fatal(err)
	}
}

func TestActionOutputOwnershipFailedRender(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", workspace)
	output := filepath.Join(workspace, "custom", "nested", "result.yaml")
	ownership, err := prepareActionOutputOwnership(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		t.Fatal(err)
	}
	if err := ownership.restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(output)); err != nil {
		t.Fatal(err)
	}
}

func TestActionOutputOwnershipPartialDirectoryCreation(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", workspace)
	output := filepath.Join(workspace, "partial", "never-created", "rendered.yaml")
	owner, err := prepareActionOutputOwnership(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "partial"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := owner.restore(); err != nil {
		t.Fatal(err)
	}
}

func TestActionOutputOwnershipSafety(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", workspace)
	if owner, err := prepareActionOutputOwnership(filepath.Join(outside, "file")); err != nil || owner != nil {
		t.Fatalf("outside workspace: %v %v", owner, err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareActionOutputOwnership(filepath.Join(workspace, "link", "file")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection: %v", err)
	}
	target := filepath.Join(outside, "file")
	if err := os.WriteFile(target, []byte("untouched"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(workspace, "result")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareActionOutputOwnership(filepath.Join(workspace, "result")); err == nil {
		t.Fatal("output symlink should be rejected")
	}
}

func TestActionOutputOwnershipDisabledOutsideActions(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "false")
	t.Setenv("GITHUB_WORKSPACE", t.TempDir())
	owner, err := prepareActionOutputOwnership("nested/file")
	if err != nil || owner != nil {
		t.Fatalf("outside Actions: %v %v", owner, err)
	}
}

func TestActionOutputOwnershipRootCreatesFilesForRunner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to simulate a Docker Action")
	}
	workspace := t.TempDir()
	const uid, gid = 12345, 12345
	if err := os.Chown(workspace, uid, gid); err != nil {
		t.Skipf("cannot simulate runner UID: %v", err)
	}
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", workspace)
	output := filepath.Join(workspace, ".manifest-repo", "rendered.yaml")
	owner, err := prepareActionOutputOwnership(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("rendered"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := owner.restore(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Dir(output), output} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid != uid || stat.Gid != gid {
			t.Fatalf("%s owned by %d:%d", path, stat.Uid, stat.Gid)
		}
	}
}
