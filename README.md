# manifest-repo

Tools for publishing deployment manifests from application repositories into a central Git repository consumed by GitOps systems such as [swarm-deploy](https://github.com/swarm-deploy/swarm-deploy).

## Commands

### `render`

Prepares an application Compose manifest for a release. It applies deterministic release rendering:

- every service image is replaced with `<registry>/applications/<stack>/<service>:<tag>`;
- every service gets `org.swarm_deploy.github_repository=<source repository URL>` in `deploy.labels`;
- both mapping and list Compose label syntax are supported.

```bash
manifest-repo render \
  --source deploy/prod.yaml \
  --output /tmp/deploy-prod.yaml \
  --stack core \
  --registry registry.example \
  --tag 2026-09-29-a1b2c3d \
  --source-repository-url https://github.com/example/core
```

### `merge`

Updates a local manifest using the same merge rules as the previous CI scripts.

`merge` preserves target entries and replaces/adds entries from the source for these Compose sections:

- `services`
- `networks`
- `volumes`
- `secrets`
- `configs`

Other top-level keys from the source replace the corresponding target value. Entries inside a section are replaced as a whole; service definitions are not deep-merged.

```bash
manifest-repo merge \
  --source /tmp/deploy-prod.yaml \
  --target applications/core.yaml \
  --mode merge
```

Use `--mode replace` to replace the target file byte-for-byte.

### `publish`

Clones the central repository, applies `merge` or `replace`, validates the resulting file with `docker compose config`, commits the change, and pushes the target branch.

```bash
export MANIFEST_REPO_TOKEN=github_token

manifest-repo publish \
  --source /tmp/deploy-prod.yaml \
  --repo example/manifests \
  --branch main \
  --stack core \
  --mode merge \
  --message "chore(gitops): sync core from example/core@a1b2c3d (2026-09-29-a1b2c3d)"
```

With `--stack core`, the default target is `applications/core.yaml`. A custom path can be provided with `--target`.

The Git token is read from `MANIFEST_REPO_TOKEN` by default. Use `--token-env` to select a different environment variable. The token is passed to Git through process-local configuration rather than being embedded in the repository URL.

Compose validation is enabled by default for `publish`; use `--validate-compose=false` only when the target is intentionally not a Compose manifest.

The CLI intentionally does not own image building or GitHub Release creation. Those remain responsibilities of the application release workflow.

## Development

```bash
go test ./...
go build ./cmd/manifest-repo
```
