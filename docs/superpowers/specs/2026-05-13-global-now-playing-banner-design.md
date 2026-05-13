# Global Now Playing Banner Design

**Date:** 2026-05-13  
**Status:** Design approved; code-review fixes applied; awaiting user review of written spec  
**Scope:** Add one global now-playing and quick-cast command band to the non-setup web UI, and remove duplicated active playback controls from adapter settings pages.

## Problem

Playback controls currently live inside adapter-specific settings panels. URL, Streams, and Torrent each render their own active-session controls, while the Status page has a separate live summary. This makes playback feel like an adapter-page concern even though the bridge only has one active core session at a time.

The UI should expose one predictable place for active playback state and controls from every page. Adapter pages should focus on configuration, source browsing, and source launch workflows, not duplicate transport controls.

## Goals

- Add a global now-playing banner to the shell for all non-setup UI pages.
- Keep the banner visible while idle so page layout stays predictable.
- Ship the banner as a roomy sticky top command band.
- Keep placement easy to change later by isolating top/bottom layout in shell markup and CSS.
- Put all active playback transport controls in the banner, and remove them from adapter panels.
- Include adapter-specific active controls in the banner when the active adapter supports them, such as Streams previous/next.
- Add a global Cast drawer in the banner for URL paste and Torrent magnet/upload launch.
- Avoid adapter-specific branching in the UI by adding a small optional adapter control interface.
- Preserve existing server-rendered htmx patterns.

## Non-Goals

- No user-facing top/bottom placement setting in this pass.
- No broad redesign of the sidebar, Status page, or adapter settings pages.
- No new JavaScript framework.
- No attempt to implement Plex, Jellyfin, or DLNA web-UI transport controls unless they opt into the new interface later.
- No removal of source browsing controls such as Streams channel buttons.

## Decisions

| Topic | Decision |
|---|---|
| Placement | Top command band in the global shell. |
| Idle behavior | Banner remains visible in idle state. |
| Future bottom placement | Keep placement low-coupled through one shell region and placement CSS. |
| Settings | No user-facing placement setting. |
| Active controls | All active playback transport controls live only in the banner. |
| Source launch | URL paste and Torrent magnet/upload are available from a Cast drawer in the banner. |
| Adapter pages | Keep source setup, browsing, history, cookies, provider refresh, and channel-launch surfaces. Remove active transport controls. |
| Adapter integration | Use a generic adapter opt-in interface, not UI hard-coding by adapter name. |

## Layout

The banner sits between the sidebar shell chrome and the active page panel. It is part of the non-setup shell, not part of individual panels.

Closed state:

- state label: `Ready to cast`, `Now Playing`, or `Paused`;
- source display, such as `Streams`, `URL`, or `Plex`;
- title/source metadata from core and adapter enrichment;
- timeline readout when duration is known;
- seek slider only when duration is known and the active adapter exposes a seek action;
- transport/action buttons from the active adapter;
- Cast button that opens the drawer.

Open Cast drawer:

- appears directly below the command band;
- shows tabs or segmented controls for available quick-cast providers;
- URL tab exposes the URL adapter's URL input and mode selection as needed;
- Torrent tab exposes magnet input and .torrent upload;
- submissions start playback through existing adapter logic and re-render the banner.

The top command band should use normal document flow with sticky positioning inside the shell, so its space is reserved and it does not overlay page content. If the placement later changes to bottom, only the shell partial and placement CSS should need substantial changes; adapter contracts and control routes should remain unchanged.

## Architecture

Add one server-rendered playback banner in `internal/ui`. It composes a core status-home snapshot with optional adapter-provided control metadata.

Core remains the source of truth for:

- idle/playing/paused state;
- active `AdapterRef`;
- source adapter name when populated;
- title;
- position;
- duration;
- started-at timestamp for the current active session;
- modeline and timing data already exposed to the Status page.

The banner builder consumes `core.StatusHomeView`, not only `core.SessionStatus`, because the banner needs `Source` and `Title` as first-class fields. It derives a small `PlaybackBannerSnapshot` for providers, containing at least state, source, title, adapter ref, position, duration, started-at, media kind, and modeline.

Adapters remain the source of truth for adapter-specific commands and labels. The UI asks the active adapter for controls through an optional interface and dispatches actions back through that same interface.

Initial behavior:

- URL implements active pause/resume/stop/replay/seek and quick-cast URL launch.
- Streams implements active previous/next/replay/stop and queue display enrichment.
- Torrent implements active stop and quick-cast magnet/upload launch.
- Plex, Jellyfin, and DLNA show read-only now-playing status until they implement the interface.

## Components

### `PlaybackBannerData`

An internal UI view model used by the shell partial.

It includes:

- state: idle, playing, paused;
- title;
- source display name;
- adapter ref;
- active session key;
- position and duration;
- whether seek is available;
- active action list;
- quick-cast provider list;
- drawer open/closed state;
- transient error or success message.

### `PlaybackControlProvider`

Optional adapter interface for active playback controls.

Conceptual shape:

```go
type PlaybackControlProvider interface {
    PlaybackBanner(ctx context.Context, snap PlaybackBannerSnapshot) (PlaybackBannerAdapterView, bool)
    HandlePlaybackAction(ctx context.Context, action PlaybackActionRequest) (PlaybackActionResult, error)
}
```

The boolean means the adapter owns the supplied session and can enrich or control it. Providers must recheck ownership before mutating playback, either under adapter locks or with core guarded methods such as `StopIfAdapterRef`, `PauseIfAdapterRef`, and `SeekToIfAdapterRef`.

`PlaybackActionRequest` must include the submitted action ID, the expected active `AdapterRef`, and the expected active `StartedAt` timestamp rendered with the control. The generic route compares those expected values to a fresh core snapshot before dispatch. Providers repeat the same check before mutation. This prevents stale controls from an older session mutating a newer session from the same adapter.

### `QuickCastProvider`

Optional adapter interface for global launch forms.

Conceptual shape:

```go
type QuickCastProvider interface {
    QuickCastTabs() []QuickCastTab
    HandleQuickCast(ctx context.Context, req QuickCastRequest) (QuickCastResult, error)
}
```

This keeps URL and Torrent launch logic owned by their adapters while allowing the banner to render a global entry point.

`QuickCastTab` includes label, enabled state, disabled reason, and form encoding. URL quick-cast uses ordinary form encoding. Torrent upload requires multipart form handling for `.torrent` files, with the same upload limits and validation behavior as the existing Torrent adapter route.

### Shell Partial

Add a template such as `now-playing-banner.html` and render it from `shell.html` for non-setup pages. It should poll lightly while idle and more frequently while playing or paused.

### Adapter Panel Changes

URL:

- remove active position, scrub, pause/resume, stop, and replay controls from the panel;
- keep URL history, cookies, yt-dlp status, mode help, and the existing page-local URL launch form in the first implementation;
- expose URL quick-cast through the banner.

Streams:

- remove active previous/next/replay/stop controls from the panel;
- keep provider/channel browsing and refresh;
- if the Streams focused guide spec is implemented, its local now-playing strip must not duplicate transport controls.

Torrent:

- remove active Stop from the panel;
- keep configuration and existing page-local launch forms in the first implementation;
- expose magnet and .torrent launch through the banner.

Plex/Jellyfin/DLNA:

- no panel change required for controls in this pass;
- active sessions render read-only in the banner.

## Data Flow

1. Shell render or banner poll calls `buildPlaybackBannerData`.
2. UI reads `core.StatusHomeView` from the configured status provider.
3. UI resolves the source adapter using `core.StatusHomeView.Source` where available, falling back to adapter-ref parsing only for legacy sessions.
4. UI asks the active adapter whether it implements `PlaybackControlProvider`.
5. If the provider owns the session, it returns display enrichment and action definitions.
6. UI renders the banner with core state plus adapter enrichment.
7. A transport button posts to a generic route such as `/ui/playback/action` with hidden expected session fields.
8. The route is mounted through the existing UI POST/CSRF middleware.
9. The route snapshots the active session and compares the submitted expected `AdapterRef` and `StartedAt` with the fresh snapshot.
10. If the expected session still matches, the route resolves the owning provider and calls `HandlePlaybackAction`.
11. The provider revalidates ownership and performs the action.
12. The route re-renders the banner from fresh state.

Quick-cast flow:

1. User opens the Cast drawer.
2. UI renders quick-cast tabs from enabled `QuickCastProvider`s.
3. User submits URL or Torrent input.
4. Generic quick-cast route dispatches to the selected provider.
5. Provider reuses existing adapter launch logic.
6. Banner re-renders with success or inline error.

## Actions

Use stable action IDs so the UI can render generic buttons:

- `pause`
- `resume`
- `stop`
- `seek`
- `replay`
- `previous`
- `next`

Each action definition includes:

- ID;
- label;
- icon name or symbolic label;
- enabled state;
- expected session key for form rendering;
- optional disabled reason;
- optional confirmation flag if a future adapter needs it.

Seek is represented separately from button actions because it carries an absolute `offset_ms` value.

The expected session key is the pair of active `AdapterRef` and active `StartedAt` timestamp. If implementation later adds a monotonic core session generation, that generation should replace or augment `StartedAt`; the invariant is that a stale control must not match a newer active session, even when both sessions belong to the same adapter.

## Error Handling

Idle:

- show `Ready to cast`;
- hide timeline;
- disable transport controls;
- enable Cast drawer if at least one quick-cast provider is available.

Unknown duration:

- show state and controls;
- hide seek slider;
- allow the owning adapter to decide whether pause/resume/replay is available.

Read-only active session:

- show source, title, state, and timing if available;
- render no active transport buttons, or render disabled controls with a clear reason if that is more legible.

Stale clicks:

- return an inline banner error such as `active session changed`;
- are rejected by the generic route before provider dispatch when submitted expected session fields do not match the fresh core snapshot;
- re-render from fresh state;
- do not mutate the new session.

Start failures:

- keep the Cast drawer open;
- show a short error;
- redact URL credentials and tokens using existing adapter redaction rules.

Provider unavailable:

- quick-cast tab is omitted or disabled with a reason;
- active sessions remain visible read-only.

## Accessibility

- Banner controls are real buttons or forms.
- The command band appears before page content in DOM order so keyboard users reach global playback controls first.
- The Cast drawer is reachable from the Cast button and can be closed without losing page context.
- Seek uses a labeled range input when duration is known.
- Disabled controls include accessible labels or title text explaining why.
- Long titles and URLs truncate visually but retain useful accessible labels.
- Color is not the only state signal; playing/paused/idle use text plus icon treatment.

## Styling

Add styles to `internal/ui/static/app.css`, scoped under banner-specific classes such as:

- `.gr-now-playing`
- `.gr-now-playing--top`
- `.gr-now-playing--bottom`
- `.gr-now-playing-drawer`
- `.gr-playback-actions`
- `.gr-quick-cast`

Use existing tokens and the PR2 warm dark visual language. The banner should feel like an operational command band, not a marketing hero. It should avoid oversized type, nested cards, and decorative effects that do not serve state readability.

The CSS should define placement using a small number of wrapper classes so a future bottom placement does not require changing adapter code or route logic.

## Relationship To Existing Specs

This design supersedes the local playback-control parts of the Streams focused guide design. The Streams guide may still have a top strip for provider artwork and tuned status, but previous/next/replay/stop belong in the global banner.

This design is related to the companion extension mini remote, but does not reuse its extension-gated JSON routes. The web UI remains server-rendered HTML with htmx and uses UI-local routes so extension security concerns do not leak into the shell.

## Implementation Invariants

- Build banner display from `core.StatusHomeView` or an equivalent richer snapshot, not only from `core.SessionStatus`.
- Do not parse `AdapterRef` to identify the source except as a legacy fallback when `Source` is empty.
- Do not add UI `switch` statements for adapter-specific playback controls; use provider interfaces.
- Do not leave active transport controls in adapter panels after migrating that adapter.
- Every active transport form must carry the expected session key.
- Generic action routes must reject mismatched expected session keys before dispatching to an adapter.
- Providers must recheck ownership before mutation.

## Testing

Unit tests in `internal/ui`:

- idle banner renders predictable layout and quick-cast availability;
- playing VOD renders timeline, seek, and active controls;
- paused state renders resume instead of pause;
- unknown-duration state hides seek;
- read-only Plex/Jellyfin/DLNA sessions render status without controls;
- banner render uses source/title data from `StatusHomeView`;
- banner appears on `/ui/`, `/ui/bridge`, `/ui/diagnostics`, and `/ui/adapter/{name}` shell renders;
- banner is absent from `/ui/setup` and setup-step shell renders;
- fake playback providers can add previous/next/replay/stop;
- generic action route dispatches only to the active owning provider;
- stale-session action with changed `AdapterRef` returns an inline error and does not call the wrong provider;
- stale-session action with the same adapter but a different `StartedAt` returns an inline error before provider dispatch;
- quick-cast tabs render based on enabled providers;
- quick-cast tabs expose disabled reasons;
- quick-cast errors keep the drawer open and redact sensitive input.

Adapter tests:

- URL exposes pause/resume/stop/replay/seek and URL quick-cast;
- URL panel no longer renders active transport controls;
- Streams exposes previous/next/replay/stop for an owned queue;
- Streams panel no longer renders active transport controls;
- Torrent exposes stop and magnet/upload quick-cast;
- Torrent panel no longer renders active Stop;
- Torrent quick-cast upload preserves multipart handling and existing upload validation;
- providers reject actions when ownership has changed.

Browser checks with Playwright or an equivalent real-browser tool:

- desktop idle, playing, paused, unknown-duration, and read-only active sessions;
- mobile/narrow banner wrapping;
- Cast drawer open/closed;
- long titles, long URLs, and magnet links;
- setup pages do not render the banner;
- sticky top placement reserves layout space and does not overlay content;
- tab order from banner controls into page content;
- no overlap with adapter save buttons.

## Implementation Notes

Keep the implementation incremental:

1. Add banner view model, template, routes, and fake-provider tests in `internal/ui`.
2. Implement URL provider and remove URL panel transport duplication.
3. Implement Streams provider and remove Streams panel transport duplication.
4. Implement Torrent provider and remove Torrent panel active Stop duplication.
5. Add quick-cast drawer provider support.
6. Run UI and adapter tests, then browser-check desktop and mobile layouts.

Each adapter should own the final mutation logic. The UI route is a dispatcher, not a second playback implementation.
