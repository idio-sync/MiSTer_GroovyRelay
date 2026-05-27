package adapters

import (
	"encoding/json"
	"testing"
)

func TestPresetStarResult_StarredOmitsCleared(t *testing.T) {
	t.Parallel()
	res := PresetStarResult{Starred: true, Slot: 7}
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(body)
	want := `{"starred":true,"slot":7}`
	if got != want {
		t.Errorf("Marshal(Starred) = %s, want %s", got, want)
	}
}

func TestPresetStarResult_UnstarredOmitsSlot(t *testing.T) {
	t.Parallel()
	res := PresetStarResult{Starred: false, Cleared: []int{3, 9}}
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(body)
	want := `{"starred":false,"cleared":[3,9]}`
	if got != want {
		t.Errorf("Marshal(Unstarred) = %s, want %s", got, want)
	}
}

func TestPresetStarResult_UnstarredEmptyClearedOmitsCleared(t *testing.T) {
	t.Parallel()
	// Empty Cleared slice on the unstarred no-op path: omitempty kicks in.
	res := PresetStarResult{Starred: false}
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(body)
	want := `{"starred":false}`
	if got != want {
		t.Errorf("Marshal(Unstarred no-op) = %s, want %s", got, want)
	}
}
