//go:build integration

package integration

import (
	"bytes"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
)

// assertSwitchresMatches asserts that at least one SWITCHRES wire-byte
// payload in snap.SwitchresRaw matches want byte-for-byte. Reused by
// modeline_ntsc240p_test.go, modeline_pal576i_test.go,
// modeline_pal288p_test.go.
func assertSwitchresMatches(t *testing.T, snap fakemister.RecorderSnapshot, want []byte, presetName string) {
	t.Helper()
	if len(snap.SwitchresRaw) == 0 {
		t.Errorf("%s: no SWITCHRES commands captured", presetName)
		return
	}
	for _, got := range snap.SwitchresRaw {
		if bytes.Equal(got, want) {
			return
		}
	}
	t.Errorf("%s: no SWITCHRES payload matched expected wire bytes\n  want: %x\n  got:  %x",
		presetName, want, snap.SwitchresRaw[len(snap.SwitchresRaw)-1])
}
