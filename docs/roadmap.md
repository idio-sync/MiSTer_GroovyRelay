# Roadmap

Loose backlog of things to look at. Not a commitment, not ordered by priority.

## Plex

- **Investigate plex.tv v2 vs legacy device endpoints.** `RegisterDevice` in [internal/adapters/plex/linking.go](../internal/adapters/plex/linking.go) uses the legacy `PUT /devices/{uuid}` path while `RequestPIN`/`PollPIN` in the same file use `/api/v2/pins` and `RevokeDevice` uses `DELETE /api/v2/devices/{uuid}`. No documented reason for the inconsistency. Decide whether to migrate register to v2 too, or document why it stays on the legacy path. Check whether v2 register needs different form fields (`Connection[][uri]` vs JSON body) and whether the legacy endpoint has deprecation signals from Plex.
