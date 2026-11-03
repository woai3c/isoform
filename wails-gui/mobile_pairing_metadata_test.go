package main

import (
	"encoding/json"
	"os"
	"testing"
)

// Keep the pre-metadata vector usable while accepting explicit Simulator facts
// from the same ios platform and ordinary device names that shipping phones use.
func TestMobilePairingSimulatorMetadataPreservesLegacyClaims(t *testing.T) {
	raw, err := os.ReadFile(mobileVectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var vectors mobileVectorsFile
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, simulator string
		wantSimulator   bool
		wantError       bool
	}{
		{name: "legacy omitted"},
		{name: "physical phone", simulator: "false"},
		{name: "Simulator with ordinary device name", simulator: "true", wantSimulator: true},
		{name: "invalid string", simulator: `"true"`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(vectors.Claim.Plaintext), &fields); err != nil {
				t.Fatal(err)
			}
			if _, exists := fields["simulator"]; exists {
				t.Fatal("the compatibility fixture must remain an older claim without simulator metadata")
			}
			fields["deviceName"] = json.RawMessage(`"iPhone 17 Pro"`)
			if tc.simulator != "" {
				fields["simulator"] = json.RawMessage(tc.simulator)
			}
			plaintext, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			claim, err := decodeMobileClaim(plaintext, vectors.DeviceID, vectors.DevicePublic)
			if (err != nil) != tc.wantError {
				t.Fatalf("claim error = %v, want error %v", err, tc.wantError)
			}
			if tc.wantError {
				return
			}
			if claim.Platform != "ios" || claim.DeviceName != "iPhone 17 Pro" || claim.Simulator != tc.wantSimulator {
				t.Fatalf("device facts: platform=%q name=%q simulator=%v", claim.Platform, claim.DeviceName, claim.Simulator)
			}
			if len(claim.Scopes) != 4 {
				t.Fatalf("advisory metadata changed granted scopes: %v", claim.Scopes)
			}
		})
	}
}
