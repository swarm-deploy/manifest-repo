package publisher

import (
	"encoding/base64"
	"path/filepath"
	"strings"
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

	env := gitAuthEnv("secret-token", "https://git.example.com/acme/deploy.git?ignored=yes#fragment")
	if len(env) != 3 {
		t.Fatalf("gitAuthEnv() returned %d entries, want 3", len(env))
	}
	if env[1] != "GIT_CONFIG_KEY_0=http.https://git.example.com/acme/deploy.git.extraHeader" {
		t.Fatalf("auth config is not repository scoped: %q", env[1])
	}
	if strings.Contains(env[1], "http.extraHeader") {
		t.Fatalf("auth config uses global http.extraHeader: %q", env[1])
	}
	wantCredentials := base64.StdEncoding.EncodeToString([]byte("x-access-token:secret-token"))
	if env[2] != "GIT_CONFIG_VALUE_0=Authorization: Basic "+wantCredentials {
		t.Fatalf("unexpected authorization header: %q", env[2])
	}
}

func TestGitAuthEnvSkipsNonHTTPRepositories(t *testing.T) {
	t.Parallel()

	for _, repository := range []string{
		"git@github.com:acme/deploy.git",
		"ssh://git@github.com/acme/deploy.git",
		"file:///tmp/deploy.git",
		"../deploy.git",
	} {
		if env := gitAuthEnv("secret-token", repository); env != nil {
			t.Fatalf("gitAuthEnv(%q) = %#v, want nil", repository, env)
		}
	}
}
