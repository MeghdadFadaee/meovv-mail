package stalwart

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(base string, insecure bool) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} // #nosec G402 -- explicitly controlled for deployments that re-encrypt to a private Stalwart endpoint.
	return &Client{BaseURL: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 15 * time.Second, Transport: transport}}
}

func (c *Client) Bootstrap(ctx context.Context, bootstrapURL, recoveryAdmin, hostname, domain string) ([]byte, error) {
	parts := strings.SplitN(recoveryAdmin, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("Stalwart recovery administrator is not configured")
	}
	payload := map[string]any{
		"using": []string{"urn:ietf:params:jmap:core", "urn:stalwart:jmap"},
		"methodCalls": []any{[]any{"x:Bootstrap/set", map[string]any{"update": map[string]any{"singleton": map[string]any{
			"serverHostname": hostname, "defaultDomain": domain, "requestTlsCertificate": false, "generateDkimKeys": true,
			"dataStore": map[string]any{"@type": "RocksDb", "path": "/var/lib/stalwart/"},
			"blobStore": map[string]any{"@type": "Default"}, "searchStore": map[string]any{"@type": "Default"}, "inMemoryStore": map[string]any{"@type": "Default"},
			"directory": map[string]any{"@type": "Internal"}, "tracer": map[string]any{"@type": "Console"}, "dnsServer": map[string]any{"@type": "Manual"},
		}}}, "bootstrap"}},
	}
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(bootstrapURL, "/")+"/api", bytes.NewReader(raw))
	req.SetBasicAuth(parts[0], parts[1])
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("configure Stalwart: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("configure Stalwart: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if bytes.Contains(body, []byte(`"type":"error"`)) || bytes.Contains(body, []byte(`"notUpdated"`)) {
		return nil, fmt.Errorf("Stalwart rejected bootstrap configuration: %s", strings.TrimSpace(string(body)))
	}
	return body, nil
}
func (c *Client) Ready(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/.well-known/jmap", nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("stalwart returned %s", resp.Status)
	}
	return nil
}

type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

func (c *Client) Exchange(ctx context.Context, clientID, code, verifier, redirectURI string) (Tokens, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirectURI}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/auth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Tokens{}, fmt.Errorf("token exchange: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out Tokens
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}
func (c *Client) Refresh(ctx context.Context, clientID, refreshToken string) (Tokens, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refreshToken}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/auth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Tokens{}, fmt.Errorf("token refresh: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out Tokens
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}
func (c *Client) ProxyJMAP(ctx context.Context, token string, body []byte) (int, http.Header, []byte, error) {
	return c.proxy(ctx, token, "/jmap", body)
}

// ProxyManagement keeps Stalwart's pre-1.0 management schema behind the MEOVV
// adapter. The browser never receives an administrator token or direct access
// to Stalwart's management endpoint.
func (c *Client) ProxyManagement(ctx context.Context, token string, body []byte) (int, http.Header, []byte, error) {
	return c.proxy(ctx, token, "/api", body)
}

func (c *Client) JMAPSession(ctx context.Context, token string) (int, http.Header, []byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/.well-known/jmap", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, resp.Header, raw, err
}

func (c *Client) EventStream(ctx context.Context, token string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/jmap/eventsource/?types=Email,Mailbox&closeafter=45&ping=15", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Transport: c.HTTP.Transport}
	return client.Do(req)
}

func (c *Client) proxy(ctx context.Context, token, path string, body []byte) (int, http.Header, []byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	return resp.StatusCode, resp.Header, raw, err
}
