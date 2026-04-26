package core

import (
	"fmt"
	"strings"

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
		Modeline:     groovy.NTSC240p60,
		FpsExpr:      "60000/1001",
		Experimental: false,
	},
	"PAL_576i": {
		Name:         "PAL_576i",
		Modeline:     groovy.PAL576i50,
		FpsExpr:      "50/1",
		Experimental: true,
	},
	"PAL_288p": {
		Name:         "PAL_288p",
		Modeline:     groovy.PAL288p50,
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
const experimentalSuffix = " (experimental)"

func ResolvePreset(name string) (ModelinePreset, error) {
	if strings.HasSuffix(name, experimentalSuffix) {
		name = strings.TrimSuffix(name, experimentalSuffix)
	}
	if name == "" {
		return presets["NTSC_480i"], nil
	}
	p, ok := presets[name]
	if !ok {
		return ModelinePreset{}, fmt.Errorf("unknown modeline %q (set bridge.video.modeline to one of: %s)",
			name, strings.Join(PresetNames(), ", "))
	}
	return p, nil
}
