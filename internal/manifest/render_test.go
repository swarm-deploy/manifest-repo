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
    image: registry.example/acme/core-grpc:v1.2.3
    x-init-deploy-jobs:
      - name: migrate
        image: registry.example/acme/migrations:v4
    deploy:
      labels:
        - keep=value
        - org.swarm_deploy.github_repository=https://old.example/repo
  worker:
    image: registry.example/acme/worker:v5
    env_file: worker.env
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
	if grpc["image"] != "registry.example/acme/core-grpc:v1.2.3" {
		t.Fatalf("unexpected image: %v", grpc["image"])
	}
	jobs := grpc["x-init-deploy-jobs"].([]any)
	if jobs[0].(map[string]any)["image"] != "registry.example/acme/migrations:v4" {
		t.Fatalf("init job image changed: %#v", jobs)
	}
	labels := grpc["deploy"].(map[string]any)["labels"].([]any)
	if len(labels) != 2 || labels[0] != "keep=value" || labels[1] != GitHubRepositoryLabel+"=https://github.com/acme/core" {
		t.Fatalf("unexpected list labels: %#v", labels)
	}

	worker := services["worker"].(map[string]any)
	if worker["image"] != "registry.example/acme/worker:v5" {
		t.Fatalf("worker image changed: %v", worker["image"])
	}
	if worker["env_file"] != "worker.env" {
		t.Fatalf("env_file changed when global files were omitted: %#v", worker["env_file"])
	}
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

func TestRenderFilePrependsGlobalEnvFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	output := filepath.Join(dir, "rendered.yaml")
	mustWrite(t, source, `services:
  api:
    image: registry.example/api:v1
    env_file:
      - ../../env/common.env
      - path: ../../env/service.env
        required: false
    environment:
      KEEP: unchanged
    x-init-deploy-jobs:
      - name: migrate
        image: registry.example/migrations:v1
        env_file:
          - init.env
  worker:
    image: registry.example/worker:v1
`)

	err := RenderFile(source, output, RenderOptions{
		GlobalEnvFiles: []string{
			"env/common.env",
			"env/production.env",
			"env/./common.env",
		},
		TargetFile: "clusters/prod/app.yaml",
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
	api := services["api"].(map[string]any)
	apiEnvFiles := api["env_file"].([]any)
	if len(apiEnvFiles) != 3 {
		t.Fatalf("api env_file = %#v, want three entries", apiEnvFiles)
	}
	if apiEnvFiles[0] != "../../env/production.env" || apiEnvFiles[1] != "../../env/common.env" {
		t.Fatalf("global files were not prepended in order without duplicates: %#v", apiEnvFiles)
	}
	serviceEnv := apiEnvFiles[2].(map[string]any)
	if serviceEnv["path"] != "../../env/service.env" || serviceEnv["required"] != false {
		t.Fatalf("service-specific long syntax changed: %#v", serviceEnv)
	}
	if api["environment"].(map[string]any)["KEEP"] != "unchanged" {
		t.Fatalf("environment changed: %#v", api["environment"])
	}
	jobs := api["x-init-deploy-jobs"].([]any)
	jobEnvFiles := jobs[0].(map[string]any)["env_file"].([]any)
	if len(jobEnvFiles) != 1 || jobEnvFiles[0] != "init.env" {
		t.Fatalf("init job changed: %#v", jobs)
	}

	worker := services["worker"].(map[string]any)
	workerEnvFiles := worker["env_file"].([]any)
	if len(workerEnvFiles) != 2 || workerEnvFiles[0] != "../../env/common.env" || workerEnvFiles[1] != "../../env/production.env" {
		t.Fatalf("worker env_file = %#v", workerEnvFiles)
	}
}

func TestRenderFilePreservesScalarEnvFileAndRootTargetPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	output := filepath.Join(dir, "rendered.yaml")
	mustWrite(t, source, `services:
  api:
    env_file: service.env
`)

	err := RenderFile(source, output, RenderOptions{
		GlobalEnvFiles: []string{"env/common.env"},
		TargetFile:     "compose.yaml",
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
	envFiles := got["services"].(map[string]any)["api"].(map[string]any)["env_file"].([]any)
	if len(envFiles) != 2 || envFiles[0] != "env/common.env" || envFiles[1] != "service.env" {
		t.Fatalf("env_file = %#v", envFiles)
	}
}

func TestRenderFileRejectsGlobalEnvFileOutsideRepository(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	mustWrite(t, source, "services:\n  api: {}\n")

	err := RenderFile(source, filepath.Join(dir, "rendered.yaml"), RenderOptions{
		GlobalEnvFiles: []string{"../outside.env"},
		TargetFile:     "applications/api.yaml",
	})
	if err == nil || !strings.Contains(err.Error(), "relative to the target repository root") {
		t.Fatalf("expected repository path error, got %v", err)
	}
}
