package streams

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// BundledPresets returns the 12 default chassis preset slots. The
// list is constant for the adapter's lifetime; 3A does not support
// editing. CastPreset (in this file) consumes the same array.
func (a *Adapter) BundledPresets() [12]adapters.PresetEntry {
	return bundledChassisPresets
}
