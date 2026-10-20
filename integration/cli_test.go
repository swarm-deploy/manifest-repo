package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swarm-deploy/manifest-repo/internal/result"
)

func TestSourceBuiltRenderInspectPublish(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	dir := t.TempDir()
	binary := filepath.Join(dir, "manifest-repo")
	runCommand(t, root, "go", "build", "-o", binary, "./cmd/manifest-repo")

	work := filepath.Join(dir, "desired-work")
	require.NoError(t, os.MkdirAll(filepath.Join(work, "applications"), 0o755))
	writeFile(t, filepath.Join(work, "applications", "api.yaml"), `name: api
services:
  api:
    image: registry.example/acme/api:v1
    x-init-deploy-jobs:
      - name: migrate
        image: registry.example/acme/migrations:old
`)
	runCommand(t, root, "git", "-C", work, "init", "-b", "main")
	runCommand(t, root, "git", "-C", work, "config", "user.name", "fixture")
	runCommand(t, root, "git", "-C", work, "config", "user.email", "fixture@example.invalid")
	runCommand(t, root, "git", "-C", work, "add", "applications/api.yaml")
	runCommand(t, root, "git", "-C", work, "commit", "-m", "initial")
	repository := filepath.Join(dir, "desired.git")
	runCommand(t, root, "git", "clone", "--bare", work, repository)

	writeFile(t, filepath.Join(dir, "migrations", "001.sql"), "create table example;\n")
	source := filepath.Join(dir, "source.yaml")
	writeSource(t, source, "api", "")
	renderedPath := filepath.Join(dir, "rendered.yaml")
	render := func(output string) result.Result {
		t.Helper()
		return runCLI(t, root, binary, "render",
			"--source", source, "--output", output, "--repo", repository,
			"--branch", "main", "--stack", "api", "--template", "--tag", "v2",
			"--workspace", dir, "--source-repository-url", "https://github.com/acme/api",
			"--global-env-files", "env/common.env\nenv/production.env",
		)
	}
	publish := func(input string) result.Result {
		t.Helper()
		return runCLI(t, root, binary, "publish",
			"--source", input, "--rendered", "--repo", repository,
			"--branch", "main", "--stack", "api", "--message", "publish api",
			"--global-env-files", "env/common.env\nenv/production.env",
			"--validate-compose=false",
		)
	}

	first := render(renderedPath)
	assert.True(t, first.Changed)
	assert.Equal(t, "render", first.Operation)
	initJob := changeByID(first.Changes, "init-job/api/migrate")
	assert.Equal(t, "updated", initJob.Change)
	assert.Equal(t, []string{"image"}, initJob.ChangedFields)
	assert.Equal(t, "registry.example/acme/migrations:old", initJob.Details["image"].Before)
	assert.Regexp(t, `^registry\.example/acme/migrations:[0-9a-f]{64}$`, initJob.Details["image"].After)
	buildMarker := filepath.Join(dir, "migration-build-ran")
	if imageChanged(first.Changes, "init-job/api/migrate") {
		writeFile(t, buildMarker, "built")
	}
	assert.FileExists(t, buildMarker)
	rendered, err := os.ReadFile(renderedPath)
	require.NoError(t, err)
	assert.Contains(t, string(rendered), "image: registry.example/acme/api:v2")
	assert.Contains(t, string(rendered), "image: registry.example/acme/migrations:")
	assert.Contains(t, string(rendered), "org.swarm_deploy.github_repository: https://github.com/acme/api")
	assert.Equal(t, 1, strings.Count(string(rendered), "../env/common.env"))
	assert.Equal(t, 1, strings.Count(string(rendered), "../env/production.env"))

	second := publish(renderedPath)
	assert.True(t, second.Changed)
	assert.Equal(t, first.Changes, second.Changes)
	assert.NotEqual(t, first.DesiredState.Revision, second.DesiredState.Revision)
	published := runCommand(t, root, "git", "--git-dir", repository, "show", "main:applications/api.yaml")
	assert.Equal(t, 1, strings.Count(string(published), "../env/common.env"))
	assert.Equal(t, 1, strings.Count(string(published), "../env/production.env"))

	third := render(filepath.Join(dir, "unchanged.yaml"))
	assert.False(t, third.Changed)
	assert.Empty(t, third.Changes)
	if imageChanged(third.Changes, "init-job/api/migrate") {
		writeFile(t, filepath.Join(dir, "unexpected-build"), "built")
	}
	assert.NoFileExists(t, filepath.Join(dir, "unexpected-build"))
	assert.Equal(t, second.DesiredState.Revision, third.DesiredState.Revision)

	writeSource(t, source, "renamed", "x-policy: {token: secret}\n")
	topLevel := render(filepath.Join(dir, "top-level.yaml"))
	assert.True(t, topLevel.Changed)
	assert.Equal(t, "updated", changeByID(topLevel.Changes, "top-level/name").Change)
	assert.Equal(t, "added", changeByID(topLevel.Changes, "top-level/x-policy").Change)
	assert.False(t, imageChanged(topLevel.Changes, "init-job/api/migrate"))
	assert.NotContains(t, mustJSON(t, topLevel), "secret")
	topLevelPublished := publish(filepath.Join(dir, "top-level.yaml"))
	assert.True(t, topLevelPublished.Changed)
	assert.Equal(t, topLevel.Changes, topLevelPublished.Changes)
	assert.NotEqual(t, second.DesiredState.Revision, topLevelPublished.DesiredState.Revision)
}

func writeSource(t *testing.T, path, name, extension string) {
	t.Helper()
	writeFile(t, path, "name: "+name+"\n"+extension+`services:
  api:
    image: registry.example/acme/api:{{ .Tag }}
    x-init-deploy-jobs:
      - name: migrate
        image: registry.example/acme/migrations:{{ hashFiles "migrations/**" }}
`)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func runCommand(t *testing.T, dir, name string, args ...string) []byte {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s %v: %s", name, args, output)
	return output
}

func runCLI(t *testing.T, dir, binary string, args ...string) result.Result {
	t.Helper()
	output := runCommand(t, dir, binary, args...)
	var value result.Result
	require.NoError(t, json.Unmarshal(output, &value))
	return value
}

func imageChanged(changes []result.Change, id string) bool {
	for _, change := range changes {
		if change.ID != id {
			continue
		}
		for _, field := range change.ChangedFields {
			if field == "image" {
				return true
			}
		}
	}
	return false
}

func changeByID(changes []result.Change, id string) result.Change {
	for _, change := range changes {
		if change.ID == id {
			return change
		}
	}
	return result.Change{}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}
