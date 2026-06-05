# Receiver Local Files Button Design

## Goal

Expose the existing Local Files adapter from the receiver UI without sending the user into Settings. The entry point should sit beside the existing `.TORRENT` upload button and feel like the same control family.

## User Flow

The receiver input row gains a `LOCAL FILES` button next to `.TORRENT`. Pressing it opens a local-files browse drawer on the receiver surface. The drawer lists configured libraries, lets the user navigate directories, and casts a playable file using the existing Local Files adapter service.

If Local Files is not configured or no libraries exist, the button stays visually consistent with `.TORRENT` but presents a clear unavailable state instead of opening an empty drawer.

## UI Design

`LOCAL FILES` must match the `.TORRENT` button style: same chrome, height, typography, spacing, and responsive behavior. The label is the only visible difference. The new browse drawer should reuse the existing catalog/local-files card vocabulary rather than introducing new visual patterns.

The drawer belongs to the receiver task surface, not the Settings drawer. It should close after a successful cast and report browse/cast errors through the same compact notice/chip style already used by receiver input actions.

## Architecture

Reuse the current chassis Local Files interfaces:

- `LocalFilesService.Browse(ctx, lib, path)`
- `LocalFilesService.Cast(ctx, lib, path)`
- `LocalFilesLibraryEditor.Libraries()`

The receiver route can either reuse the existing settings endpoints or add receiver-scoped aliases that call the same service methods. The implementation should avoid importing the concrete adapter into `internal/chassis`.

## Error Handling

Missing service, missing libraries, invalid library, unreadable directory, and failed casts should return small JSON envelopes that the receiver JS can translate into short UI messages. Local path details should not leak into user-visible errors.

## Testing

Add focused tests for:

- The receiver HTML renders a `LOCAL FILES` button beside `.TORRENT`.
- The new button uses the same style class as the torrent upload button.
- Browse/cast requests reach `LocalFilesService` with the selected library and path.
- Error paths return compact JSON and do not expose raw local filesystem details.

