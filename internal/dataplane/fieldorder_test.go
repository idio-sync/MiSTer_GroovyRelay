package dataplane

import "testing"

func TestInitialFieldForOrder(t *testing.T) {
	cases := []struct {
		order string
		want  uint8
	}{
		{"tff", 0},
		{"bff", 1},
		{"", 0},
	}
	for _, tc := range cases {
		if got := initialFieldForOrder(tc.order); got != tc.want {
			t.Fatalf("initialFieldForOrder(%q) = %d, want %d", tc.order, got, tc.want)
		}
	}
}

func TestTerminalFieldForOrder(t *testing.T) {
	cases := []struct {
		order string
		want  uint8
	}{
		{"tff", 1},
		{"bff", 0},
		{"", 1},
	}
	for _, tc := range cases {
		if got := terminalFieldForOrder(tc.order); got != tc.want {
			t.Fatalf("terminalFieldForOrder(%q) = %d, want %d", tc.order, got, tc.want)
		}
	}
}
