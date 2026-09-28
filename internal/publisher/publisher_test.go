package publisher

import (
	"path/filepath"
	"testing"
)

func TestRepositoryURL(t *testing.T) {
	t.Parallel()

	if got := repositoryURL("acme/deploy"); got != "https://github.com/acme/deploy.git" {
		t.Fatalf("repositoryURL() = %q", got)
	}
	if got := repositoryURL("file:///tmp/deploy.git"); got != "file:///tmp/deploy.git" {
		t.Fatalf("repositoryURL(file) = %q", got)
	}
}

func TestSafeTargetPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	got, err := safeTargetPath(root, "applications/core.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "applications", "core.yaml")
	if got != want {
		t.Fatalf("safeTargetPath() = %q, want %q", got, want)
	}

	for _, value := range []string{"../escape.yaml", "/absolute.yaml"} {
		if _, err := safeTargetPath(root, value); err == nil {
			t.Fatalf("safeTargetPath(%q) expected error", value)
		}
	}
}

func TestGitAuthEnv(t *testing.T) {
	t.Parallel()

	env := gitAuthEnv("secret-token")
	if len(env) != 3 {
		t.Fatalf("gitAuthEnv() returned %d entries, want 3", len(env))
	}
	for _, entry := range env {
		if entry == "secret-token" {
			t.Fatalf("raw token unexpectedly exposed as environment entry")
		}
	}
}
