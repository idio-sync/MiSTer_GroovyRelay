package jellyfin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// Token holds the persisted authentication state for a linked JF
// server. Persisted under <data_dir>/jellyfin/token.json mode 0600
// (Unix). On Windows the mode bits are best-effort only — JF auth
// material lives in the user's profile dir which already has ACL
// restrictions, so this is acceptable for v1.
type Token struct {
	AccessToken string `json:"access_token"`
	UserID      string `json:"user_id"`
	UserName    string `json:"user_name"` // displayed in the UI as "Linked as ..."
	ServerID    string `json:"server_id"`
	ServerURL   string `json:"server_url"`
}

// LoadToken returns the persisted Token at path. Returns the zero
// value (Token{}) for both "file does not exist" and "file exists
// but is corrupt JSON" — the caller treats both as "not linked." A
// corrupt file is logged at warn level so the operator can spot it.
func LoadToken(path string) (Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Token{}, nil
		}
		return Token{}, fmt.Errorf("jellyfin tokenstore read %q: %w", path, err)
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		slog.Warn("jellyfin tokenstore: corrupt JSON, treating as unlinked",
			"path", path, "err", err)
		return Token{}, nil
	}
	return t, nil
}

// SaveToken persists tok to path atomically: write to "<path>.tmp",
// fsync, rename to path. The parent directory is created with mode
// 0700 if missing. The final file is mode 0600.
func SaveToken(path string, tok Token) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("jellyfin tokenstore: mkdir %q: %w", dir, err)
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return fmt.Errorf("jellyfin tokenstore: marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("jellyfin tokenstore: write %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("jellyfin tokenstore: rename %q -> %q: %w", tmp, path, err)
	}
	return nil
}

// WipeToken removes the persisted token at path. Missing-file is a
// no-op (returns nil). Used by Unlink and by Start() when the server
// rejects the token with 401.
func WipeToken(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("jellyfin tokenstore: remove %q: %w", path, err)
	}
	return nil
}
