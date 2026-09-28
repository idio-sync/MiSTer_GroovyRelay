package groovy

import "testing"

func TestCoreFromVersion(t *testing.T) {
	cases := []struct {
		v    byte
		want Core
	}{
		{0, CoreGroovy}, // no reply
		{1, CoreGroovy},
		{2, CoreGroovyNLC},
		{3, CoreGroovyNLC},
	}
	for _, c := range cases {
		if got := CoreFromVersion(c.v); got != c.want {
			t.Errorf("CoreFromVersion(%d) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestCoreString(t *testing.T) {
	if CoreGroovy.String() != "groovy" || CoreGroovyNLC.String() != "groovynlc" || CoreUnknown.String() != "unknown" {
		t.Fatalf("unexpected names: %q %q %q", CoreGroovy, CoreGroovyNLC, CoreUnknown)
	}
}

func TestQueryBuilders(t *testing.T) {
	if got := BuildGetVersion(); len(got) != 1 || got[0] != CmdGetVersion {
		t.Fatalf("BuildGetVersion = %v", got)
	}
	if got := BuildGetStatus(); len(got) != 1 || got[0] != CmdGetStatus {
		t.Fatalf("BuildGetStatus = %v", got)
	}
}
