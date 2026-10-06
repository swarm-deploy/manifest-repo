package manifest

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swarm-deploy/manifest-repo/internal/result"
)

func TestDiffFilesReportsLogicalObjectsAndSafeDetails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	before := filepath.Join(dir, "before.yaml")
	after := filepath.Join(dir, "after.yaml")
	mustWrite(t, before, `services:
  api:
    image: registry/api:old
    environment:
      TOKEN: old-secret
    deploy:
      replicas: 1
      placement:
        constraints: [node.role == worker]
    x-init-deploy-jobs:
      - name: migrate
        image: registry/migrations:old
        environment:
          PASSWORD: old-migration-secret
  removed:
    image: registry/removed:v1
configs:
  settings:
    content: old-config-secret
`)
	mustWrite(t, after, `services:
  api:
    image: registry/api:new
    environment:
      TOKEN: new-secret
    deploy:
      replicas: 2
      placement:
        constraints: [node.labels.zone == a]
    x-init-deploy-jobs:
      - name: migrate
        image: registry/migrations:new
        environment:
          PASSWORD: new-migration-secret
  added:
    image: registry/added:v1
configs:
  settings:
    content: new-config-secret
`)

	changes, err := DiffFiles(before, after)
	require.NoError(t, err)

	byID := changesByID(changes)
	assert.Equal(t, "added", byID["service/added"].Change)
	assert.Equal(t, "removed", byID["service/removed"].Change)
	assert.Equal(t, []string{"constraints", "environment", "image", "replicas"}, byID["service/api"].ChangedFields)
	assert.Equal(t, result.FieldChange{Before: "registry/api:old", After: "registry/api:new"}, byID["service/api"].Details["image"])
	assert.Equal(t, result.FieldChange{Before: 1, After: 2}, byID["service/api"].Details["replicas"])

	initJob := byID["init-job/api/migrate"]
	assert.Equal(t, "init_job", initJob.Kind)
	assert.Equal(t, "api", initJob.Service)
	assert.Equal(t, "migrate", initJob.Name)
	assert.Equal(t, "updated", initJob.Change)
	assert.Equal(t, []string{"environment", "image"}, initJob.ChangedFields)
	assert.Equal(t, result.FieldChange{Before: "registry/migrations:old", After: "registry/migrations:new"}, initJob.Details["image"])

	encoded, err := json.Marshal(changes)
	require.NoError(t, err)
	for _, sensitive := range []string{"old-secret", "new-secret", "old-migration-secret", "new-migration-secret", "old-config-secret", "new-config-secret"} {
		assert.NotContains(t, string(encoded), sensitive)
	}
}

func TestDiffFilesReturnsNoChangesForSemanticEquality(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	before := filepath.Join(dir, "before.yaml")
	after := filepath.Join(dir, "after.yaml")
	mustWrite(t, before, "services: {api: {image: registry/api:v1}}\n")
	mustWrite(t, after, `services:
  api:
    image: registry/api:v1
`)

	changes, err := DiffFiles(before, after)
	require.NoError(t, err)
	assert.Empty(t, changes)
}

func TestDiffFilesClassifiesTopLevelObjectKinds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	before := filepath.Join(dir, "before.yaml")
	after := filepath.Join(dir, "after.yaml")
	mustWrite(t, before, `configs:
  settings:
    file: old.conf
secrets:
  token:
    external: true
networks:
  backend: {}
volumes:
  data:
`)
	mustWrite(t, after, `configs:
  settings:
    file: new.conf
networks:
  backend: {}
  frontend: {}
volumes:
  data:
    driver: local
`)

	changes, err := DiffFiles(before, after)
	require.NoError(t, err)
	byID := changesByID(changes)
	assert.Equal(t, "config", byID["config/settings"].Kind)
	assert.Equal(t, "updated", byID["config/settings"].Change)
	assert.Equal(t, "secret", byID["secret/token"].Kind)
	assert.Equal(t, "removed", byID["secret/token"].Change)
	assert.Equal(t, "network", byID["network/frontend"].Kind)
	assert.Equal(t, "added", byID["network/frontend"].Change)
	assert.Equal(t, "volume", byID["volume/data"].Kind)
	assert.Equal(t, "updated", byID["volume/data"].Change)
}

func changesByID(changes []result.Change) map[string]result.Change {
	indexed := make(map[string]result.Change, len(changes))
	for _, change := range changes {
		indexed[change.ID] = change
	}
	return indexed
}
