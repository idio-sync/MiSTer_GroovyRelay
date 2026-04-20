package plex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// StoredData holds the persisted plex.tv identity for the bridge: the
// stable device UUID (used by GDM and plex.tv registration) plus the auth
// token produced by the PIN link flow. Persisted as JSON for easy manual
// inspection / editing during development.
type StoredData struct {
	DeviceUUID string `json:"device_uuid"`
	AuthToken  string `json:"auth_token"`
}

// storedDataFilename is the on-disk name under the configured data dir.
const storedDataFilename = "data.json"

// LoadStoredData returns the persisted StoredData for the bridge. A missing
// file is treated as "never linked" and yields a zero-value struct with no
// error — callers use the empty AuthToken as the signal to run the PIN
// flow. Read or parse errors are returned as errors.
func LoadStoredData(dataDir string) (*StoredData, error) {
	path := filepath.Join(dataDir, storedDataFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &StoredData{}, nil
		}
		return nil, fmt.Errorf("read stored data: %w", err)
	}
	var sd StoredData
	if err := json.Unmarshal(data, &sd); err != nil {
		return nil, fmt.Errorf("parse stored data: %w", err)
	}
	return &sd, nil
}

// SaveStoredData writes the stored data atomically-enough to dataDir. The
// directory is created with 0700 and the file with 0600 so the plex.tv
// auth token is not world-readable on a shared host. JSON is indented so a
// human operator can eyeball it.
func SaveStoredData(dataDir string, d *StoredData) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, storedDataFilename)
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stored data: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write stored data: %w", err)
	}
	return nil
}
