package streams

// userProviderCacheKey and userPlaylistCacheKey derive cache keys for
// user-authored providers (spec §4.6). Raw "user:" IDs contain a colon and
// would fail cache.go's validateCacheKey, so we hash the locked IDs. The hash
// input uses a NUL separator that cannot appear in IDs, so (a, b-c) and
// (a-b, c) never collide. sha256Hex (cache.go) emits lowercase hex, so the
// emitted key matches ^[a-z0-9][a-z0-9-]{0,127}$.
func userProviderCacheKey(providerID string) string {
	return "user-provider-" + sha256Hex([]byte(providerID))
}

func userPlaylistCacheKey(providerID, channelID string) string {
	return "user-playlist-" + sha256Hex([]byte(providerID+"\x00"+channelID))
}
