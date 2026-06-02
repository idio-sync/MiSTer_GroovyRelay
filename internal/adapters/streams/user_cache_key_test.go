package streams

import "testing"

func TestUserCacheKeys_ValidateAndStable(t *testing.T) {
	pk := userProviderCacheKey("user:f1-tv")
	if err := validateCacheKey(pk); err != nil {
		t.Errorf("provider cache key %q invalid: %v", pk, err)
	}
	if pk != userProviderCacheKey("user:f1-tv") {
		t.Error("provider cache key not deterministic")
	}

	ck := userPlaylistCacheKey("user:f1-tv", "highlights")
	if err := validateCacheKey(ck); err != nil {
		t.Errorf("playlist cache key %q invalid: %v", ck, err)
	}
	// Distinct (provider, channel) pairs must not collide.
	if userPlaylistCacheKey("user:f1-tv", "highlights") == userPlaylistCacheKey("user:f1-tv", "races") {
		t.Error("distinct channels share a playlist cache key")
	}
	if userPlaylistCacheKey("user:a", "b-c") == userPlaylistCacheKey("user:a-b", "c") {
		t.Error("ambiguous separator collision between (a,b-c) and (a-b,c)")
	}
}
