package streams

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

const userManifestVersion = 1

type userManifestFile struct {
	Version   int                  `json:"version"`
	Providers []ProviderDefinition `json:"providers"`
}

// userProviderStore owns the in-memory user manifest and its atomic file
// persistence. It mirrors presetStore: load is self-healing (a missing or
// malformed file yields an empty store, never a fatal error), and writes go
// through config.WriteAtomic.
type userProviderStore struct {
	mu        sync.Mutex
	path      string
	providers []ProviderDefinition
	saveErrs  *onePerInstanceLog
}

// newUserProviderStore loads + validates the manifest at path. Invalid
// individual providers are dropped with a log; a broken file yields an empty
// store. Returns error only for truly unexpected conditions (none today).
func newUserProviderStore(path string) (*userProviderStore, error) {
	st := &userProviderStore{path: path, saveErrs: &onePerInstanceLog{}}

	body, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return st, nil
	case err != nil:
		slog.Warn("user_providers: read failed; starting empty", "err", err, "path", path)
		return st, nil
	}

	var doc userManifestFile
	if err := json.Unmarshal(body, &doc); err != nil {
		slog.Warn("user_providers: parse failed; starting empty", "err", err, "path", path)
		return st, nil
	}
	if doc.Version != userManifestVersion {
		slog.Warn("user_providers: version mismatch; starting empty",
			"got", doc.Version, "want", userManifestVersion, "path", path)
		return st, nil
	}

	seen := map[string]bool{}
	for _, def := range doc.Providers {
		if len(st.providers) >= maxUserProviders {
			slog.Info("user_providers: dropped providers over limit", "max", maxUserProviders)
			break
		}
		norm, err := normalizeUserProvider(def, seen)
		if err != nil {
			slog.Info("user_providers: dropped invalid provider on load", "id", def.ID, "err", err)
			continue
		}
		seen[norm.ID] = true
		st.providers = append(st.providers, norm)
	}
	return st, nil
}

// Snapshot returns a deep copy safe for the caller to read without sharing
// nested slices with store state.
func (s *userProviderStore) Snapshot() []ProviderDefinition {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ProviderDefinition, len(s.providers))
	for i := range s.providers {
		out[i] = cloneUserProvider(s.providers[i])
	}
	return out
}

// Put creates or replaces a user provider. A definition whose ID is empty is a
// create: a locked ID is assigned from the display name. A non-empty malformed
// ID is rejected, and a well-formed "user:" ID that matches no existing
// provider returns a NOT FOUND error (Put never creates from a caller-supplied
// ID). A definition whose ID matches an existing user provider is an in-place
// update that preserves the locked provider ID. Channel IDs are
// assigned/preserved the same way within the provider. Returns the saved,
// normalized definition.
func (s *userProviderStore) Put(def ProviderDefinition) (ProviderDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	norm, next, err := s.planPutLocked(def)
	if err != nil {
		return ProviderDefinition{}, err
	}
	if err := s.persistLocked(next); err != nil {
		return ProviderDefinition{}, err
	}
	s.providers = next
	return norm, nil
}

func cloneUserProvider(def ProviderDefinition) ProviderDefinition {
	def.URLRules = append([]URLRule(nil), def.URLRules...)
	def.Groups = append([]GroupDefinition(nil), def.Groups...)
	def.Channels = append([]ChannelDefinition(nil), def.Channels...)
	return def
}

// Delete removes the provider with the given ID. Returns ok=false (no error)
// when the ID is absent.
func (s *userProviderStore) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, p := range s.providers {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}
	next := append(s.providers[:idx:idx], s.providers[idx+1:]...)
	if err := s.persistLocked(next); err != nil {
		return false, err
	}
	s.providers = next
	return true, nil
}

func (s *userProviderStore) PlanPut(def ProviderDefinition) (ProviderDefinition, []ProviderDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, next, err := s.planPutLocked(def)
	if err != nil {
		return ProviderDefinition{}, nil, err
	}
	// planPutLocked already returns a next slice independent of s.providers
	// (built via cloneUserProviders), so no second deep copy is needed.
	return saved, next, nil
}

func (s *userProviderStore) PlanDelete(id string) ([]ProviderDefinition, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, p := range s.providers {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, false
	}
	next := append([]ProviderDefinition(nil), s.providers[:idx]...)
	next = append(next, s.providers[idx+1:]...)
	return cloneUserProviders(next), true
}

func (s *userProviderStore) CommitProviders(providers []ProviderDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneUserProviders(providers)
	if err := s.persistLocked(next); err != nil {
		return err
	}
	s.providers = next
	return nil
}

func (s *userProviderStore) Exists(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.providers {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (s *userProviderStore) planPutLocked(def ProviderDefinition) (ProviderDefinition, []ProviderDefinition, error) {
	idx := -1
	if isUserProviderID(def.ID) {
		for i, p := range s.providers {
			if p.ID == def.ID {
				idx = i
				break
			}
		}
	}
	if def.ID != "" {
		if err := validateUserProviderID(def.ID); err != nil {
			return ProviderDefinition{}, nil, err
		}
		if idx < 0 {
			return ProviderDefinition{}, nil, notFoundUserProvider(def.ID)
		}
	}
	if idx < 0 && len(s.providers) >= maxUserProviders {
		return ProviderDefinition{}, nil, badRequest("BANK FULL",
			fmt.Sprintf("streams: at most %d user providers", maxUserProviders))
	}
	taken := func(id string) bool {
		for i, p := range s.providers {
			if i == idx {
				continue
			}
			if p.ID == id {
				return true
			}
		}
		return false
	}
	if def.ID == "" {
		def.ID = newUserProviderID(def.DisplayName, taken)
	}
	norm, err := normalizeUserProvider(def, nil)
	if err != nil {
		return ProviderDefinition{}, nil, err
	}
	next := cloneUserProviders(s.providers)
	if idx >= 0 {
		next[idx] = norm
	} else {
		next = append(next, norm)
	}
	return norm, next, nil
}

func cloneUserProviders(in []ProviderDefinition) []ProviderDefinition {
	out := make([]ProviderDefinition, len(in))
	for i := range in {
		out[i] = cloneUserProvider(in[i])
	}
	return out
}

func notFoundUserProvider(id string) error {
	return &adapters.QuickCastError{
		Status:  http.StatusNotFound,
		Chip:    "NOT FOUND",
		Message: fmt.Sprintf("streams: user provider %q not found", id),
	}
}

func (s *userProviderStore) persistLocked(providers []ProviderDefinition) error {
	doc := userManifestFile{Version: userManifestVersion, Providers: providers}
	bodyBytes, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		s.saveErrs.Warn("user_providers: marshal failed", "err", err)
		return fmt.Errorf("user_providers: marshal: %w", err)
	}
	// Defense-in-depth: data_dir is expected to exist (the adapter requires it
	// at init), but MkdirAll keeps the store robust and mirrors writeCacheFile
	// (cache.go). config.WriteAtomic assumes the parent dir already exists.
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		s.saveErrs.Warn("user_providers: mkdir failed", "err", err, "path", s.path)
		return fmt.Errorf("user_providers: mkdir: %w", err)
	}
	if err := config.WriteAtomic(s.path, bodyBytes); err != nil {
		s.saveErrs.Warn("user_providers: write failed", "err", err, "path", s.path)
		return fmt.Errorf("user_providers: write: %w", err)
	}
	return nil
}

func badRequest(chip, msg string) error {
	return &adapters.QuickCastError{Status: http.StatusBadRequest, Chip: chip, Message: msg}
}
