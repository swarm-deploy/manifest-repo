# manifest-repo

`manifest-repo` renders application Compose manifests and publishes them into a central Git repository used as desired state by [swarm-deploy](https://github.com/swarm-deploy/swarm-deploy).

Application repositories still own image builds and notifications. `manifest-repo` only prepares desired state, reports semantic changes, and optionally commits and pushes the result.

## Publish in one step

The existing Action flow remains the default:

```yaml
- name: Publish deployment manifest
  uses: swarm-deploy/manifest-repo@v0
  with:
    source: deploy/prod.yaml
    repo: example/manifests
    branch: main
    stack: api
    mode: merge
    message: "chore(gitops): sync api from ${{ github.repository }}@${{ github.sha }}"
    token: ${{ secrets.MANIFEST_REPO_TOKEN }}
```

With the default `operation: publish`, the Action prepares the source, clones the desired-state repository, simulates the merge or replacement, calculates a semantic diff, validates the result, commits, and pushes it.
Specify a complete image reference, including registry, image name, and tag, for each service and Init Job in the Compose manifest. The Action preserves these references.

Shared environment files can be added to every source-owned service without reading or expanding their contents:

```yaml
- name: Publish deployment manifest
  uses: swarm-deploy/manifest-repo@v0
  with:
    # ...
    global-env-files: |
      env/common.env
      env/production.env
```

Paths are relative to the root of the target GitOps repository and are written relative to the target Compose file. Global entries are prepended to each service's existing `env_file` list so service-specific files retain precedence. Existing entries keep their order, duplicate paths are not added, and `environment` and Init Jobs are not changed. The referenced files are never read or parsed by `manifest-repo`; `swarm-deploy` remains responsible for resolving `env_file` values.

## Render, inspect, and publish

Use separate operations when image builds depend on the planned desired-state changes. `render` never commits or pushes. Its `rendered-manifest` output can be passed back to `publish` with `rendered: "true"`; this prevents template, repository label, and global environment file rendering from running twice.

```yaml
- name: Render desired state
  id: render
  uses: swarm-deploy/manifest-repo@v0
  with:
    operation: render
    source: deploy/prod.yaml
    output: .manifest-repo/api.yaml
    repo: example/manifests
    branch: main
    stack: api
    tag: ${{ github.sha }}
    template: "true"
    token: ${{ secrets.MANIFEST_REPO_TOKEN }}

- name: Inspect migration change
  id: inspect
  env:
    RESULT: ${{ steps.render.outputs.result }}
  run: |
    if jq -e '.changes[] | select(.id == "init-job/api/migrate" and .change == "updated" and (.changed_fields | index("image")))' <<<"$RESULT" >/dev/null; then
      echo "migration_changed=true" >> "$GITHUB_OUTPUT"
    else
      echo "migration_changed=false" >> "$GITHUB_OUTPUT"
    fi

- name: Build migration image
  if: ${{ steps.inspect.outputs.migration_changed == 'true' }}
  run: ./scripts/build-migrations

- name: Publish rendered desired state
  uses: swarm-deploy/manifest-repo@v0
  with:
    operation: publish
    source: ${{ steps.render.outputs.rendered-manifest }}
    rendered: "true"
    repo: example/manifests
    branch: main
    stack: api
    message: "chore(gitops): sync api from ${{ github.repository }}@${{ github.sha }}"
    token: ${{ secrets.MANIFEST_REPO_TOKEN }}
```

The full locally runnable example is [`.github/workflows/manifest-repo-act.yaml`](./.github/workflows/manifest-repo-act.yaml). It verifies both branches: a migration build runs when the init-job image changes and is skipped after the rendered manifest has been published.
The CI integration test in [`integration/cli_test.go`](./integration/cli_test.go) builds the CLI from the current source and exercises this same flow.

## Go template input

Set `template: "true"` to execute the complete source file with Go `text/template` before YAML parsing. Templates use `missingkey=error`. The optional `tag` input is available as `{{ .Tag }}` and `hashFiles` is available as a helper:

```gotemplate
{{ hashFiles "go.mod" "go.sum" "internal/**" "!internal/generated/**" }}
```

`hashFiles` calculates SHA-256 hashes in stable path order using the source workspace as its root. It supports multiple patterns, `**`, and ordered `!` exclusions. No matches return an empty string. Patterns that traverse above the workspace and symlinks that resolve outside it are rejected.

For example, a migration image can use independent source hashes:

```yaml
services:
  api:
    image: ghcr.io/acme/api:{{ .Tag }}
    x-init-deploy-jobs:
      - name: migrate
        image: ghcr.io/acme/api:{{ hashFiles "cmd/api/**" "internal/**" }}-migrations-{{ hashFiles "migrations/**" }}
```

The source repository URL is added as the `org.swarm_deploy.github_repository` service label independently of image templating. No other template helper library is included.

## Structured result

Both operations print versioned JSON and expose it as the Action `result` output. Use `${{ fromJSON(steps.render.outputs.result).changed }}` for simple conditions or inspect `changes` for an exact object and field.

```json
{
  "schema_version": "v1",
  "operation": "render",
  "changed": true,
  "stack": "api",
  "target": "applications/api.yaml",
  "changes": [
    {
      "id": "init-job/api/migrate",
      "kind": "init_job",
      "service": "api",
      "name": "migrate",
      "change": "updated",
      "changed_fields": ["image"],
      "details": {
        "image": {
          "before": "ghcr.io/acme/api:old",
          "after": "ghcr.io/acme/api:new"
        }
      }
    }
  ],
  "source": {
    "repository": "https://github.com/acme/api",
    "revision": "source-commit"
  },
  "desired_state": {
    "repository": "example/manifests",
    "revision": "desired-state-commit"
  }
}
```

Logical object kinds are `service`, `init_job`, `config`, `secret`, `network`, `volume`, and `top_level`. Top-level fields such as `name` and `x-*` are reported separately. Init jobs under `x-init-deploy-jobs` are compared independently from their service. Changes are `added`, `updated`, or `removed`.

Values are included only for safe fields: `image`, `replicas`, `ports`, and `constraints`. Environment values, secret values, config contents, and other potentially sensitive values never appear in `details`.

Action outputs:

| Output | Description |
| --- | --- |
| `result` | Compact v1 JSON result for `fromJSON(...)` or another consumer. |
| `changed` | `true` when the simulated desired state has semantic changes. |
| `rendered-manifest` | Path supplied through `output`. |
| `revision` | Current desired-state revision, or the new commit after publish. |

See [action.yml](./action.yml) for all inputs.

## Merge behavior

In `merge` mode, published entries replace or add entries in `services`, `networks`, `volumes`, `secrets`, and `configs`; unrelated target entries remain. Entries are complete objects and are not deep-merged. `replace` makes the source own the complete target file.

With `stack: api`, the default target is `applications/api.yaml`.

## CLI and development

See [CLI reference](./docs/cli.md) for direct use.

```bash
go test ./...
test -z "$(gofmt -l .)"
go vet ./...
go build ./cmd/manifest-repo
golangci-lint run
```
