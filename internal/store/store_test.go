package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAPIKeyHashingListingAndRevocation(t *testing.T) {
	ctx := context.Background()
	db := testStore(t)
	key := APIKey{ID: "key_1", Name: "Production", Prefix: "meovv_abc", Scopes: []string{"messages.send"}, AllowedSenders: []string{"*@example.com"}, RateLimit: 60, CreatedAt: time.Now()}
	if err := db.CreateAPIKey(ctx, key, "meovv_secret"); err != nil {
		t.Fatal(err)
	}
	got, err := db.AuthenticateAPIKey(ctx, "meovv_secret")
	if err != nil || got.ID != key.ID || len(got.Scopes) != 1 {
		t.Fatalf("authenticate = %#v, %v", got, err)
	}
	keys, err := db.ListAPIKeys(ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("list = %#v, %v", keys, err)
	}
	if err = db.RevokeAPIKey(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.AuthenticateAPIKey(ctx, "meovv_secret"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked key authenticated: %v", err)
	}
}

func TestIdempotencyAndRecipientStateRollup(t *testing.T) {
	ctx := context.Background()
	db := testStore(t)
	now := time.Now().UTC()
	key := APIKey{ID: "key_1", Name: "test", Prefix: "meovv_test", Scopes: []string{"messages.send"}, AllowedSenders: []string{"sender@example.com"}, RateLimit: 60, CreatedAt: now}
	if err := db.CreateAPIKey(ctx, key, "secret"); err != nil {
		t.Fatal(err)
	}
	message := Message{ID: "msg_1", APIKeyID: key.ID, Sender: "sender@example.com", Subject: "test", Status: "queued", SubmittedAt: now, UpdatedAt: now, Recipients: []Recipient{{Address: "a@example.net", Status: "queued"}, {Address: "b@example.net", Status: "queued"}}}
	if prior, err := db.InsertMessage(ctx, message, "retry-token", "digest-a"); err != nil || prior != "" {
		t.Fatalf("insert = %q, %v", prior, err)
	}
	duplicate := message
	duplicate.ID = "msg_2"
	if prior, err := db.InsertMessage(ctx, duplicate, "retry-token", "digest-a"); err != nil || prior != "msg_1" {
		t.Fatalf("replay = %q, %v", prior, err)
	}
	if _, err := db.InsertMessage(ctx, duplicate, "retry-token", "digest-b"); err == nil {
		t.Fatal("expected conflicting idempotency digest")
	}
	if err := db.UpdateRecipient(ctx, "msg_1", "a@example.net", "delivered", "250 ok", now); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRecipient(ctx, "msg_1", "b@example.net", "bounced", "550 no", now); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMessage(ctx, "msg_1")
	if err != nil || got.Status != "partial" {
		t.Fatalf("status = %q, %v", got.Status, err)
	}
}

func TestWebhookEndpointLifecycle(t *testing.T) {
	ctx := context.Background()
	db := testStore(t)
	endpoint := WebhookEndpoint{ID: "wh_1", Name: "Billing", URL: "https://example.com/hook", Secret: "secret", Events: []string{"message.delivered"}, Enabled: true, CreatedAt: time.Now()}
	if err := db.CreateWebhookEndpoint(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListWebhookEndpoints(ctx)
	if err != nil || len(items) != 1 || items[0].Secret != "" {
		t.Fatalf("list = %#v, %v", items, err)
	}
	if err = db.DeleteWebhookEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
}
