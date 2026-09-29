package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestApplyFileMerge(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	target := filepath.Join(dir, "target.yaml")
	mustWrite(t, source, `services:
  api:
    image: registry/api:v2
  worker:
    image: registry/worker:v1
networks:
  backend:
    external: true
secrets:
  api-token:
    external: true
name: application
`)
	mustWrite(t, target, `services:
  api:
    image: registry/api:v1
    environment:
      OLD: value
  untouched:
    image: registry/untouched:v1
networks:
  frontend:
    external: true
name: old-name
`)

	if err := ApplyFile(source, target, MergeModeMerge); err != nil {
		t.Fatalf("ApplyFile() error = %v", err)
	}

	var got map[string]any
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	services := got["services"].(map[string]any)
	api := services["api"].(map[string]any)
	if api["image"] != "registry/api:v2" {
		t.Fatalf("api image = %v", api["image"])
	}
	if _, ok := api["environment"]; ok {
		t.Fatalf("service entries must be replaced, not deep-merged: %#v", api)
	}
	if _, ok := services["untouched"]; !ok {
		t.Fatalf("target-only service was removed: %#v", services)
	}
	if _, ok := services["worker"]; !ok {
		t.Fatalf("source service was not added: %#v", services)
	}

	networks := got["networks"].(map[string]any)
	if _, ok := networks["frontend"]; !ok {
		t.Fatalf("target-only network was removed: %#v", networks)
	}
	if _, ok := networks["backend"]; !ok {
		t.Fatalf("source network was not added: %#v", networks)
	}
	if got["name"] != "application" {
		t.Fatalf("non-merge section should be replaced, got name=%v", got["name"])
	}
}

func TestApplyFileReplace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	target := filepath.Join(dir, "nested", "target.yaml")
	want := `services:
  api:
    image: registry/api:v2
`
	mustWrite(t, source, want)

	if err := ApplyFile(source, target, MergeModeReplace); err != nil {
		t.Fatalf("ApplyFile() error = %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("replace changed source bytes: want %q, got %q", want, got)
	}
}

func TestApplyFileMergeSkipsNullSections(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	target := filepath.Join(dir, "target.yaml")
	mustWrite(t, source, `services: null
networks: null
volumes: null
secrets: null
configs: null
name: updated
`)
	mustWrite(t, target, `services:
  api:
    image: registry/api:v1
networks:
  shared:
    external: true
volumes:
  data: {}
secrets:
  token:
    external: true
configs:
  settings:
    external: true
name: old
`)

	if err := ApplyFile(source, target, MergeModeMerge); err != nil {
		t.Fatalf("ApplyFile() error = %v", err)
	}

	var got map[string]any
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for section, entry := range map[string]string{
		"services": "api",
		"networks": "shared",
		"volumes":  "data",
		"secrets":  "token",
		"configs":  "settings",
	} {
		values := got[section].(map[string]any)
		if _, ok := values[entry]; !ok {
			t.Fatalf("null source section %s changed target: %#v", section, values)
		}
	}
	if got["name"] != "updated" {
		t.Fatalf("non-merge section was not updated: %#v", got["name"])
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
