package entity

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIntegrationSettingDoesNotSerializeInternalRevision(t *testing.T) {
	encoded, err := json.Marshal(IntegrationSetting{
		Provider: "jenkins", ConfigData: `{"token":"must-not-leak"}`, Revision: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(encoded)
	if strings.Contains(serialized, "revision") || strings.Contains(serialized, "config_data") ||
		strings.Contains(serialized, "must-not-leak") {
		t.Fatalf("internal integration settings leaked through JSON: %s", serialized)
	}
}
