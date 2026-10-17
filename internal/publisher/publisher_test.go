package publisher

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swarm-deploy/manifest-repo/internal/manifest"
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

func TestProvenanceRepositoryRemovesURLCredentials(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "https://git.example.com/acme/deploy.git", provenanceRepository("https://user:secret@git.example.com/acme/deploy.git?token=secret#fragment"))
	assert.Equal(t, "./local.git", provenanceRepository("./local.git"))
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

func TestRenderAndPublishReturnTheSameSemanticDiff(t *testing.T) {
	desiredRepo := newDesiredRepository(t, `services:
  api:
    image: registry.example/applications/api/api:v1
    x-init-deploy-jobs:
      - name: migrate
        image: registry.example/applications/api/migrations:v1
`)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	writeTestFile(t, source, `services:
  api:
    image: registry.example/applications/api/api:v2
    x-init-deploy-jobs:
      - name: migrate
        image: registry.example/applications/api/migrations:v2
`)
	beforeRevision := repositoryRevision(t, desiredRepo)
	cfg := Config{
		SourceFile: source,
		OutputFile: filepath.Join(dir, "rendered.yaml"),
		Tag:        "unused-without-template",
		RenderOptions: manifest.RenderOptions{
			SourceRepositoryURL: "https://github.com/acme/api",
		},
		Repository:       desiredRepo,
		Branch:           "main",
		TargetFile:       "applications/api.yaml",
		Stack:            "api",
		Mode:             manifest.MergeModeMerge,
		CommitMessage:    "publish api",
		SourceRepository: "https://github.com/acme/api",
		SourceRevision:   "source-sha",
	}

	rendered, err := Render(cfg)
	require.NoError(t, err)
	assert.True(t, rendered.Changed)
	assert.Equal(t, "render", rendered.Operation)
	assert.Equal(t, beforeRevision, repositoryRevision(t, desiredRepo), "render must not push")
	assert.Equal(t, "source-sha", rendered.Source.Revision)
	renderedManifest, err := os.ReadFile(cfg.OutputFile)
	require.NoError(t, err)
	assert.Contains(t, string(renderedManifest), "image: registry.example/applications/api/api:v2")
	assert.Contains(t, string(renderedManifest), "image: registry.example/applications/api/migrations:v2")
	assert.Contains(t, string(renderedManifest), "org.swarm_deploy.github_repository: https://github.com/acme/api")

	published, err := Publish(cfg)
	require.NoError(t, err)
	assert.True(t, published.Changed)
	assert.Equal(t, "publish", published.Operation)
	assert.Equal(t, rendered.Changes, published.Changes)
	assert.NotEqual(t, beforeRevision, published.DesiredState.Revision)
	assert.Equal(t, published.DesiredState.Revision, repositoryRevision(t, desiredRepo))

	unchanged, err := Render(cfg)
	require.NoError(t, err)
	assert.False(t, unchanged.Changed)
	assert.Empty(t, unchanged.Changes)
}

func TestPublishAlreadyRenderedManifestSkipsTemplateAndLabelRendering(t *testing.T) {
	desiredRepo := newDesiredRepository(t, `services:
  api:
    image: registry/api:old
`)
	dir := t.TempDir()
	renderedSource := filepath.Join(dir, "rendered.yaml")
	writeTestFile(t, renderedSource, `services:
  api:
    image: "{{ .missing }}"
`)

	resultValue, err := Publish(Config{
		SourceFile:       renderedSource,
		SourceIsRendered: true,
		Template:         true,
		RenderOptions: manifest.RenderOptions{
			SourceRepositoryURL: "https://github.com/acme/api",
		},
		Repository:    desiredRepo,
		Branch:        "main",
		TargetFile:    "applications/api.yaml",
		Stack:         "api",
		Mode:          manifest.MergeModeMerge,
		CommitMessage: "publish rendered",
	})
	require.NoError(t, err)
	assert.True(t, resultValue.Changed)

	published, err := outputCommand("", nil, "git", "--git-dir", desiredRepo, "show", "main:applications/api.yaml")
	require.NoError(t, err)
	assert.Contains(t, published, `image: "{{ .missing }}"`)
}

func newDesiredRepository(t *testing.T, manifestContent string) string {
	t.Helper()
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	require.NoError(t, os.MkdirAll(filepath.Join(work, "applications"), 0o755))
	writeTestFile(t, filepath.Join(work, "applications", "api.yaml"), manifestContent)
	require.NoError(t, runGit(work, nil, "init", "-b", "main"))
	require.NoError(t, runGit(work, nil, "config", "user.name", "fixture"))
	require.NoError(t, runGit(work, nil, "config", "user.email", "fixture@example.invalid"))
	require.NoError(t, runGit(work, nil, "add", "applications/api.yaml"))
	require.NoError(t, runGit(work, nil, "commit", "-m", "initial"))
	bare := filepath.Join(dir, "desired.git")
	require.NoError(t, runGit("", nil, "clone", "--bare", work, bare))
	return bare
}

func repositoryRevision(t *testing.T, repository string) string {
	t.Helper()
	revision, err := outputCommand("", nil, "git", "--git-dir", repository, "rev-parse", "main")
	require.NoError(t, err)
	return strings.TrimSpace(revision)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
