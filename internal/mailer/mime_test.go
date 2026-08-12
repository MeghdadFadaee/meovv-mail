package mailer

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestBuildMIMEAndProtectAttachmentFilename(t *testing.T) {
	raw, recipients, err := Build(Message{
		From:        "MEOVV <sender@example.com>",
		To:          []string{"Person <PERSON@example.net>"},
		Subject:     "سلام — status",
		Text:        "Hello",
		HTML:        "<p>Hello</p>",
		Headers:     map[string]string{"X-Campaign-ID": "welcome"},
		Attachments: []Attachment{{Filename: "../../private/statement.pdf", ContentType: "application/pdf", ContentBase64: base64.StdEncoding.EncodeToString([]byte("content"))}},
	}, 25<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 || recipients[0] != "person@example.net" {
		t.Fatalf("unexpected recipients: %#v", recipients)
	}
	message := string(raw)
	if strings.Contains(message, "../") || !strings.Contains(message, `filename="statement.pdf"`) {
		t.Fatalf("unsafe or missing filename in MIME:\n%s", message)
	}
	if strings.Contains(message, "Bcc:") {
		t.Fatal("Bcc header leaked into MIME")
	}
}

func TestBuildRejectsHeaderInjectionAndDecodedLimit(t *testing.T) {
	_, _, err := Build(Message{From: "sender@example.com", To: []string{"to@example.net"}, Text: "hello", Headers: map[string]string{"X-Test": "ok\r\nBcc: stolen@example.net"}}, 1024)
	if err == nil {
		t.Fatal("expected injected header to be rejected")
	}
	_, _, err = Build(Message{From: "sender@example.com", To: []string{"to@example.net"}, Text: strings.Repeat("x", 1025)}, 1024)
	if err == nil || !strings.Contains(err.Error(), "decoded message") {
		t.Fatalf("expected decoded size limit, got %v", err)
	}
}

func TestBuildRequiresRecipient(t *testing.T) {
	_, _, err := Build(Message{From: "sender@example.com", Text: "hello"}, 1024)
	if err == nil {
		t.Fatal("expected recipient validation error")
	}
}
