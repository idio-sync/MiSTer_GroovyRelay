# Jellyfin Auto-Advance (Continuous Play) — Design

**Date:** 2026-05-31
**Status:** Approved design; written spec pending user review
**Scope:** Jellyfin adapter continuous play through the client-supplied queue.

## Problem

Jellyfin controllers can send this bridge a list of items to play, or can add
items with `PlayNext` / `PlayLast`. The current adapter starts the first item
for `PlayNow` and supports a small adapter-local queue for later `PlayNext` /
`PlayLast` commands, but when the active item reaches EOF the relay stops
instead of advancing.

Plex auto-advance can refetch a Plex play queue. Jellyfin does not expose the
same kind of target-readable server queue for this adapter path. Jellyfin apps
generally behave as the playback target that receives an ordered `ItemIds`
payload, keeps its own queue, reports `NowPlayingQueue`, and handles next-track
commands locally. The bridge should follow that target-side model.

## Goals

- Add a persisted `[adapters.jellyfin].auto_advance` toggle, default off.
- When an item ends cleanly and the toggle is on, play the next item already
  known to the Jellyfin adapter queue.
- Treat multi-item `PlayNow` requests as queue-bearing requests: start the
  selected item and retain the remaining items for later auto-advance.
- Preserve existing `PlayNext`, `PlayLast`, and manual `NextTrack` behavior.
- Avoid double-advance if a Jellyfin controller sends its own next/play command
  near EOF.
- Degrade to today's stop behavior on empty queue or any advance failure.

## Non-Goals

- No Jellyfin API "next episode," album-track, playlist, instant-mix, shuffle,
  or repeat resolver in v1.
- No queue looping. End of the adapter-local queue stops.
- No prefetch, gapless playback, or parallel transcoder setup.
- No chassis transport-row `CONTINUOUS` button in this core pass. The settings
  field is enough to enable and test the behavior; a shared UI polish pass can
  bind Plex and Jellyfin continuous-play toggles later.

## Decisions

| Question | Decision |
| --- | --- |
| Queue source | Client-supplied Jellyfin `ItemIds`, `PlayNext`, and `PlayLast` only |
| Default | Off |
| Config owner | `[adapters.jellyfin].auto_advance` |
| Apply scope | `ScopeHotSwap` |
| EOF trigger | `core.SessionRequest.OnStop("eof")` only |
| Race guard | `core.Manager.StartSessionIfIdle` through Jellyfin's local `SessionManager` interface |
| End of queue | Stop quietly |
| Failed next item | Stop; do not skip further |

## Architecture

The feature lives inside `internal/adapters/jellyfin/` plus the existing core
guard method exposed through the adapter's narrow `SessionManager` interface.
Core stays adapter-agnostic.

The adapter already has:

- `Adapter.queue []QueuedItem`, protected by `Adapter.mu`.
- `queueAt` for `PlayNext` / `PlayLast`.
- `popQueueHead` and `startQueuedItem`, used by manual `NextTrack`.
- `buildSessionRequest`, which attaches an `OnStop` closure.
- `makeOnStop`, which wakes the reporter and marks error stops.

The implementation adds a small auto-advance unit around those existing pieces.
The important behavior is not "discover the next thing"; it is "continue the
queue Jellyfin already handed us."

## Component 1 — Config and Live Toggle

Add `AutoAdvance bool` to `Config` with TOML key `auto_advance`, defaulting to
false.

Expose it through `Fields()`:

- key: `auto_advance`
- label: `Continuous Play`
- kind: `adapters.KindBool`
- default: `false`
- apply scope: `adapters.ScopeHotSwap`
- section: `Playback`

`CurrentValues()` returns the current value. `ApplyConfig()` stores the new
config and reports hot-swap scope for `auto_advance` changes. The EOF goroutine
reads the toggle through a small snapshot helper under `Adapter.mu`; no
additional atomic mirror is needed for v1 because Jellyfin adapter config is
already mutex-protected and the read is infrequent.

## Component 2 — Queue Capture for PlayNow

`HandlePlay` currently routes `PlayNow` to `startPlayNow(p)`, and
`startPlayNow` plays `p.ItemIDs[0]`. For continuous play, `PlayNow` must honor
the full list:

1. Clamp `StartIndex` to a valid index; if omitted or invalid, use `0`.
2. Start `ItemIDs[StartIndex]`.
3. Replace the adapter-local queue with the items after `StartIndex`, preserving
   `MediaSourceID`, `AudioStreamIndex`, and `SubtitleStreamIndex` in each
   `QueuedItem`.
4. Ignore items before `StartIndex` for v1; previous-track history remains a
   non-goal.

This queue replacement happens synchronously before the start goroutine begins,
under `Adapter.mu`, so the adapter's now-playing queue snapshot and manual
`NextTrack` see the same ordering.

`PlayNext` and `PlayLast` keep their current meaning: insert at the front or
append to the current adapter-local queue.

## Component 3 — EOF Auto-Advance

Attach auto-advance in `buildSessionRequest` with a wrapper around the existing
cleanup + reporter stop behavior:

```go
base := artworkcache.WithCleanup(in.PlayInfo.ArtworkPath, a.makeOnStop(refKey))
req.OnStop = a.withAutoAdvance(refKey, base)
```

The wrapper calls `base(reason)` first so artwork cleanup and reporter wakeup
stay byte-for-byte equivalent, then starts background EOF advance work only when
`reason == "eof"`.

Flow:

```text
ffmpeg exits cleanly
  -> core calls OnStop("eof")
  -> Jellyfin reporter wakeup still runs
  -> if auto_advance is off: done
  -> if queue is empty: done
  -> pop one queued item
  -> 1s settle delay
  -> build next request through the same queued-item playback path
  -> StartSessionIfIdle(nextReq)
      -> started: commit currentRefKey and spawn reporter
      -> not idle: restore/stand down without stealing controller-owned playback
      -> error: log, cleanup, stop
```

Only `"eof"` advances. `"stopped"`, `"preempted"`, `"error"`, pause, seek, and
track-switch restarts never advance.

The settle delay is for smoothness only. Correctness comes from
`StartSessionIfIdle`, which prevents a background EOF advance from double
advancing when a controller starts something first.

## Component 4 — Shared Start Helper

Manual `NextTrack` and EOF auto-advance should share most of the "start a
queued item" machinery but use different core start functions:

- Manual `NextTrack`: call `StartSession`, because the controller explicitly
  requested the replacement.
- EOF auto-advance: call `StartSessionIfIdle`, because it is autonomous
  background behavior.

The clean shape is to extract a helper that accepts:

- the `QueuedItem`
- a start strategy (`StartSession` vs `StartSessionIfIdle`)
- a guard-miss restore policy

The helper performs the existing sequence:

1. Load token and snapshot config.
2. Resolve the modeline preset.
3. Fetch metadata best-effort.
4. Fetch `PlaybackInfo`.
5. Build `core.SessionRequest`.
6. Reserve `currentRefKey` via `beginSelfPreempt`.
7. Start through the supplied strategy.
8. Commit or rollback.
9. Spawn a reporter with a fresh `NowPlayingQueue` snapshot.

For auto-advance, if `StartSessionIfIdle` returns `started=false`, the adapter
stands down and restores the popped item to the front of the queue. This keeps
the controller-supplied queue intact if a Jellyfin controller wins the race and
starts playback itself.

## Concurrency and State Rules

- All access to `cfg`, `queue`, `currentRefKey`, and `reporters` remains under
  `Adapter.mu`.
- No network I/O, token load, metadata fetch, `PlaybackInfo`, or core start may
  happen while holding `Adapter.mu`.
- The `OnStop` closure must not block. It may wake the reporter synchronously,
  then spawn the EOF advance goroutine.
- The EOF goroutine must use captured session identity only for logging and
  stale-work checks. It must not advance because a later session's state happens
  to be current.
- `StartSessionIfIdle` is the final arbiter. If another session is active, the
  background advance stands down.

## Edge Cases

| Case | Behavior |
| --- | --- |
| `auto_advance = false` | EOF stops normally |
| Empty queue | EOF stops normally |
| User stop | No advance |
| ffmpeg error | No advance; reporter marks failure as today |
| Seek or track switch | No advance from the old session |
| Controller sends `NextTrack` near EOF | Controller start wins; auto-advance guard stands down |
| Toggle flips off before EOF | EOF reads false and stops |
| Toggle flips off after goroutine passed the check | One in-flight hop may complete; the next EOF reads false |
| `PlaybackInfo` fails for next item | Log and stop; do not skip ahead |
| `StartSessionIfIdle` returns error | Log, cleanup, rollback, stop |

## Testing

Add focused tests under `internal/adapters/jellyfin/`:

- Config defaults: `DefaultConfig().AutoAdvance == false`.
- Field schema includes `auto_advance` as a hot-swappable boolean.
- `CurrentValues()` includes `auto_advance`.
- `ApplyConfig()` reports hot-swap for `auto_advance`.
- `PlayNow` with multiple `ItemIds` and `StartIndex` starts the selected item
  and stores only the following items in order.
- `PlayNow` with omitted or invalid `StartIndex` starts index `0`.
- `PlayNext` / `PlayLast` still insert and append as before.
- EOF with toggle off does not pop or start.
- EOF with non-`eof` reason does not pop or start.
- EOF with toggle on and queued item starts through `StartSessionIfIdle`.
- Guard miss restores the popped queued item to the front.
- Manual `NextTrack` still uses immediate `StartSession`.
- Reporter wakeup still fires on every stop reason.

Run:

```sh
go test ./internal/adapters/jellyfin/...
```

If the `SessionManager` interface change ripples, also run the affected adapter
and core packages.

## Future Work

A later design can add a Jellyfin "queue-empty resolver" that discovers next
episodes, album tracks, playlist continuation, shuffle, or repeat semantics from
Jellyfin APIs. That should be a separate unit behind a small resolver interface,
because those rules are policy-heavy and should not be entangled with the
deterministic client-supplied queue behavior in v1.
