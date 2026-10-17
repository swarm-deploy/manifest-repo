package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPrepareFileTemplatesWholeInputBeforeYAMLParsing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "migrations", "001.sql"), "create table example;")
	source := filepath.Join(dir, "source.yaml")
	output := filepath.Join(dir, "output.yaml")
	mustWrite(t, source, `services:
  api:
    image: "registry.example/acme/api:{{ .Tag }}-{{ hashFiles "migrations/**" }}"
    x-init-deploy-jobs:
      - name: migrate
        image: "registry.example/acme/migrations:{{ hashFiles "migrations/**" }}"
`)

	require.NoError(t, PrepareFile(source, output, PrepareOptions{
		Template: TemplateOptions{Enabled: true, WorkspaceRoot: dir, Tag: "v2"},
	}))

	data, err := os.ReadFile(output)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, yaml.Unmarshal(data, &document))
	image := document["services"].(map[string]any)["api"].(map[string]any)["image"].(string)
	assert.Regexp(t, `^registry.example/acme/api:v2-[0-9a-f]{64}$`, image)
	job := document["services"].(map[string]any)["api"].(map[string]any)["x-init-deploy-jobs"].([]any)[0].(map[string]any)
	assert.Regexp(t, `^registry.example/acme/migrations:[0-9a-f]{64}$`, job["image"])
	assert.NotContains(t, string(data), "hashFiles")
}

func TestExecuteTemplateFileErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		message string
	}{
		{name: "syntax", input: `services: {{`, message: "parse template"},
		{name: "missing key", input: `services: {{ .missing }}`, message: `map has no entry for key "missing"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source := filepath.Join(dir, "source.yaml")
			mustWrite(t, source, test.input)
			err := ExecuteTemplateFile(source, filepath.Join(dir, "output.yaml"), dir, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.message)
		})
	}
}

func TestHashFilesPatternsAndDeterminism(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module example\n")
	mustWrite(t, filepath.Join(dir, "go.sum"), "dependency\n")
	mustWrite(t, filepath.Join(dir, "internal", "api", "main.go"), "package api\n")
	mustWrite(t, filepath.Join(dir, "internal", "generated", "ignored.go"), "ignored\n")

	patterns := []string{"go.mod", "go.sum", "internal/**", "!internal/generated/**"}
	first, err := HashFiles(dir, patterns...)
	require.NoError(t, err)
	second, err := HashFiles(dir, "internal/**", "go.sum", "go.mod", "!internal/generated/**")
	require.NoError(t, err)

	assert.Equal(t, expectedHash(t, dir,
		"go.mod",
		"go.sum",
		filepath.Join("internal", "api", "main.go"),
	), first)
	assert.Equal(t, first, second)

	mustWrite(t, filepath.Join(dir, "internal", "generated", "ignored.go"), "changed but excluded\n")
	afterExcludedChange, err := HashFiles(dir, patterns...)
	require.NoError(t, err)
	assert.Equal(t, first, afterExcludedChange)
}

func TestHashFilesNoMatches(t *testing.T) {
	t.Parallel()

	value, err := HashFiles(t.TempDir(), "missing/**")
	require.NoError(t, err)
	assert.Empty(t, value)
}

func TestHashFilesRejectsPathTraversal(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{"../secret", "internal/../../secret", "!../secret"} {
		_, err := HashFiles(t.TempDir(), pattern)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes workspace")
	}
}

func TestHashFilesRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	mustWrite(t, outside, "secret")
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape.txt")))

	_, err := HashFiles(root, "escape.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace through symlink")
}

func expectedHash(t *testing.T, root string, files ...string) string {
	t.Helper()
	combined := sha256.New()
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(root, file))
		require.NoError(t, err)
		fileHash := sha256.Sum256(data)
		_, err = combined.Write(fileHash[:])
		require.NoError(t, err)
	}
	return strings.ToLower(hex.EncodeToString(combined.Sum(nil)))
}
