# CLI reference

The CLI supports the same two-stage flow as the GitHub Action. `render` and `publish` use the same preparation, clone, merge or replace simulation, and semantic diff pipeline. Both print a v1 JSON result to stdout.

## `render`

`render` reads the current desired state and writes the simulated resulting manifest without committing or pushing:

```bash
manifest-repo render \
  --source deploy/prod.yaml \
  --output .manifest-repo/core.yaml \
  --repo example/manifests \
  --branch main \
  --stack core \
  --template \
  --tag 2026-09-29-a1b2c3d \
  --source-repository-url https://github.com/example/core \
  --source-revision a1b2c3d \
  --mode merge
```

Preparation order is:

1. optionally execute the whole source as a Go template;
2. add the source repository label when `--source-repository-url` is supplied;
3. clone current desired state;
4. simulate merge or replacement;
5. calculate semantic changes;
6. write the resulting manifest and structured result.

Specify complete image references, including registry, image name, and tag, in the source Compose manifest. Service and Init Job images are preserved. Use `--template` to enable strict `text/template` processing; optional `--tag` supplies `{{ .Tag }}` and does not alter images by itself. `--workspace` selects the root used by `hashFiles`; it defaults to `GITHUB_WORKSPACE`, then the current directory.

```yaml
services:
  api:
    image: registry.example/api:{{ .Tag }}
    x-init-deploy-jobs:
      - name: migrate
        image: registry.example/api:{{ hashFiles "go.mod" "internal/**" }}-migrations-{{ hashFiles "migrations/**" "!migrations/testdata/**" }}
```

Template syntax errors and missing keys fail the operation before YAML parsing. `hashFiles` supports multiple ordered patterns, `**`, and `!` exclusions. It returns an empty string for no matches and rejects workspace traversal or symlink escapes.

## `publish`

The one-step flow prepares and publishes a source manifest:

```bash
export MANIFEST_REPO_TOKEN=github_token

manifest-repo publish \
  --source deploy/prod.yaml \
  --repo example/manifests \
  --branch main \
  --stack core \
  --source-repository-url https://github.com/example/core \
  --source-revision a1b2c3d \
  --mode merge \
  --message "chore(gitops): sync core"
```

To publish output from a prior `render`, pass `--rendered`. Template and repository label rendering are skipped even when their flags are also present:

```bash
manifest-repo publish \
  --source .manifest-repo/core.yaml \
  --rendered \
  --repo example/manifests \
  --branch main \
  --stack core \
  --mode merge \
  --message "chore(gitops): sync core"
```

After simulation, `publish` validates with `docker compose config`, commits when semantic changes exist, pushes, and returns the new desired-state revision. Use `--validate-compose=false` for a fixture or a target intentionally unsupported by the local Compose version.

The Git token is read from `MANIFEST_REPO_TOKEN` by default. `--token-env` selects another environment variable. HTTP credentials are passed with repository-scoped process-local Git configuration and are not embedded in the URL. SSH and local repositories receive no HTTP authorization header.

## Structured JSON

The result contains:

- `schema_version`: currently `v1`;
- `operation`: `render` or `publish`;
- `changed`: semantic change status;
- `stack` and repository-relative `target`;
- `changes`: stable ordered logical-object changes;
- source and desired-state provenance when available.

Logical object IDs use these forms:

```text
service/<name>
init-job/<service>/<name>
config/<name>
secret/<name>
network/<name>
volume/<name>
```

`changed_fields` lists field names without values. `details.before` and `details.after` are emitted only for `image`, `replicas`, `ports`, and `constraints`. Sensitive values such as environment variables, secrets, and config contents are not emitted.

When `GITHUB_OUTPUT` is available, the CLI uses `github.com/ci-space/github-output` to write `result`, `changed`, `rendered-manifest`, and `revision` outputs.

## `merge`

`merge` remains a local low-level command:

```bash
manifest-repo merge \
  --source /tmp/deploy-prod.yaml \
  --target applications/core.yaml \
  --mode merge
```

In `merge` mode, source entries replace or add complete objects in `services`, `networks`, `volumes`, `secrets`, and `configs`; unrelated target entries remain. `--mode replace` replaces the target file.
