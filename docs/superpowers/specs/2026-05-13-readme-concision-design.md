# README Concision Design

## Goal

Make `README.md` shorter, easier to scan, and more visually organized while preserving the opening section through `Hardware requirements` unchanged.

The README should become the project's front door: enough to understand, install, run, and find the right deeper guide without reading every adapter and operational edge case inline.

## Approved Approach

Use a polished README plus focused supporting docs.

- Keep the existing content from the top of the README through `Hardware requirements` unchanged.
- Rewrite everything after that into concise sections with clear headings, short paragraphs, and small tables where they improve scanning.
- Move long adapter, operations, and troubleshooting detail into focused docs under `docs/`.
- Preserve important safety warnings and setup requirements in the README, but link to deeper explanations instead of embedding every detail.

## README Structure

After `Hardware requirements`, organize the README as:

1. `Quick start (Docker)`
2. `Native builds`
3. `First-time setup`
4. `Cast sources`
5. `Adapters`
6. `Settings UI`
7. `Operations`
8. `Troubleshooting`
9. `License`

## Supporting Docs

Create or update focused docs for details that make the README too dense:

- `docs/url-adapter.md`: URL playback, yt-dlp support, cookies, scripted POSTs, self-update behavior.
- `docs/dlna.md`: DLNA behavior, LAN security warning, required config, compatibility notes, and DLNA-specific troubleshooting.
- `docs/torrent.md`: torrent enablement, traffic acknowledgement, upload visibility, cache behavior.
- `docs/operations.md`: multi-NIC hosts, experimental delta-LZ4, Docker CPU contention, and longer troubleshooting guidance.

Keep existing docs intact where they already serve a narrower purpose, especially `docs/dlna-compatibility.md`.

## Content Style

- Prefer direct product language over release-note prose.
- Keep sections short enough to scan in seconds.
- Use tables for adapter summaries and "where to go next" references.
- Keep command blocks only where the command is the thing the reader needs immediately.
- Avoid burying warnings in long paragraphs.

## Data Flow

This is a documentation-only change. No runtime data flow changes.

The documentation flow should be:

- README introduces the workflow and links to details.
- Supporting docs hold advanced setup, security notes, and adapter-specific behavior.
- Existing links to repo files and docs remain relative and GitHub-friendly.

## Error Handling

For documentation, "error handling" means preventing reader dead ends:

- Keep the Docker host-networking requirement visible in quick start.
- Keep DLNA and torrent warnings visible in adapter summaries.
- Keep common troubleshooting symptoms discoverable from the README.
- Make every moved detail reachable from a nearby README link.

## Testing

Verify the change with documentation checks:

- Confirm the first section through `Hardware requirements` is unchanged.
- Run `rg` for conflict markers.
- Review heading order and relative links.
- Run a Markdown/link check if the repo has one; otherwise use targeted `rg` and manual link inspection.

## Current Repository Constraint

At design time, `README.md` is marked unmerged in the git index even though the working copy has no visible conflict markers. Implementation should avoid touching unrelated files and should resolve or stage the README deliberately after rewriting it.
