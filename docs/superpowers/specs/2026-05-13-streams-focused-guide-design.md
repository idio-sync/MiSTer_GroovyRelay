# Streams Focused Guide Design

**Date:** 2026-05-13  
**Status:** Design approved; implementation plan not started  
**Scope:** Redesign the Streams adapter panel into a wide, TV Guide-inspired provider/channel browser.

## Problem

The Streams adapter already has useful provider, group, channel, item-count, and playback data. The current panel renders that data as provider headings, category headings, tables, and small channel buttons. The categorization is acceptable, but the arrangement is visually weak and becomes especially hard to scan for MTV Rewind, which has dozens of channels and several large 5,000-item buckets.

The UI should feel like browsing a channel guide: nostalgic, dense, and quick to operate, without becoming a literal TV Guide clone or a one-off layout that assumes only MTV Rewind and Cartoon Rewind exist.

## Goals

- Replace the current table/button layout with a wide TV Guide-inspired "focused guide."
- Keep every visible channel one click away from playback.
- Show only one provider/category slice at a time so large providers remain readable.
- Make the Streams browsing surface use the full available main-column width.
- Keep the generic adapter header, status section, and settings form at the normal readable width.
- Add optional provider artwork in the Now Playing preview box using referenced provider-owned URLs.
- Support future stream providers without redesigning the panel.
- Preserve the existing htmx/server-rendered flow and playback routes.

## Non-Goals

- Program schedules, time slots, or EPG data.
- Search, favorites, sorting, or keyboard navigation in this pass.
- A custom UI for only MTV Rewind or only Cartoon Rewind.
- Vendoring third-party logo files into the repository.
- Reworking Streams playback, queue semantics, URL resolving, or provider fetching.
- Changing adapter settings behavior.

## Approved Visual Direction

Use a restrained CRT-era channel guide aesthetic inside the existing GroovyRelay dark UI.

The panel has:

- a wide guide surface using the available main-column width;
- a top Now Playing/Idle strip;
- a preview artwork box at the left of the strip;
- compact playback controls and refresh action;
- provider tabs for available stream sources;
- a category rail for the selected provider;
- a channel grid for only the selected provider/category;
- active/hover states that use the existing amber accent.

The design should feel nostalgic through hierarchy, density, scanline texture, and guide-like cells. It should not copy TV Guide branding, use loud multi-color novelty styling, or make the rest of the app feel like a themed landing page.

## Extensible Provider Model

The UI must treat each Streams source as a generic provider. More sources will be added.

Provider tabs should be generated from `StatusView.Providers`. They should support more than two providers as a single horizontally scrollable row rather than fixed two-column assumptions. The tab label should show provider name and channel count.

Each provider may expose:

- `ID`
- `Name`
- `Groups`
- `Channels`
- `UpdatedAt`
- optional `LogoURL`
- optional `LogoAlt`
- optional `FallbackLabel`

The first implementation will add the artwork fields to the Streams manifest/provider definition model and thread them into `ProviderStatusView`. Providers without artwork still render cleanly with a text wordmark.

Default selection rules:

- If a queue is active, select its provider and channel group when possible.
- If there is no active queue, select the first provider in display order.
- For the selected provider, select the first non-empty group.
- If a requested provider or group no longer exists, fall back to the first available provider/group.

Provider ordering will continue to follow the current deterministic status ordering. A future provider-ordering feature can change display order without changing the guide layout.

## Provider Artwork

Provider artwork is referenced by URL, not vendored.

Initial metadata:

| Provider | Logo URL | Fallback |
|---|---|---|
| MTV Rewind | `https://wantmymtv.vercel.app/public/images/rewindlogo.png` | `MTV REWIND` |
| Cartoon Rewind | `https://cartoonrewind.tv/social.png` | `CARTOON REWIND` |

The Now Playing preview box should render the selected provider artwork whether idle or tuned. If image loading fails, it should show the provider fallback label as a wordmark. This fallback must be part of the HTML/CSS behavior, not only a test assumption.

The implementation should keep remote image use optional. A missing or empty logo URL must not block provider rendering or playback.

## Layout

Only the Streams extra panel should become wide. The broader adapter page structure should remain familiar:

- `h1`, subtitle, and status section stay at the existing readable width.
- `.adapter-extras` for the Streams page expands to the available main content width.
- the guide fills that expanded area.
- the settings form below stays at the existing readable width.

Guide structure:

```text
Streams adapter page
  Header/status at normal width
  Streams guide at full main-column width
    Top strip
      Provider artwork preview
      Now Playing / Idle text
      playback controls
      refresh action
    Provider tabs
    Body
      Category rail
      Channel grid for selected category
      Catalog refresh/readout footer
  Settings form at normal width
```

Desktop guide behavior:

- Use a category rail around 160-180px wide.
- Use 4 channel columns at normal wide desktop sizes.
- Allow 5 columns on very wide screens.
- Keep cell heights stable so channel counts and hover states do not shift layout.

Mobile/narrow behavior:

- Collapse the guide top strip to one column.
- Keep provider tabs horizontally scrollable.
- Turn the category rail into a compact grid or vertical list.
- Reduce channel grid to one column on small screens.
- Avoid text overlap by truncating long channel names inside cells.

## Interactions

Provider and category controls should re-render `#streams-panel` with explicit selection state. Use htmx-friendly GET or form parameters such as:

- `provider_id`
- `group_id`

Channel cells remain playback forms posting to `/ui/adapter/streams/play` with:

- `provider_id`
- `channel_id`

Refresh remains a POST to `/ui/adapter/streams/refresh`.

The active channel, if present in the selected grid, should get a tuned/active treatment. If the active channel is in another provider/group, default selection should move the guide to that active provider/group.

Playback controls remain the existing routes:

- previous
- next
- replay
- stop

The UI should not introduce client-side-only state that can get out of sync with htmx refreshes. Selection state should round-trip through the server-rendered panel.

## Data Flow

`Adapter.statusView` should continue to be the panel data source.

Extend view data as needed:

```go
type ProviderStatusView struct {
    ID            string
    Name          string
    Groups        []ChannelGroupView
    Channels      []ChannelStatusView
    UpdatedAt     time.Time
    LogoURL       string
    LogoAlt       string
    FallbackLabel string
}
```

The renderer should derive grouped channel views from `ProviderStatusView.Groups` and `ProviderStatusView.Channels`, as it does today, but render only the selected group.

No playback or catalog semantics should depend on artwork metadata.

## Error Handling

- No providers: show a compact empty state inside the guide.
- Provider has no channels: show a provider-level empty state and keep settings visible.
- Selected provider missing: fall back to the first provider.
- Selected group missing or empty: fall back to the first non-empty group for that provider.
- Logo URL missing or image load fails: show fallback wordmark.
- Active queue references a provider/channel no longer present in the catalog: still show the active status text, but do not force a broken guide selection.

## Accessibility

- Provider tabs, category selectors, channel cells, refresh, and playback controls must be real buttons or forms.
- Channel cells need clear text labels and visible focus states.
- The artwork image should use provider-specific alt text.
- The fallback wordmark should be text, not a background image.
- Color should not be the only active-state signal; use border/position/treatment as well.

## Styling

Add Streams-specific CSS in `internal/ui/static/app.css`, scoped under Streams classes such as:

- `.streams-panel`
- `.streams-guide`
- `.streams-provider-tabs`
- `.streams-category-rail`
- `.streams-channel-grid`
- `.streams-channel-cell`

Use existing tokens:

- `--gr-bg`
- `--gr-surface`
- `--gr-border`
- `--gr-text`
- `--gr-dim`
- `--gr-amber`

The palette should remain the app's warm dark GroovyRelay system. Do not introduce provider-specific color schemes in this pass.

## Testing

Update Streams UI tests to cover:

- provider tabs render for all enabled providers;
- provider tabs include channel counts;
- selected provider and group render only that group's channel cells;
- MTV Rewind dense categories, such as `Labels & Scenes`, render without a table layout;
- channel cells preserve `hx-post="/ui/adapter/streams/play"` and hidden provider/channel inputs;
- refresh and playback controls preserve their existing htmx routes;
- provider artwork metadata renders `img` with `src` and `alt`;
- fallback wordmark renders when artwork metadata is absent;
- HTML escaping still protects provider, group, channel, and fallback text;
- unknown selected provider/group falls back deterministically.

Manual visual checks should include:

- wide desktop layout;
- narrow/mobile layout;
- MTV Rewind `Labels & Scenes`;
- Cartoon Rewind decade groups;
- idle state;
- active/tuned state.

## Implementation Notes

This design can be implemented without adding JavaScript. Server-rendered htmx fragments are enough.

The existing table renderer should be replaced with semantic sections/forms rather than restyled tables. The new guide should keep the same adapter route boundaries so implementation risk stays in rendering and CSS, not playback.

Future provider additions should only need provider definitions and optional artwork metadata. They should not require Streams UI code changes unless they introduce a genuinely new source shape, such as schedules or live EPG data.
