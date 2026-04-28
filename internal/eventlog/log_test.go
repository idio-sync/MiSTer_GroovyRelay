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

func TestLog_AppendAndSnapshot(t *testing.T) {
	l := New(8)
	l.Append(Entry{Source: "a", Message: "1"})
	l.Append(Entry{Source: "b", Message: "2"})
	l.Append(Entry{Source: "c", Message: "3"})

	got := l.Snapshot()
	if len(got) != 3 {
		t.Fatalf("Snapshot len: got %d, want 3", len(got))
	}
	wantMsgs := []string{"1", "2", "3"}
	for i, e := range got {
		if e.Message != wantMsgs[i] {
			t.Errorf("entry[%d].Message: got %q, want %q", i, e.Message, wantMsgs[i])
		}
	}
}

func TestLog_SnapshotIsACopy(t *testing.T) {
	l := New(4)
	l.Append(Entry{Message: "original"})
	got := l.Snapshot()
	got[0].Message = "mutated"
	again := l.Snapshot()
	if again[0].Message != "original" {
		t.Errorf("Snapshot leaked mutation: got %q, want %q", again[0].Message, "original")
	}
}
