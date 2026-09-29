package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	output := filepath.Join(dir, "rendered.yaml")
	input := `services:
  core-grpc:
    image: example/ignored:latest
    deploy:
      labels:
        - keep=value
        - org.swarm_deploy.github_repository=https://old.example/repo
  worker:
    image: example/ignored:latest
    deploy:
      labels:
        custom: label
networks:
  infra:
    external: true
`
	if err := os.WriteFile(source, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RenderFile(source, output, RenderOptions{
		StackName:           "core",
		Registry:            "registry.example/",
		ReleaseTag:          "v1.2.3",
		SourceRepositoryURL: "https://github.com/acme/core",
	})
	if err != nil {
		t.Fatalf("RenderFile() error = %v", err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	services := got["services"].(map[string]any)
	grpc := services["core-grpc"].(map[string]any)
	if grpc["image"] != "registry.example/applications/core/core-grpc:v1.2.3" {
		t.Fatalf("unexpected image: %v", grpc["image"])
	}
	labels := grpc["deploy"].(map[string]any)["labels"].([]any)
	if len(labels) != 2 || labels[0] != "keep=value" || labels[1] != GitHubRepositoryLabel+"=https://github.com/acme/core" {
		t.Fatalf("unexpected list labels: %#v", labels)
	}

	worker := services["worker"].(map[string]any)
	workerLabels := worker["deploy"].(map[string]any)["labels"].(map[string]any)
	if workerLabels[GitHubRepositoryLabel] != "https://github.com/acme/core" {
		t.Fatalf("repository label not set: %#v", workerLabels)
	}
}

func TestRenderFileRejectsNonStringListLabel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	input := `services:
  api:
    deploy:
      labels:
        - 42
`
	if err := os.WriteFile(source, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RenderFile(source, filepath.Join(dir, "out.yaml"), RenderOptions{
		StackName:           "api",
		Registry:            "registry.example",
		ReleaseTag:          "v1",
		SourceRepositoryURL: "https://github.com/acme/api",
	})
	if err == nil || !strings.Contains(err.Error(), "labels list must contain strings") {
		t.Fatalf("expected label type error, got %v", err)
	}
}

func TestRenderFileTreatsNullDeployAndLabelsAsAbsent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	output := filepath.Join(dir, "rendered.yaml")
	mustWrite(t, source, `services:
  null-deploy:
    deploy: null
  null-labels:
    deploy:
      labels: null
`)

	err := RenderFile(source, output, RenderOptions{
		StackName:           "core",
		Registry:            "registry.example",
		ReleaseTag:          "v1",
		SourceRepositoryURL: "https://github.com/acme/core",
	})
	if err != nil {
		t.Fatalf("RenderFile() error = %v", err)
	}

	var got map[string]any
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	services := got["services"].(map[string]any)
	for _, serviceName := range []string{"null-deploy", "null-labels"} {
		service := services[serviceName].(map[string]any)
		labels := service["deploy"].(map[string]any)["labels"].(map[string]any)
		if labels[GitHubRepositoryLabel] != "https://github.com/acme/core" {
			t.Fatalf("repository label not set for %s: %#v", serviceName, labels)
		}
	}
}
