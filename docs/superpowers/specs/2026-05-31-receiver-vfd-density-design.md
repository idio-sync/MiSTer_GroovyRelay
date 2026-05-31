# Receiver VFD Density Pass Design

**Date:** 2026-05-31
**Status:** Approved direction; pending user review of written spec
**Surface:** receiver chassis web UI, top VFD only

## Goal

The top VFD currently has the right visual vocabulary, but sparse now-playing
metadata can make the screen feel under-filled. Improve the density while
preserving the illusion of a manufactured 1980s/1990s AV equipment display.

This pass must not duplicate state that already belongs to the source cluster,
transport row, meter, visualizer bank, or audio strip. The top VFD remains the
now-playing identity surface: title, attribution, media detail, and believable
inactive display material.

## Design Direction

Use adaptive metadata geometry plus dormant VFD texture.

When the session has three meaningful metadata tiers, keep the existing stacked
DSEG14 hierarchy. When there are only one or two meaningful tiers, the VFD
should rebalance instead of leaving collapsed holes. The primary title can grow
slightly, secondary metadata can sit as a wider subtitle, and a dim dormant
legend or metadata facet can occupy the detail row.

The screen should look like a physical display module with more segment cells
than the current item needs. Empty areas are filled with inactive segment masks,
fixed character wells, small divider ticks, and short printed legends. These
marks are visual structure, not new system state.

## Non-Duplication Rule

Do not add live playback/source/control facts that already exist elsewhere.

Allowed on the VFD:

- Media title, show title, episode title, artist, album, year, season/episode,
  upload date, provider group, channel/group metadata.
- Static or dormant labels such as `TITLE`, `ARTIST`, `ALBUM`, `TEXT`, `INDEX`,
  `CH`, `DATE`, or `DISC` when they read as printed VFD capabilities.
- Ghost segment masks that imply unused characters.

Avoid on the VFD:

- Transport state such as play/pause/stop, elapsed time, seek position, or
  remaining time.
- Source availability or active adapter state.
- Audio DSP, meter, link, buffer, field lock, volume, or visualizer state.
- Extra status badges that would compete with existing hardware modules.

## Visual Behavior

### Dense Three-Tier Mode

Used when primary, secondary, and tertiary are all present.

- Keep the current three-row DSEG14 composition.
- Preserve marquee measurement behavior for overflowing rows.
- Add a subtle fixed-width character-cell feel behind rows so long and short
  text feel housed in the same hardware.
- Keep the right clock panel visually unchanged in this mode.

### Sparse Two-Tier Mode

Used when primary and secondary are present, tertiary is empty.

- Give primary slightly more vertical presence than dense mode, enough to fill
  the glass without becoming a hero headline.
- Let secondary occupy the normal subtitle row.
- Reserve the tertiary position for either a media-detail facet or a dormant
  legend rail. For example, a movie with year available can use the year; a
  saved stream can use group/provider metadata; a direct URL can show a dim
  `TEXT` or `INDEX` legend if no real detail exists.

### Sparse One-Tier Mode

Used when only primary is present.

- Promote primary into a taller CD-text style readout.
- Use dim ghost rows above or below it so the display still reads as a full VFD
  module.
- Prefer a dormant label rail over invented status. For example, `TITLE` can be
  lit or semi-lit while `ARTIST`, `ALBUM`, and `DATE` remain inactive.

### Idle Mode

Idle should stay clearly different from live now-playing.

- Keep `STANDBY` as the primary identity.
- Keep the existing idle hint as the secondary marquee.
- Use the same dormant display texture, but dimmer, so the screen feels powered
  and ready without claiming media is loaded.

## Data And DOM Shape

The existing `VFDData` tiers remain enough for content. The density mode can be
derived client-side and server-side from which tier strings are present:

- `dense`: primary, secondary, and tertiary present.
- `sparse-two`: primary and secondary present, tertiary empty.
- `sparse-one`: primary present, secondary and tertiary empty.
- `empty`: no primary, fallback should be idle or blank-safe.

Implementation should add mode classes to the VFD row container after each SSE
update and on first render. No new backend state is needed for this design pass.

The template should add static structural elements:

- A dormant legend rail.
- Ghost segment wells per metadata row.
- Thin divider ticks or fixed cell separators.

These elements should be `aria-hidden="true"` unless they convey real text
already exposed in the tier spans.

## CSS Approach

Use the current VFD tokens, fonts, glow, and screen frame. Do not introduce a
new palette or new visual register.

- Add density mode classes below the existing VFD CSS.
- Keep type sizes fixed with container-query adjustments, not viewport-scaled
  font sizes.
- Preserve readable contrast for active metadata.
- Keep inactive marks dim enough to read as glass/segment hardware, not content.
- Avoid card styling, rounded panels, decorative gradients, or new shadows.

## Motion

Keep the existing marquee behavior for overflow. Do not add decorative
animation. If any new transition is added for density class changes, keep it
short and state-based, and honor `prefers-reduced-motion`.

## Testing

- Template test: VFD renders the new dormant structural elements and keeps real
  tier text in the existing data spans.
- JS behavior test: VFD tier updates assign the expected density class for
  three-tier, two-tier, one-tier, and empty payloads.
- JS behavior test: density class recalculates when a later SSE event fills or
  clears a tier.
- CSS scope test: new selectors remain under `body.receiver` and do not leak to
  the non-chassis UI.
- Manual visual check: idle, music, movie, direct URL, and stream cases at wide,
  medium, and narrow chassis widths.

## Out Of Scope

- New adapter metadata fetching.
- New backend VFD fields.
- Transport, source, meter, audio strip, or visualizer-bank changes.
- Artwork, thumbnails, waveform displays, or spectrum displays inside the top
  VFD.
- Moving the system clock, queue, or uptime panel in this pass.

## Risks

- **Dormant elements mistaken for real state:** keep inactive legends visually
  subordinate and avoid words that imply live controls.
- **Sparse title over-scaling:** cap the promoted title size so it still feels
  like hardware text, not a web headline.
- **Responsive crowding:** use container-query mode adjustments and keep the
  right panel hidden at the existing narrow breakpoint.
