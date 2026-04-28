package eventlog

import (
	"testing"
	"time"
)

func TestEntry_Fields(t *testing.T) {
	e := Entry{
		Time:     time.Unix(1700000000, 0),
		Severity: SeverityInfo,
		Source:   "core",
		Message:  "cast started",
	}
	if e.Source != "core" {
		t.Errorf("Source: got %q, want %q", e.Source, "core")
	}
	if e.Severity != SeverityInfo {
		t.Errorf("Severity: got %v, want %v", e.Severity, SeverityInfo)
	}
}

func TestSeverity_String(t *testing.T) {
	cases := []struct {
		s    Severity
		want string
	}{
		{SeverityInfo, "info"},
		{SeverityWarn, "warn"},
		{SeverityErr, "err"},
	}
	for _, tc := range cases {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tc.s, got, tc.want)
		}
	}
}
