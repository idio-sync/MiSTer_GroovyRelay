# Docker Hub Branch Publishing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `.github/workflows/ci.yml` so pushes to `main` and `dev` publish multi-arch (`linux/amd64` + `linux/arm64`) images to Docker Hub as `:branch` and `:branch-<short-sha>`, while the existing `v*` tag flow continues to publish `:vX.Y.Z` + `:latest` unchanged.

**Architecture:** Single-file YAML change. Keep `test` job as-is. Replace `build-image` job's gate (tag-only → any push), add `docker/setup-qemu-action` for ARM emulation, add `docker/metadata-action` for tag computation, add GHA cache inputs to `build-push-action`. No Dockerfile changes — pure Go (`CGO_ENABLED=0`) cross-compiles correctly under buildx.

**Tech Stack:** GitHub Actions, `docker/metadata-action@v5`, `docker/build-push-action@v5`, `docker/setup-qemu-action@v3`, `docker/setup-buildx-action@v3`, `actionlint` (local static validator).

---

## File Structure

- **Modify:** `.github/workflows/ci.yml` (the only file touched)
- **No new files.** Dockerfile is deliberately untouched — pure Go cross-compiles under buildx.

---

## TDD note for declarative config

You can't unit-test a YAML file in the usual sense, but you CAN apply TDD rigor:

1. Encode every spec invariant as a structural assertion (grep-based) in a bash block.
2. Run it against the current file → it fails (the new structure isn't there yet).
3. Apply the edit.
4. Run the same block → it passes.
5. Run `actionlint` as a schema-level sanity check.

That's Tasks 2–5. The final observational verification (Task 7) confirms the live behavior on GitHub/Docker Hub.

---

### Task 1: Install actionlint for local validation

**Files:** none (tool install)

Actionlint is a GitHub Actions workflow linter. It catches typos in action names, wrong step syntax, bad `if:` expressions, unknown inputs, etc. — strictly more than YAML validation.

- [ ] **Step 1: Install actionlint**

Run:
```bash
go install github.com/rhysd/actionlint/cmd/actionlint@latest
```

This installs to `$(go env GOPATH)/bin/actionlint.exe` on Windows (or `actionlint` on Linux/macOS). Ensure that directory is on `$PATH`.

- [ ] **Step 2: Verify install**

Run:
```bash
actionlint --version
```
Expected: version string printed (e.g., `1.7.x`), exit code 0.

- [ ] **Step 3: Baseline the current workflow**

Run from the repo root:
```bash
actionlint .github/workflows/ci.yml
```
Expected: exits 0 with no output. The current file is valid — this just confirms the tool works on it before we change anything.

---

### Task 2: Write failing structural assertions

**Files:** none (inline bash; not committed)

These assertions encode the invariants the NEW workflow must satisfy. They are structural (grep-based). They MUST fail against the current `ci.yml` — that's the failing-test step that validates the test has teeth.

- [ ] **Step 1: Run the assertion block against the current ci.yml**

Copy-paste and run from the repo root:

```bash
F=.github/workflows/ci.yml
fail=0
check() { local pat="$1" desc="$2"; if grep -qE "$pat" "$F"; then echo "PASS: $desc"; else echo "FAIL: $desc"; fail=1; fi }

check 'branches:[[:space:]]*\[main,[[:space:]]*dev\]' "triggers include main and dev branches"
check "tags:[[:space:]]*\\['v\\*'\\]" "tag trigger preserved"
check 'pull_request:' "pull_request trigger preserved"
check "if:[[:space:]]*github\\.event_name[[:space:]]*==[[:space:]]*'push'" "build-image gated on push event"
check 'needs:[[:space:]]*test' "build-image needs test job"
check 'docker/setup-qemu-action@v3' "qemu setup present"
check 'docker/setup-buildx-action@v3' "buildx setup present"
check 'docker/login-action@v3' "docker login present"
check 'docker/metadata-action@v5' "metadata-action present"
check 'type=ref,event=branch$' "metadata rule: branch tag"
check 'type=ref,event=branch,suffix=-\{\{sha\}\}' "metadata rule: branch-sha tag"
check 'type=ref,event=tag' "metadata rule: tag ref"
check 'type=raw,value=latest,enable=' "metadata rule: latest only on v-tags"
check 'platforms:[[:space:]]*linux/amd64,linux/arm64' "multi-arch platforms declared"
check 'tags:[[:space:]]*\$\{\{[[:space:]]*steps\.meta\.outputs\.tags' "build-push consumes metadata tags"
check 'cache-from:[[:space:]]*type=gha' "GHA cache read"
check 'cache-to:[[:space:]]*type=gha,mode=max' "GHA cache write max"

[ $fail -eq 0 ] && echo "ALL GREEN" || echo "FAILURES PRESENT"
```

Expected output: mostly `FAIL:` lines, ending with `FAILURES PRESENT`. A few may incidentally pass (e.g., `pull_request:` is already there, `needs: test` is already there, `docker/setup-buildx-action@v3` and `docker/login-action@v3` already exist). That's fine — what matters is that the block ends with `FAILURES PRESENT`, proving the new structure is NOT yet in the file.

If it reports `ALL GREEN`, the file already has the new structure — you're probably re-running the plan on a finished state. Skip to Task 7.

Save this block somewhere reusable — shell history or a scratch file. You'll re-run it verbatim in Task 4.

---

### Task 3: Apply the full workflow rewrite

**Files:**
- Modify: `.github/workflows/ci.yml` (entire file replaced)

This is a single coherent change. Intermediate states (e.g., add `setup-qemu-action` without wiring `metadata-action`) would produce a broken workflow, so we do it all in one edit.

- [ ] **Step 1: Replace the file contents**

Overwrite `.github/workflows/ci.yml` with exactly this content:

```yaml
# CI pipeline for MiSTer_GroovyRelay.
#
# test job:
#   - Runs on every push to main/dev, every PR, and every tag push.
#   - Installs ffmpeg (required by integration tests — the sample-clip
#     generator and live Plane scenarios both shell out).
#   - go vet + go test (unit) + go test -tags=integration across the
#     whole module. The integration glob is ./... (not just
#     tests/integration/...) so any //go:build integration test in
#     another package also runs.
#
# build-image job:
#   - Runs on any push (main, dev, v* tag). Skipped on pull_request
#     because Docker Hub credentials are not exposed to fork PRs.
#   - Uses the Docker Hub credentials configured as repo secrets:
#       DOCKERHUB_USERNAME, DOCKERHUB_TOKEN
#   - Publishes multi-arch images (linux/amd64 + linux/arm64) under a
#     single manifest per tag. Tag set is computed by metadata-action:
#       main push  -> :main, :main-<short-sha>
#       dev push   -> :dev,  :dev-<short-sha>
#       v* tag     -> :vX.Y.Z, :latest
#   - GitHub Actions cache (type=gha, mode=max) amortizes the ARM
#     emulation cost across runs.
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

---

### Task 4: Re-run the assertion block

- [ ] **Step 1: Re-run the same block from Task 2, Step 1**

Copy-paste the same bash block (or recall from history). Run it unchanged against the now-modified file.

Expected output: every line reads `PASS:`, and the final line is `ALL GREEN`.

If ANY line reads `FAIL:`, the corresponding invariant is missing from the file you just wrote — re-read Task 3's YAML and reconcile the missing piece. Do not move on until every assertion passes.

---

### Task 5: Run actionlint

- [ ] **Step 1: Lint the updated workflow**

Run:
```bash
actionlint .github/workflows/ci.yml
```
Expected: exits 0, no output.

If it reports errors (e.g., `"unknown action"`, `"expression parse error"`, `"unexpected input"`), fix them before proceeding. Common causes: a typo in an action name, a missing step field, an unbalanced `${{ }}` expression.

---

### Task 6: Commit

- [ ] **Step 1: Stage and commit**

Run:
```bash
git add .github/workflows/ci.yml
git commit -m "ci(workflow): publish multi-arch images on main/dev pushes

Branch pushes to main and dev now publish multi-arch Docker Hub images
(linux/amd64 + linux/arm64) as :branch and :branch-<short-sha>. The v*
tag release flow is preserved: still publishes :vX.Y.Z + :latest.

Uses docker/metadata-action for tag computation, docker/setup-qemu-action
for ARM emulation on the x86 runner, and the GHA cache (type=gha,
mode=max) to amortize the emulated build cost. build-image is gated on
github.event_name == 'push' so PRs still run tests only."
```

Expected: commit succeeds, `git status` is clean.

---

### Task 7: Post-push verification on GitHub + Docker Hub

**Files:** none (observational — live-system checks)

These are the spec's acceptance criteria translated into live-system checks. Do them in order — earlier steps catch problems before they pollute Docker Hub.

- [ ] **Step 1: Open a throwaway PR to dry-run the trigger gate**

Before merging to `main`, push the commit to a feature branch (e.g., `ci/docker-branch-publish`) and open a PR. On the PR's Actions tab, observe:

- `test` job: runs and passes.
- `build-image` job: does NOT run (the `if: github.event_name == 'push'` excludes `pull_request`).

If `build-image` runs on a PR, the `if:` expression is wrong — revisit Task 3.

- [ ] **Step 2: Merge to main and watch the first run**

Merge the PR. On the resulting push to `main`:

- `test` runs and passes.
- `build-image` runs. Expect ~6–8 minutes on a cold GHA cache (QEMU emulating ARM is the slow part).

If `build-image` is skipped on the push to `main`, the `if:` condition is excluding pushes too — revisit Task 3.

- [ ] **Step 3: Verify Docker Hub tags**

At `https://hub.docker.com/r/idiosync000/mister-groovy-relay/tags`, confirm:

- `:main` exists, updated minutes ago.
- `:main-<short-sha>` exists, where `<short-sha>` is the first 7 chars of the merge commit SHA.
- `:latest` is UNCHANGED — its "last pushed" timestamp still points at whatever release previously moved it, NOT the `:main` push.

If `:latest` moved on the `main` push, the metadata rule `type=raw,value=latest,enable=${{ startsWith(github.ref, 'refs/tags/v') }}` is wrong — revisit Task 3.

- [ ] **Step 4: Verify multi-arch manifest**

From any Docker host with `buildx`:
```bash
docker buildx imagetools inspect idiosync000/mister-groovy-relay:main
```

Expected: output lists two platforms in the manifest:
```
Platform: linux/amd64
Platform: linux/arm64
```

If only one platform is listed, the `platforms:` input didn't take effect — revisit Task 3.

- [ ] **Step 5: Verify cache kicked in on a second run**

Trigger a second push to `main` (a no-op docs change or whatever next lands). Watch the `build-image` job timing: it should complete in ~2–3 minutes (layers restore from the GHA cache).

If the second run takes the full cold-cache time (~6–8 min), caching isn't working — check that both `cache-from: type=gha` AND `cache-to: type=gha,mode=max` are in the file (Task 2 assertions should have caught a missing line — double-check).

- [ ] **Step 6: Smoke-test the `dev` branch path (deferred until `dev` exists)**

When the `dev` branch is eventually created and pushed to, repeat Steps 2–4 substituting `:dev` and `:dev-<short-sha>` for the `main` variants. `:latest` must remain unmoved on `dev` pushes too. If `dev` does not exist yet, this step is deferred — the workflow is already wired to handle it on first push.

---

## Self-Review

**1. Spec coverage:**
- Triggers updated (main + dev + v* + PR) → Task 3 ✓
- `build-image` gate changed to any push → Task 3 ✓
- Tag computation rules (branch, branch-sha, tag, latest-on-v) → Task 3 ✓
- Multi-arch build (amd64 + arm64) → Task 3 ✓
- GHA cache (type=gha, mode=max) → Task 3 ✓
- Test gate preserved (`needs: test`) → Task 3 ✓
- Dockerfile unchanged → implicit, no task modifies it ✓
- Spec's acceptance criteria (branch tags published, tag flow preserved, PR path unchanged, multi-arch manifest, failing tests block publish) → Task 7 Steps 1–6 ✓

**2. Placeholder scan:** None. All grep patterns are literal. Full YAML content shown in Task 3. All commands have explicit expected output.

**3. Name/type consistency:** Cross-checked. Every pattern in Task 2's assertions maps to literal content in Task 3's YAML. The metadata-action `id: meta` is consumed as `steps.meta.outputs.tags` in `build-push-action` — names match in both places. Secret names (`DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN`) match what the spec says is already configured.
