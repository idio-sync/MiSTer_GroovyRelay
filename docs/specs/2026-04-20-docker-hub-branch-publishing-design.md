# Docker Hub branch publishing — design

**Date:** 2026-04-20
**Status:** Approved, pending implementation
**Scope:** `.github/workflows/ci.yml` only

## Problem

The current `build-image` job in `.github/workflows/ci.yml` only publishes a
Docker Hub image on semver tag pushes (`refs/tags/v*`). That is fine for
cutting releases but leaves no easy way to pull "the current tip of `main`"
or "the current tip of a development branch" as a container image. We want
pushes to `main` and (a future) `dev` branch to publish images to Docker
Hub as well, without disturbing the existing release flow.

## Goals

- Push to `main` publishes an image tagged `:main` plus an immutable
  SHA-pinned variant `:main-<short-sha>`.
- Push to `dev` publishes `:dev` and `:dev-<short-sha>`.
- Semver tag push (`v*`) continues to publish `:vX.Y.Z` and `:latest` —
  exactly today's behavior.
- Images are multi-arch: `linux/amd64` and `linux/arm64`, in a single
  manifest per tag, so `docker pull` picks the right one per host.
- The existing `test` job still gates the image build on every path —
  broken code does not reach Docker Hub.
- Pull requests never push images (unchanged — the Docker Hub credentials
  are not available to fork PRs anyway).

## Non-goals

- No `armv7` / 32-bit ARM support (only `arm64`).
- No change to the Dockerfile. The existing `CGO_ENABLED=0 go build`
  cross-compiles cleanly under buildx — `$TARGETARCH` is set by buildx per
  platform and pure Go picks it up.
- No change to which secrets are used. `DOCKERHUB_USERNAME` and
  `DOCKERHUB_TOKEN` are already configured.
- `:latest` continues to mean "last released version" — branch pushes do
  not move it.

## Design

### Trigger

Add `dev` to the push-branch list. Tag trigger and PR trigger unchanged.

```yaml
on:
  push:
    branches: [main, dev]
    tags: ['v*']
  pull_request:
```

### `build-image` job gating

Replace the tag-only gate with an event-type gate so the job runs on
branch pushes and tag pushes but still skips pull requests.

```yaml
build-image:
  runs-on: ubuntu-latest
  needs: test
  if: github.event_name == 'push'
```

`needs: test` is preserved — the image build waits for `go vet`, unit
tests, and integration tests to succeed first.

### Tag computation (`docker/metadata-action`)

Rather than hand-assembling tag strings per trigger, use the standard
`docker/metadata-action@v5` to compute the tag list.

```yaml
- uses: docker/metadata-action@v5
  id: meta
  with:
    images: idiosync000/mister-groovy-relay
    tags: |
      type=ref,event=branch
      type=ref,event=branch,suffix=-{{sha}}
      type=ref,event=tag
      type=raw,value=latest,enable=${{ startsWith(github.ref, 'refs/tags/v') }}
```

Produced tags per trigger:

| Trigger | Tags published |
|---|---|
| Push to `main` | `:main`, `:main-<short-sha>` |
| Push to `dev` | `:dev`, `:dev-<short-sha>` |
| Push to tag `v0.1.0` | `:v0.1.0`, `:latest` |
| Pull request | (job does not run) |

`type=ref,event=tag` preserves the literal tag string (`v0.1.0`) — matching
the current workflow which uses `${{ github.ref_name }}`. Using
`type=semver,pattern={{version}}` would strip the `v` prefix and publish
`:0.1.0` instead, which would be a breaking change for anyone pulling
`:v*` today.

### Multi-arch build

Add QEMU (emulates ARM on an x86 runner), keep the existing Buildx setup,
and pass both platforms to `build-push-action`. Consume the metadata
action's tag output directly.

```yaml
- uses: docker/setup-qemu-action@v3
- uses: docker/setup-buildx-action@v3
- uses: docker/login-action@v3
  with:
    username: ${{ secrets.DOCKERHUB_USERNAME }}
    password: ${{ secrets.DOCKERHUB_TOKEN }}
- uses: docker/build-push-action@v5
  with:
    context: .
    platforms: linux/amd64,linux/arm64
    push: true
    tags: ${{ steps.meta.outputs.tags }}
    cache-from: type=gha
    cache-to: type=gha,mode=max
```

Both platforms are published under a single manifest per tag. A user on
an arm64 host running `docker pull idiosync000/mister-groovy-relay:main`
gets the arm64 layer automatically; the same command on amd64 gets the
amd64 layer.

### Caching

`cache-from: type=gha` + `cache-to: type=gha,mode=max` uses the GitHub
Actions cache service. ARM emulation via QEMU is the slow part of a
multi-arch build; caching amortizes it across runs. `mode=max` caches all
intermediate layers (vs `min`, which only caches the final stage).

Expected build times:

- Cold cache (first run after cache eviction): ~6–8 min total
- Warm cache: ~2–3 min total for incremental changes

## Final workflow shape (reference)

```yaml
name: CI

on:
  push:
    branches: [main, dev]
    tags: ['v*']
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
      - name: Install ffmpeg
        run: sudo apt-get update && sudo apt-get install -y ffmpeg
      - run: go vet ./...
      - run: go test ./...
      - run: go test -tags=integration ./...

  build-image:
    runs-on: ubuntu-latest
    needs: test
    if: github.event_name == 'push'
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}
      - uses: docker/metadata-action@v5
        id: meta
        with:
          images: idiosync000/mister-groovy-relay
          tags: |
            type=ref,event=branch
            type=ref,event=branch,suffix=-{{sha}}
            type=ref,event=tag
            type=raw,value=latest,enable=${{ startsWith(github.ref, 'refs/tags/v') }}
      - uses: docker/build-push-action@v5
        with:
          context: .
          platforms: linux/amd64,linux/arm64
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

## Validation / acceptance

- Pushing a commit to `main` publishes exactly `:main` and
  `:main-<short-sha>`, each as a multi-arch manifest containing both
  `linux/amd64` and `linux/arm64`.
- Pushing a commit to `dev` (once the branch exists) publishes exactly
  `:dev` and `:dev-<short-sha>`, multi-arch.
- Pushing a semver tag `vX.Y.Z` publishes exactly `:vX.Y.Z` and `:latest`,
  multi-arch. `:latest` is not moved by branch pushes.
- Opening a pull request runs the `test` job only; `build-image` is
  skipped (no `pull_request` in its gate).
- A failing `test` job prevents any image publish on any trigger.

## Risks & mitigations

- **ARM emulation slowness** — first-ever build will be slow. Mitigated
  by the GHA cache on subsequent runs. If emulated build time becomes a
  problem, a follow-up could switch to a matrix job per platform using a
  native arm64 runner, but that is out of scope for v1.
- **Cache eviction** — GitHub Actions cache has a 10 GB repo limit and
  evicts LRU. A full cold rebuild remains fine; no correctness impact.
- **Tag collisions** — `:main` and `:dev` move on every push (intended).
  Consumers who want a stable pointer should use the SHA-pinned variant.
  This is documented behavior — not a bug.

## Out-of-scope follow-ups (not part of this change)

- README update documenting pullable tags (`:main`, `:dev`, `:latest`,
  `:vX.Y.Z`).
- Creating the `dev` branch itself (this spec only makes the workflow
  ready for it).
- Image signing / provenance (`cosign`, SLSA attestations).
- Pruning old SHA-pinned tags from Docker Hub on a schedule.
