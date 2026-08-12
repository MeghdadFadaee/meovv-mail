package httpapi

import (
	"testing"

	"github.com/meovv-mail/meovv-mail/internal/store"
)

func TestEventNormalization(t *testing.T) {
	tests := map[string][2]string{
		"queue.queue-message":    {"queued", "message.queued"},
		"delivery.delivered":     {"delivered", "message.delivered"},
		"delivery.dsn-temp-fail": {"deferred", "message.deferred"},
		"delivery.dsn-perm-fail": {"bounced", "message.bounced"},
		"delivery.double-bounce": {"failed", "message.failed"},
	}
	for event, expected := range tests {
		state, external := normalizeEvent(event)
		if state != expected[0] || external != expected[1] {
			t.Errorf("%s = %s/%s", event, state, external)
		}
	}
}

func TestScopesAndApprovedSender(t *testing.T) {
	key := store.APIKey{Scopes: []string{"messages.send"}, AllowedSenders: []string{"alerts@example.com", "*@internal.example.com"}}
	if !hasScope(key, "messages.send") || hasScope(key, "messages.status") {
		t.Fatal("scope evaluation failed")
	}
	if !allowedSender(key, "Alerts <alerts@example.com>") || !allowedSender(key, "robot@internal.example.com") || allowedSender(key, "attacker@example.net") {
		t.Fatal("sender identity evaluation failed")
	}
}
