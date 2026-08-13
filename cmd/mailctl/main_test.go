package main

import (
	"strings"
	"testing"
)

func TestSignedCompatibilityManifest(t *testing.T) {
	if err := verifyCompatibility("../../release/compatibility.json", "../../release/compatibility.sig"); err != nil {
		t.Fatal(err)
	}
}

func TestCreatedObjectID(t *testing.T) {
	raw := []byte(`{"methodResponses":[["x:Certificate/set",{"created":{"certbot":{"id":"cert_123"}}},"certificate"]]}`)
	id, err := createdObjectID(raw, "certbot")
	if err != nil {
		t.Fatal(err)
	}
	if id != "cert_123" {
		t.Fatalf("got %q", id)
	}
}

func TestCreatedObjectIDRejectsNotCreated(t *testing.T) {
	raw := []byte(`{"methodResponses":[["x:Certificate/set",{"notCreated":{"certbot":{"type":"invalidProperties"}}},"certificate"]]}`)
	_, err := createdObjectID(raw, "certbot")
	if err == nil || !strings.Contains(err.Error(), "invalidProperties") {
		t.Fatalf("expected Stalwart error, got %v", err)
	}
}

func TestJMAPSucceededRejectsFailedUpdate(t *testing.T) {
	raw := []byte(`{"methodResponses":[["x:SystemSettings/set",{"notUpdated":{"singleton":{"type":"forbidden"}}},"settings"]]}`)
	if err := jmapSucceeded(raw); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("expected forbidden update, got %v", err)
	}
}

func TestConfigureTLSRejectsRemoteManagementURL(t *testing.T) {
	err := configureTLS([]string{"--directory", t.TempDir(), "--url", "http://mail.example.com/jmap"})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected loopback URL rejection, got %v", err)
	}
}

func TestConfigureTLSRejectsHTTPSManagementURL(t *testing.T) {
	err := configureTLS([]string{"--directory", t.TempDir(), "--url", "https://127.0.0.1:8081/jmap"})
	if err == nil || !strings.Contains(err.Error(), "loopback HTTP URL") {
		t.Fatalf("expected plain loopback HTTP requirement, got %v", err)
	}
}
