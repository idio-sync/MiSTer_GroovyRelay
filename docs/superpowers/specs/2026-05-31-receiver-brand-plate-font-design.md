# Receiver Brand Plate Font Design

**Date:** 2026-05-31
**Status:** Approved direction; pending user review of written spec
**Surface:** receiver chassis web UI, top-right brand plate

## Goal

Change the receiver UI brand plate from a VFD/display-style badge to a printed
AV hardware label. The result should feel influenced by 1980s/1990s Sony
component typography without copying the Sony logo or introducing a new brand
system.

The change applies only to the top-right `GroovyRelay` brand plate in the
receiver status bar. It should not alter source labels, preset badges, settings
badges, or VFD text.

## Chosen Direction

Use a component-label sans treatment for the brand name.

The `GroovyRelay` name should render as an uppercase printed badge using the
existing `Inter` font. Increase weight and tracking so it reads like compact
panel typography on AV equipment rather than web UI text. Remove the segmented
DSEG face from this badge so the VFD display remains the only element that uses
display-segment typography.

The model line should stay small, uppercase, technical, and subordinate. Keep
the existing `480i` VFD accent because it ties the model name back to the
receiver display language without making the brand itself glow.

## Alternatives Considered

- **Slab-serif wordmark:** strongest Sony influence, but too logo-like for this
  app and more likely to look like a brand parody.
- **Component-label sans:** selected because it feels like printed hardware
  labeling, fits the product UI register, and avoids a new font asset.
- **Monospace badge:** compatible with technical equipment, but less specific
  to 1980s/1990s AV faceplate typography and still too close to display text.

## CSS Approach

Update `body.receiver .brand-plate .name` in
`internal/chassis/static/chassis.css`:

- Use `Inter` instead of `DSEG14-Modern`.
- Set a heavier weight, uppercase transform, and wider letter spacing.
- Keep a fixed size appropriate to the compact status bar.
- Use a subtle printed or engraved text shadow, not a VFD glow.
- Preserve overflow handling with `white-space: nowrap`, `overflow: hidden`,
  and `text-overflow: ellipsis`.

Responsive overrides should keep the badge stable at existing chassis
container breakpoints. The existing smaller font-size rules for narrow widths
may stay, but they should continue to use the new font family and printed-label
style.

## Data And DOM

No data or template changes are required. The existing status bar template
already separates the brand name and model line:

- `.brand-plate .name` for `{{.BrandName}}`
- `.brand-plate .model` for model copy

The implementation should avoid changing rendered text content unless CSS
uppercase transform is used. Screen readers should continue receiving the same
brand name text from the template.

## Testing

- CSS scope test: confirm any new or changed brand-plate selectors remain under
  `body.receiver`.
- Existing receiver CSS/template tests should continue to pass.
- Manual visual check at wide, medium, and narrow receiver widths to confirm
  the brand plate remains readable and does not crowd the load-core button or
  LED cluster.

## Out Of Scope

- New font files or external font downloads.
- Changes to the VFD font stack.
- Changes to source-cluster nameplates, preset badges, settings badges, or
  catalog icons.
- Brand copy, model copy, or receiver layout changes.

## Risks

- **Too generic:** a plain sans could feel like normal web UI. Use enough
  weight and tracking to read as printed panel text.
- **Too loud:** excessive tracking or size could overpower the VFD. Keep the
  brand plate compact and secondary to live playback information.
- **Mobile crowding:** uppercase text may consume more width. Preserve ellipsis
  behavior and verify narrow chassis widths.
