package mailer

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Attachment struct{ Filename, ContentType, ContentBase64 string }
type Message struct {
	From                         string
	To, CC, BCC                  []string
	ReplyTo, Subject, Text, HTML string
	Headers                      map[string]string
	Attachments                  []Attachment
}

var allowedHeader = regexp.MustCompile(`(?i)^(X-[A-Za-z0-9-]{1,64}|List-Unsubscribe|List-Unsubscribe-Post)$`)

func Build(m Message, maxBytes int64) ([]byte, []string, error) {
	from, err := mail.ParseAddress(m.From)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid from address: %w", err)
	}
	recipients := append(append(append([]string{}, m.To...), m.CC...), m.BCC...)
	if len(recipients) == 0 {
		return nil, nil, fmt.Errorf("at least one recipient is required")
	}
	for i, value := range recipients {
		address, err := mail.ParseAddress(value)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid recipient %q", value)
		}
		recipients[i] = strings.ToLower(address.Address)
	}
	var out bytes.Buffer
	boundary := randomBoundary()
	bodyBoundary := randomBoundary()
	headers := textproto.MIMEHeader{}
	headers.Set("From", from.String())
	headers.Set("To", strings.Join(m.To, ", "))
	if len(m.CC) > 0 {
		headers.Set("Cc", strings.Join(m.CC, ", "))
	}
	if m.ReplyTo != "" {
		if _, err = mail.ParseAddress(m.ReplyTo); err != nil {
			return nil, nil, fmt.Errorf("invalid reply-to")
		}
		headers.Set("Reply-To", m.ReplyTo)
	}
	headers.Set("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	headers.Set("Date", time.Now().Format(time.RFC1123Z))
	headers.Set("MIME-Version", "1.0")
	headers.Set("Message-ID", fmt.Sprintf("<%s@meovv.local>", strings.ToLower(randomBoundary())))
	for k, v := range m.Headers {
		if !allowedHeader.MatchString(k) || strings.ContainsAny(k+v, "\r\n") {
			return nil, nil, fmt.Errorf("header %q is not allowed", k)
		}
		headers.Set(k, v)
	}
	withAttachments := len(m.Attachments) > 0
	decodedBytes := int64(len([]byte(m.Text)) + len([]byte(m.HTML)))
	if withAttachments {
		headers.Set("Content-Type", fmt.Sprintf(`multipart/mixed; boundary="%s"`, boundary))
	} else {
		headers.Set("Content-Type", fmt.Sprintf(`multipart/alternative; boundary="%s"`, bodyBoundary))
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&out, "%s: %s\r\n", k, headers.Get(k))
	}
	out.WriteString("\r\n")
	if withAttachments {
		fmt.Fprintf(&out, "--%s\r\nContent-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary, bodyBoundary)
	}
	writeAlternative(&out, bodyBoundary, m.Text, m.HTML)
	if withAttachments {
		for _, a := range m.Attachments {
			filename := filepath.Base(strings.ReplaceAll(a.Filename, "\\", "/"))
			if filename == "." || filename == "" {
				return nil, nil, fmt.Errorf("attachment filename is required")
			}
			raw, err := base64.StdEncoding.DecodeString(a.ContentBase64)
			if err != nil {
				return nil, nil, fmt.Errorf("attachment %q is not valid base64", filename)
			}
			decodedBytes += int64(len(raw))
			if decodedBytes > maxBytes {
				return nil, nil, fmt.Errorf("decoded message exceeds %d bytes", maxBytes)
			}
			contentType := a.ContentType
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			fmt.Fprintf(&out, "--%s\r\nContent-Type: %s; name=%q\r\nContent-Disposition: attachment; filename=%q\r\nContent-Transfer-Encoding: base64\r\n\r\n", boundary, contentType, filename, filename)
			writeBase64(&out, raw)
			out.WriteString("\r\n")
		}
		fmt.Fprintf(&out, "--%s--\r\n", boundary)
	}
	if decodedBytes > maxBytes {
		return nil, nil, fmt.Errorf("decoded message exceeds %d bytes", maxBytes)
	}
	return out.Bytes(), recipients, nil
}

func writeAlternative(out *bytes.Buffer, boundary, text, html string) {
	if text == "" {
		text = stripHTML(html)
	}
	fmt.Fprintf(out, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n", boundary)
	writeBase64(out, []byte(text))
	out.WriteString("\r\n")
	if html != "" {
		fmt.Fprintf(out, "--%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n", boundary)
		writeBase64(out, []byte(html))
		out.WriteString("\r\n")
	}
	fmt.Fprintf(out, "--%s--\r\n", boundary)
}
func writeBase64(out *bytes.Buffer, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		out.WriteString(encoded[:76])
		out.WriteString("\r\n")
		encoded = encoded[76:]
	}
	out.WriteString(encoded)
}
func randomBoundary() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func stripHTML(value string) string {
	return strings.TrimSpace(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(value, " "))
}
