# Jellyfin Auto-Advance (Continuous Play) — Design

**Date:** 2026-05-31
**Status:** Review fixes applied; pending user review
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

1. Use `StartIndex` only when `0 <= StartIndex < len(ItemIDs)`; otherwise use
   `0`.
2. Start `ItemIDs[StartIndex]`.
3. Replace the adapter-local queue with the items after `StartIndex`, preserving
   `MediaSourceID`, `AudioStreamIndex`, and `SubtitleStreamIndex` in each
   `QueuedItem`.
4. Ignore items before `StartIndex` for v1; previous-track history remains a
   non-goal.

This queue replacement happens synchronously before the start goroutine begins,
under `Adapter.mu`, so the adapter's now-playing queue snapshot and manual
`NextTrack` see the same ordering. If the selected item later fails to start,
the trailing queue remains in place; the adapter accepted the controller's
queue-bearing command, but it does not auto-skip to the following item.

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
  -> 1s settle delay
  -> if auto_advance is off: done
  -> if core is no longer idle: done
  -> if the stopped session is no longer the adapter's current session: done
  -> peek queue head without mutating the queue
  -> if queue is empty: done
  -> build next request for the peeked item
  -> re-check core idle, stopped ref still current, and queue head still matches
  -> StartSessionIfIdle(nextReq)
      -> started: commit only if the stopped ref is still current, the started queued item is still the queue head, and core still owns the started session; then remove that head item and spawn reporter
      -> not idle: stand down without changing queue or adapter ownership
      -> error: log, cleanup, stop
```

Only `"eof"` advances. `"stopped"`, `"preempted"`, `"error"`, pause, seek, and
track-switch restarts never advance.

The settle delay is for smoothness only. Correctness comes from
`StartSessionIfIdle`, which prevents a background EOF advance from double
advancing when a controller starts something first.

The EOF path must not pop the queue before the delay or before the idle guard.
It peeks the head, starts only if core is still idle, and mutates the queue only
after a successful guarded start. Because building a next request performs
network I/O, the auto path rechecks core idle, captured-ref identity, and
queue-head identity immediately before calling `StartSessionIfIdle`. At commit
time, it removes the started item only if that exact queued entry is still the
queue head. If a controller inserts ahead of the started item, consumes the
queue, or replaces the queue during the settle/build/start/commit window, the
background advance stands down, leaves the controller-mutated queue alone, and
stops the exact stale core session if auto-advance already started one.

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
- a queue-commit policy

The manual helper path performs the existing sequence:

1. Load token and snapshot config.
2. Resolve the modeline preset.
3. Fetch metadata best-effort.
4. Fetch `PlaybackInfo`.
5. Build `core.SessionRequest`.
6. Reserve `currentRefKey` via `beginSelfPreempt`.
7. Start through the supplied strategy.
8. Commit or rollback.
9. Spawn a reporter with a fresh `NowPlayingQueue` snapshot.

The auto-advance helper path is stricter because it is autonomous and guarded:

1. Sleep the settle delay before any queue mutation.
2. Re-read `auto_advance`; if it is off, return.
3. Check `a.core.Status()`; if core is not idle, return.
4. Check the captured stopped `refKey` still matches `a.snapshotCurrentRefKey()`;
   if not, return.
5. Peek the queue head under `Adapter.mu` without removing it.
6. Build the next `core.SessionRequest` outside `Adapter.mu`.
7. Recheck core idle, `snapshotCurrentRefKey() == refKey`, and the queue head
   still matching the peeked item. If any check fails, return.
8. Call `StartSessionIfIdle(nextReq)`.
9. If `started=false`, return without touching queue or `currentRefKey`.
10. If `started=true`, commit adapter ownership under `Adapter.mu` only when
   `currentRefKey` still equals the captured stopped ref, no controller-owned
   async start has appeared, and the started queued entry is still the queue
   head. On that same critical section, set `currentRefKey` to the new request
   ref, clear pending rollback state, and remove the started queue head.
11. If the compare-commit fails, leave queue and adapter ownership untouched,
   cleanup any request-owned artwork, and do not spawn a reporter; a newer
   controller-owned session has taken over.
12. If the compare-commit succeeds, recheck that core still owns the started
   session before emitting history or spawning a reporter. If core ownership was
   preempted after adapter commit, roll the adapter queue/ref back and stop only
   the exact stale auto-start generation.
13. If the compare-commit succeeds and core ownership still matches, spawn the
   reporter with a fresh `NowPlayingQueue` snapshot.

This means the auto path does not call `beginSelfPreempt` before the guarded
start. If implementation reuse makes pre-reservation unavoidable, rollback must
be compare-based: rollback only when `currentRefKey` still equals the attempted
auto-advance ref. A losing EOF goroutine must never restore an older ref over a
newer controller-owned session.

## Concurrency and State Rules

- All access to `cfg`, `queue`, `currentRefKey`, and `reporters` remains under
  `Adapter.mu`.
- No network I/O, token load, metadata fetch, `PlaybackInfo`, or core start may
  happen while holding `Adapter.mu`.
- The `OnStop` closure must not block. It may wake the reporter synchronously,
  then spawn the EOF advance goroutine.
- The EOF goroutine captures the stopped `refKey`. After the settle delay and
  before peeking the queue, it verifies that core is idle and
  `snapshotCurrentRefKey() == refKey`. If either check fails, it returns without
  queue or ownership mutation.
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
| Controller sends `NextTrack` near EOF | Controller start wins; auto-advance guard stands down without restoring or reordering queue items |
| Controller sends `PlayNow` near EOF | Controller start wins; auto-advance sees non-idle or stale `currentRefKey` and stands down |
| Toggle flips off before EOF | EOF reads false and stops |
| Toggle flips off after goroutine passed the check | One in-flight hop may complete; the next EOF reads false |
| Metadata or `PlaybackInfo` fails for next item | Log, leave queue and `currentRefKey` unchanged, cleanup request-owned artwork, do not spawn a reporter, do not attempt the following item |
| `StartSessionIfIdle` returns error | Log, cleanup, leave queue and `currentRefKey` unchanged, do not spawn a reporter, stop |
| Controller preempts after guarded start but before adapter commit | Compare-commit fails; auto path leaves adapter ownership and queue untouched |
| Controller preempts after adapter commit but before reporter/history finalization | Auto path rolls adapter ownership and queue back, stops only the stale auto-start generation, and emits no reporter/history |
| `PlayNext` inserts before the started item after guarded start | Auto path stands down, preserves the inserted item and original queue order, and stops only the stale auto-start generation |

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
- Guard miss leaves the queue unchanged because the auto path only peeked.
- EOF auto-advance racing with manual `NextTrack` does not reorder the queue or
  clobber `currentRefKey`.
- EOF auto-advance racing with controller `PlayNow` does not clobber
  `currentRefKey`.
- Stale EOF after seek or track-switch replacement does not mutate queue or
  adapter ownership.
- Failed or guard-missed auto-start does not clobber `currentRefKey`.
- Auto-start success followed by controller preemption before adapter commit does
  not clobber `currentRefKey`.
- Auto-start success followed by core preemption after adapter commit rolls the
  adapter queue/ref back before reporter/history finalization.
- `PlayNext` landing after successful `StartSessionIfIdle` but before adapter
  commit makes auto-advance stand down, preserving inserted and original queued
  items.
- Auto-advance metadata or `PlaybackInfo` failure leaves queue and
  `currentRefKey` unchanged, spawns no reporter, and does not skip ahead.
- `StartSessionIfIdle` error leaves queue and `currentRefKey` unchanged, spawns
  no reporter, and cleans request-owned artwork.
- Manual `NextTrack` still uses immediate `StartSession`.
- Reporter wakeup still fires on every stop reason.
- Wrapped `OnStop("eof")` still invokes artwork cleanup.

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
