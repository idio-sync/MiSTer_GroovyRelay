package torrent

import "testing"

func TestNameAndDisplayName(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: testBridgeConfig(t)})
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "torrent" {
		t.Fatalf("Name = %q, want torrent", a.Name())
	}
	if a.DisplayName() != "Torrent" {
		t.Fatalf("DisplayName = %q, want Torrent", a.DisplayName())
	}
}
