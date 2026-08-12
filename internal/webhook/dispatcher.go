package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/meovv-mail/meovv-mail/internal/app"
	"github.com/meovv-mail/meovv-mail/internal/store"
)

type Dispatcher struct {
	Store  *store.Store
	Cipher app.TokenCipher
	HTTP   *http.Client
	Now    func() time.Time
}

func New(s *store.Store, cipher app.TokenCipher) *Dispatcher {
	return &Dispatcher{Store: s, Cipher: cipher, HTTP: &http.Client{Timeout: 10 * time.Second}, Now: time.Now}
}

func Signature(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", timestamp)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
func Verify(secret, signature, timestamp string, body []byte, maxSkew time.Duration) bool {
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || time.Since(time.Unix(unix, 0)) > maxSkew || time.Until(time.Unix(unix, 0)) > maxSkew {
		return false
	}
	expected := Signature(secret, unix, body)
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(signature)))
}

func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.DispatchOnce(ctx)
		}
	}
}
func (d *Dispatcher) DispatchOnce(ctx context.Context) {
	items, err := d.Store.DueWebhooks(ctx, d.Now(), 25)
	if err != nil {
		return
	}
	for _, item := range items {
		d.dispatch(ctx, item)
	}
}
func (d *Dispatcher) dispatch(ctx context.Context, item store.WebhookDelivery) {
	endpoint, err := d.Store.GetWebhookEndpoint(ctx, item.EndpointID)
	if err != nil {
		return
	}
	if err = ValidatePublicURL(ctx, endpoint.URL); err != nil {
		d.fail(ctx, item, err)
		return
	}
	secretBytes, err := base64.RawStdEncoding.DecodeString(endpoint.Secret)
	if err != nil {
		d.fail(ctx, item, fmt.Errorf("decode webhook secret: %w", err))
		return
	}
	secret, err := d.Cipher.Decrypt(secretBytes)
	if err != nil {
		d.fail(ctx, item, fmt.Errorf("decrypt webhook secret: %w", err))
		return
	}
	timestamp := d.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL, bytes.NewReader(item.Payload))
	if err != nil {
		d.fail(ctx, item, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "MEOVV-Mail-Webhooks/1.0")
	req.Header.Set("X-MEOVV-Event-ID", item.EventID)
	req.Header.Set("X-MEOVV-Event", item.EventType)
	req.Header.Set("X-MEOVV-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("X-MEOVV-Signature", "v1="+Signature(secret, timestamp, item.Payload))
	resp, err := d.HTTP.Do(req)
	if err == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = d.Store.CompleteWebhook(ctx, item.ID, true, time.Time{}, "")
			return
		}
		err = fmt.Errorf("endpoint returned %s", resp.Status)
	}
	d.fail(ctx, item, err)
}
func (d *Dispatcher) fail(ctx context.Context, item store.WebhookDelivery, err error) {
	age := d.Now().Sub(item.CreatedAt)
	if age >= 24*time.Hour {
		_ = d.Store.CompleteWebhook(ctx, item.ID, false, d.Now().Add(365*24*time.Hour), "retry window expired: "+err.Error())
		return
	}
	delay := time.Duration(math.Min(3600, math.Pow(2, float64(item.Attempt))*15)) * time.Second
	_ = d.Store.CompleteWebhook(ctx, item.ID, false, d.Now().Add(delay), err.Error())
}

func ValidatePublicURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return fmt.Errorf("webhook URL must be public HTTPS")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", parsed.Hostname())
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("webhook URL resolves to a non-public address")
		}
	}
	return nil
}
