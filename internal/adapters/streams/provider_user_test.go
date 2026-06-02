package streams

import (
	"encoding/json"
	"testing"
)

func TestProviderDefinition_NewFieldsRoundTrip(t *testing.T) {
	raw := `{
		"id": "user:f1-tv",
		"type": "user",
		"display_name": "F1 TV",
		"badge_color": "amber",
		"groups": [{"id": "races", "name": "Races", "order": 0}],
		"channels": [{"id": "live", "name": "Live", "kind": "single", "url": "https://twitch.tv/formula1", "order": 0}]
	}`
	var def ProviderDefinition
	if err := json.Unmarshal([]byte(raw), &def); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if def.BadgeColor != "amber" {
		t.Errorf("BadgeColor = %q, want amber", def.BadgeColor)
	}
	if len(def.Channels) != 1 || def.Channels[0].Kind != "single" {
		t.Errorf("Channels[0].Kind = %q, want single", def.Channels[0].Kind)
	}

	out, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ProviderDefinition
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back.BadgeColor != "amber" || back.Channels[0].Kind != "single" {
		t.Errorf("round-trip lost new fields: %+v", back)
	}
}
