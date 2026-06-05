// Package adapters defines the contract every cast-source
// implementation satisfies (Plex today; Jellyfin, DLNA, URL later).
// An Adapter owns its own config section ([adapters.<name>] in TOML),
// its own validation, its UI form schema, its apply-scope rules,
// and its start/stop lifecycle. The Registry holds the set.
//
// Design reference: docs/specs/2026-04-20-settings-ui-design.md §6.
package adapters

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Adapter is the cast-source contract.
type Adapter interface {
	Name() string
	DisplayName() string
	Fields() []FieldDef
	DecodeConfig(raw toml.Primitive, meta toml.MetaData) error
	IsEnabled() bool
	Start(ctx context.Context) error
	Stop() error
	Status() Status
	ApplyConfig(raw toml.Primitive, meta toml.MetaData) (ApplyScope, error)
}

// ---- Status ----

// Status is the adapter's runtime snapshot exposed to the UI sidebar
// and /status fragment endpoint. Since is set when State last changed.
type Status struct {
	State     State
	LastError string
	Since     time.Time
}

// State is a coarse lifecycle enum; the UI renders a badge per state.
type State int

const (
	StateStopped State = iota
	StateStarting
	StateRunning
	StateError
)

// String returns a 3-char badge label for the sidebar. These strings
// are part of the UI contract (status-badge.html matches on them) —
// don't shorten or rename without updating the template.
func (s State) String() string {
	switch s {
	case StateStopped:
		return "OFF"
	case StateStarting:
		return "---"
	case StateRunning:
		return "RUN"
	case StateError:
		return "ERR"
	default:
		return "???"
	}
}

// ---- ApplyScope ----

// ApplyScope ranks how disruptive a config change is. The save path
// computes the max across changed fields and dispatches accordingly
// (hot-swap inside the live stream, apply on next cast, drop the active
// cast, or restart the bridge listener). Higher int = more disruptive;
// never reorder.
type ApplyScope int

const (
	ScopeHotSwap ApplyScope = iota
	ScopeNextCast
	ScopeRestartCast
	ScopeRestartBridge
)

func (s ApplyScope) String() string {
	switch s {
	case ScopeHotSwap:
		return "hot-swap"
	case ScopeNextCast:
		return "next-cast"
	case ScopeRestartCast:
		return "restart-cast"
	case ScopeRestartBridge:
		return "restart-bridge"
	default:
		return "unknown"
	}
}

// MaxScope returns the higher-severity of two scopes; used by
// adapters when aggregating per-field scopes across a multi-field
// save (design §9.1, "max-scope-wins").
func MaxScope(a, b ApplyScope) ApplyScope {
	if a > b {
		return a
	}
	return b
}

// ---- Field schema ----

// FieldDef describes a single form control in the adapter panel.
// Adapters return []FieldDef from Fields(); the UI server renders
// them with the matching template partial keyed on Kind.
type FieldDef struct {
	Key         string
	Label       string
	Help        string
	Kind        FieldKind
	Enum        []string
	Default     any
	Required    bool
	ApplyScope  ApplyScope
	Placeholder string
	Section     string
	// SectionOrder is the explicit render-order weight for this field's
	// section. The lowest SectionOrder among fields tagged with the
	// same Section name sets that section's render order. Zero falls
	// back to "first-field-wins" registration order — back-compatible
	// with adapters that don't set it. See spec §8.1.
	SectionOrder int
}

type FieldKind int

const (
	KindText FieldKind = iota
	KindInt
	KindBool
	KindEnum
	KindSecret
	// KindAction renders as a button rather than an input. Key is
	// the relative POST endpoint suffix (e.g. "mister/launch" mounts
	// at /ui/bridge/mister/launch); Label is the button text.
	// ApplyScope and Default are ignored. KindAction rows are skipped
	// by all TOML serialization paths. See spec §8.1.
	KindAction
)

// ---- Validation errors ----

// FieldError is a validation failure scoped to a single form key. The
// UI renders these next to the input that produced the error.
type FieldError struct {
	Key string
	Msg string
}

func (fe FieldError) Error() string { return fmt.Sprintf("%s: %s", fe.Key, fe.Msg) }

// FieldErrors is an accumulator returned from adapter Validate
// implementations so the UI can report every bad field at once
// instead of one-at-a-time.
type FieldErrors []FieldError

func (fe FieldErrors) Error() string {
	if len(fe) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fe))
	for _, e := range fe {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "; ")
}

// Err returns the FieldErrors as an error (or nil when empty). Avoids
// the common pitfall of returning a non-nil error-typed variable
// holding a zero-length slice.
func (fe FieldErrors) Err() error {
	if len(fe) == 0 {
		return nil
	}
	return fe
}

// ---- Optional extension interfaces ----

// RouteProvider is an optional interface an adapter implements when
// it needs additional HTTP routes beyond the standard
// save/toggle/status set. The UI server checks for this via type
// assertion at mount time. Example: Plex's link/unlink routes.
type RouteProvider interface {
	UIRoutes() []Route
}

// Validator is an optional interface an adapter implements to allow
// pure validation of a candidate TOML section without mutating its
// runtime config. The save path uses it to enforce "validate before
// disk write" — invalid config must leave the on-disk file untouched
// (matching the Bridge panel's contract). Adapters that don't
// implement Validator fall back to ApplyConfig acting as both
// validator and applier.
type Validator interface {
	Validate(raw toml.Primitive, meta toml.MetaData) error
}

// LinkAware is an optional Adapter capability for adapters that need
// user-driven authentication (PIN, password, OAuth, etc.). Adapters
// without a link concept (URL-input, future DLNA) don't implement it.
//
// Adapters implementing LinkAware mount their own link routes under
// /old_ui/adapter/<name>/link/* and render their own state-machine HTML —
// different adapters have meaningfully different link semantics, so
// the shared UI layer doesn't try to model them.
//
// See docs/specs/2026-04-26-ui-redesign-design.md §8.2 and the PR2 delta
// spec §S8 (Plex derivation rules).
type LinkAware interface {
	// LinkPhase returns a stable lowercase string for UI rendering.
	// Adapter-defined values; common ones include "idle", "linking",
	// "pin-issued", "linked", "error". The shared UI does NOT branch
	// on this value — it's emitted as a CSS class hook and exposed
	// for diagnostics.
	LinkPhase() string

	// IsLinked reports whether the adapter has completed authentication
	// and is ready to be enabled. The wizard's Continue button is
	// disabled until IsLinked() returns true; the adapter-page form
	// renders read-only until IsLinked() returns true.
	IsLinked() bool
}

// VideoConfigSubscriber is an optional interface for adapters that mirror
// bridge.video settings into live request builders.
type VideoConfigSubscriber interface {
	OnVideoConfigChanged(modelineName string)
}

// PublicRouteProvider is an optional interface an adapter implements
// when it needs to mount HTTP routes outside the settings UI CSRF
// middleware — e.g. UPnP/DLNA control points that cannot send the
// origin headers the /ui/* paths require. cmd/mister-groovy-relay
// walks the registry for this interface at startup and calls
// MountPublicRoutes BEFORE the UI server mounts /ui/*.
//
// Adapters must register only paths that are disjoint from /ui/* and
// any other adapter's public routes (e.g. Plex's /resources, /player/*).
// Conflict resolution is the operator's responsibility — the registry
// does not de-duplicate.
type PublicRouteProvider interface {
	MountPublicRoutes(*http.ServeMux)
}

// Handler is the handler signature adapter routes register with. An
// alias for http.HandlerFunc's underlying type so adapters don't need
// to import net/http types just to satisfy the Route struct.
type Handler = func(http.ResponseWriter, *http.Request)

// Route is a single HTTP route owned by an adapter. Path is relative
// to the adapter's mount point under /old_ui/adapter/<name>/.
type Route struct {
	Method  string // "GET", "POST", "PUT", "PATCH", or "DELETE"
	Path    string // relative, e.g., "link/start"
	Handler Handler
}
