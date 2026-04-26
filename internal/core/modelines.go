package core

import (
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// ModelinePreset bundles a named CRT modeline with its ffmpeg fps
// expression and an experimental flag. The data plane never sees this
// struct — startPlaneLocked plumbs preset.Modeline into PlaneConfig and
// preset.FpsExpr into PipelineSpec.OutputFpsExpr, then forgets the
// preset. Wire layer sees only groovy.Modeline.
type ModelinePreset struct {
	Name         string          // config string, e.g. "NTSC_480i"
	Modeline     groovy.Modeline // wire-format SWITCHRES timing
	FpsExpr      string          // ffmpeg "fps=" argument, e.g. "60000/1001"
	Experimental bool            // true → UI renders "(experimental)" suffix
}

// ntsc240p is the new progressive 60 Hz preset for cores / sources that
// expect 720x240 at the same field cadence as NTSC_480i. pclock 13.875
// produces FieldRate ≈ 59.952, within 0.02% of 60000/1001.
var ntsc240p = groovy.Modeline{
	PClock:    13.875,
	HActive:   720,
	HBegin:    744,
	HEnd:      809,
	HTotal:    880,
	VActive:   240,
	VBegin:    244,
	VEnd:      247,
	VTotal:    263,
	Interlace: 0,
}

// pal576i is the standard PAL interlaced preset. pclock 13.500 with
// 864x625 produces FieldRate = 50.000 Hz exactly when ×2 for interlace.
var pal576i = groovy.Modeline{
	PClock:    13.500,
	HActive:   720,
	HBegin:    732,
	HEnd:      795,
	HTotal:    864,
	VActive:   576,
	VBegin:    580,
	VEnd:      585,
	VTotal:    625,
	Interlace: 1,
}

// pal288p is the PAL progressive preset. pclock 13.478 (slightly off
// the standard 13.500 to compensate for integer-line rounding) produces
// FieldRate ≈ 49.992, within 0.016% of 50 Hz exact.
var pal288p = groovy.Modeline{
	PClock:    13.478,
	HActive:   720,
	HBegin:    732,
	HEnd:      795,
	HTotal:    864,
	VActive:   288,
	VBegin:    290,
	VEnd:      293,
	VTotal:    312,
	Interlace: 0,
}

// presets is the registry. Lookup by config string. Empty string
// resolves to NTSC_480i for back-compat with v1 configs that omitted
// the field.
var presets = map[string]ModelinePreset{
	"NTSC_480i": {
		Name:         "NTSC_480i",
		Modeline:     groovy.NTSC480i60,
		FpsExpr:      "60000/1001",
		Experimental: false,
	},
	"NTSC_240p": {
		Name:         "NTSC_240p",
		Modeline:     ntsc240p,
		FpsExpr:      "60000/1001",
		Experimental: false,
	},
	"PAL_576i": {
		Name:         "PAL_576i",
		Modeline:     pal576i,
		FpsExpr:      "50/1",
		Experimental: true,
	},
	"PAL_288p": {
		Name:         "PAL_288p",
		Modeline:     pal288p,
		FpsExpr:      "50/1",
		Experimental: true,
	},
}

// PresetNames returns the registered preset names in stable order
// (NTSC first, then PAL; non-experimental before experimental within
// each region). The UI dropdown reads from this for the modeline
// enum option list.
func PresetNames() []string {
	return []string{"NTSC_480i", "NTSC_240p", "PAL_576i", "PAL_288p"}
}

// ResolvePreset looks up a preset by config string. Empty string
// defaults to NTSC_480i. Unknown names return an error so the caller
// (Manager.startPlaneLocked) can surface a session-start failure to the
// operator without silently substituting a default.
func ResolvePreset(name string) (ModelinePreset, error) {
	if name == "" {
		return presets["NTSC_480i"], nil
	}
	p, ok := presets[name]
	if !ok {
		return ModelinePreset{}, fmt.Errorf("unknown modeline %q (supported: %v)",
			name, PresetNames())
	}
	return p, nil
}
