package main

import "testing"

func TestSignedCompatibilityManifest(t *testing.T) {
	if err := verifyCompatibility("../../release/compatibility.json", "../../release/compatibility.sig"); err != nil {
		t.Fatal(err)
	}
}
