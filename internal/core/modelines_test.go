package core

import (
	"math"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestResolvePreset_KnownNames(t *testing.T) {
	cases := []struct {
		name             string
		want             string
		wantExperimental bool
		wantFpsExpr      string
	}{
		{name: "NTSC_480i", want: "NTSC_480i", wantExperimental: false, wantFpsExpr: "60000/1001"},
		{name: "NTSC_240p", want: "NTSC_240p", wantExperimental: false, wantFpsExpr: "60000/1001"},
		{name: "PAL_576i", want: "PAL_576i", wantExperimental: true, wantFpsExpr: "50/1"},
		{name: "PAL_288p", want: "PAL_288p", wantExperimental: true, wantFpsExpr: "50/1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolvePreset(c.name)
			if err != nil {
				t.Fatalf("ResolvePreset(%q) error = %v", c.name, err)
			}
			if got.Name != c.want {
				t.Errorf("Name = %q, want %q", got.Name, c.want)
			}
			if got.Experimental != c.wantExperimental {
				t.Errorf("Experimental = %v, want %v", got.Experimental, c.wantExperimental)
			}
			if got.FpsExpr != c.wantFpsExpr {
				t.Errorf("FpsExpr = %q, want %q", got.FpsExpr, c.wantFpsExpr)
			}
		})
	}
}

func TestResolvePreset_EmptyDefaultsToNTSC480i(t *testing.T) {
	// Hard requirement (spec §UI exposure → §Config field): empty string
	// must default to NTSC_480i to preserve back-compat with v1 configs.
	got, err := ResolvePreset("")
	if err != nil {
		t.Fatalf("ResolvePreset(\"\") error = %v", err)
	}
	if got.Name != "NTSC_480i" {
		t.Errorf("empty string resolved to %q, want NTSC_480i (back-compat)", got.Name)
	}
}

func TestResolvePreset_UnknownReturnsError(t *testing.T) {
	_, err := ResolvePreset("BOGUS_MODE")
	if err == nil {
		t.Errorf("ResolvePreset(\"BOGUS_MODE\") error = nil, want error")
	}
}

func TestPresetFieldRateMatchesTarget(t *testing.T) {
	// Spec C1 guard: each preset's modeline must compute to its claimed
	// field rate within 0.1% relative error. If a future modeline value
	// drifts away from its claimed rate, this test fails the build.
	const tolerance = 0.001 // 0.1%
	cases := []struct {
		name string
		want float64
	}{
		{name: "NTSC_480i", want: 60000.0 / 1001.0},
		{name: "NTSC_240p", want: 60000.0 / 1001.0},
		{name: "PAL_576i", want: 50.0},
		{name: "PAL_288p", want: 50.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := ResolvePreset(c.name)
			if err != nil {
				t.Fatalf("ResolvePreset(%q) error = %v", c.name, err)
			}
			got := p.Modeline.FieldRate()
			rel := math.Abs(got-c.want) / c.want
			if rel > tolerance {
				t.Errorf("FieldRate() = %.4f, want %.4f within ±0.1%% (got %.4f%% off)",
					got, c.want, rel*100)
			}
		})
	}
}

func TestPresetFieldRateRatioMatchesPreset(t *testing.T) {
	// FieldRateRatio() must return the rational the preset claims —
	// otherwise Plane.Position will report position against one rate
	// while the modeline runs at another (silent drift).
	cases := []struct {
		name      string
		wantNumer int64
		wantDenom int64
	}{
		{name: "NTSC_480i", wantNumer: 60000, wantDenom: 1001},
		{name: "NTSC_240p", wantNumer: 60000, wantDenom: 1001},
		{name: "PAL_576i", wantNumer: 50, wantDenom: 1},
		{name: "PAL_288p", wantNumer: 50, wantDenom: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := ResolvePreset(c.name)
			if err != nil {
				t.Fatalf("ResolvePreset(%q) error = %v", c.name, err)
			}
			n, d := p.Modeline.FieldRateRatio()
			if n != c.wantNumer || d != c.wantDenom {
				t.Errorf("FieldRateRatio() = (%d, %d), want (%d, %d)",
					n, d, c.wantNumer, c.wantDenom)
			}
		})
	}
}

// reference to silence "groovy unused" if FieldRate's formula evolves
var _ = groovy.NTSC480i60
