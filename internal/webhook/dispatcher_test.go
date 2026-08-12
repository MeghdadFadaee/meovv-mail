package webhook

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/meovv-mail/meovv-mail/internal/store"
)

func TestSignatureAndVerification(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	timestamp := time.Now().Unix()
	signature := Signature("secret", timestamp, body)
	if !Verify("secret", signature, fmtInt(timestamp), body, 5*time.Minute) {
		t.Fatal("valid signature was rejected")
	}
	if Verify("wrong", signature, fmtInt(timestamp), body, 5*time.Minute) {
		t.Fatal("wrong secret was accepted")
	}
}

func TestRetryScheduleStartsAtFifteenSeconds(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	endpoint := store.WebhookEndpoint{ID: "wh_1", Name: "test", URL: "https://example.com", Secret: "secret", Events: []string{"message.failed"}, Enabled: true, CreatedAt: now}
	if err = db.CreateWebhookEndpoint(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	if err = db.EnqueueEvent(ctx, "evt_1", "message.failed", []byte(`{}`), now); err != nil {
		t.Fatal(err)
	}
	items, err := db.DueWebhooks(ctx, now, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("due = %#v, %v", items, err)
	}
	dispatcher := &Dispatcher{Store: db, Now: func() time.Time { return now }}
	dispatcher.fail(ctx, items[0], context.DeadlineExceeded)
	if due, _ := db.DueWebhooks(ctx, now.Add(14*time.Second), 10); len(due) != 0 {
		t.Fatal("delivery retried too early")
	}
	if due, _ := db.DueWebhooks(ctx, now.Add(15*time.Second), 10); len(due) != 1 {
		t.Fatal("delivery was not scheduled after 15 seconds")
	}
}

func TestRejectsPrivateWebhookTargets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ValidatePublicURL(ctx, "http://example.com/hook"); err == nil {
		t.Fatal("plain HTTP was accepted")
	}
	if err := ValidatePublicURL(ctx, "https://127.0.0.1/hook"); err == nil {
		t.Fatal("loopback target was accepted")
	}
}

func fmtInt(value int64) string {
	return strconv.FormatInt(value, 10)
}
