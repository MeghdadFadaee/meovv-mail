package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/meovv-mail/meovv-mail/internal/app"
	"github.com/meovv-mail/meovv-mail/internal/config"
	"github.com/meovv-mail/meovv-mail/internal/mailer"
	"github.com/meovv-mail/meovv-mail/internal/stalwart"
	"github.com/meovv-mail/meovv-mail/internal/store"
	"github.com/meovv-mail/meovv-mail/internal/webhook"
)

type Server struct {
	Config    config.Config
	Store     *store.Store
	Mailer    mailer.Sender
	Stalwart  *stalwart.Client
	Cipher    app.TokenCipher
	Logger    *slog.Logger
	StaticDir string
	started   time.Time
	accepted  atomic.Uint64
	failed    atomic.Uint64
	limiter   *rateLimiter
}

func New(cfg config.Config, st *store.Store, sender mailer.Sender, client *stalwart.Client, cipher app.TokenCipher, logger *slog.Logger, staticDir string) *Server {
	return &Server{Config: cfg, Store: st, Mailer: sender, Stalwart: client, Cipher: cipher, Logger: logger, StaticDir: staticDir, started: time.Now(), limiter: newRateLimiter()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /api/setup/status", s.setupStatus)
	mux.HandleFunc("POST /api/setup/complete", s.setupComplete)
	mux.HandleFunc("POST /api/session/exchange", s.sessionExchange)
	mux.HandleFunc("GET /api/session", s.sessionStatus)
	mux.HandleFunc("POST /api/session/logout", s.logout)
	mux.HandleFunc("POST /api/mail/jmap", s.jmap)
	mux.HandleFunc("GET /api/mail/session", s.jmapSession)
	mux.HandleFunc("GET /api/mail/events", s.jmapEvents)
	mux.HandleFunc("GET /api/admin/overview", s.adminOverview)
	mux.HandleFunc("POST /api/admin/stalwart", s.adminStalwart)
	mux.HandleFunc("GET /api/admin/api-keys", s.listAPIKeys)
	mux.HandleFunc("POST /api/admin/api-keys", s.createAPIKey)
	mux.HandleFunc("DELETE /api/admin/api-keys/{id}", s.revokeAPIKey)
	mux.HandleFunc("GET /api/admin/webhooks", s.listWebhooks)
	mux.HandleFunc("POST /api/admin/webhooks", s.createWebhook)
	mux.HandleFunc("DELETE /api/admin/webhooks/{id}", s.deleteWebhook)
	mux.HandleFunc("POST /api/internal/stalwart/events", s.internalEvents)
	mux.HandleFunc("POST /api/v1/messages", s.authAPI("messages.send", s.sendMessage))
	mux.HandleFunc("GET /api/v1/messages", s.authAPI("messages.status", s.listMessages))
	mux.HandleFunc("GET /api/v1/messages/{id}", s.authAPI("messages.status", s.getMessage))
	mux.Handle("/", s.frontend())
	return s.security(s.csrf(mux))
}

func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || strings.HasPrefix(r.URL.Path, "/api/v1/") || r.URL.Path == "/api/internal/stalwart/events" {
			next.ServeHTTP(w, r)
			return
		}
		origin := strings.TrimRight(r.Header.Get("Origin"), "/")
		if origin != "" && origin != s.Config.PublicURL {
			problem(w, http.StatusForbidden, "cross_site_request", "Cross-site state changes are not allowed.")
			return
		}
		if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
			problem(w, http.StatusForbidden, "cross_site_request", "Cross-site state changes are not allowed.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self' wss:; frame-src 'none'; object-src 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "uptime_seconds": int(time.Since(s.started).Seconds())})
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{"database": "ok", "stalwart": "ok"}
	status := 200
	if err := s.Store.Ping(ctx); err != nil {
		checks["database"] = err.Error()
		status = 503
	}
	if err := s.Stalwart.Ready(ctx); err != nil {
		checks["stalwart"] = err.Error()
		status = 503
	}
	writeJSON(w, status, map[string]any{"status": map[bool]string{true: "ok", false: "degraded"}[status == 200], "checks": checks})
}
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP meovv_messages_accepted_total Messages accepted by the REST API.\n# TYPE meovv_messages_accepted_total counter\nmeovv_messages_accepted_total %d\n# HELP meovv_messages_failed_total Message submissions that failed.\n# TYPE meovv_messages_failed_total counter\nmeovv_messages_failed_total %d\n", s.accepted.Load(), s.failed.Load())
}

type setupRequest struct {
	Hostname      string `json:"hostname"`
	PrimaryDomain string `json:"primary_domain"`
	AdminEmail    string `json:"admin_email"`
	Organization  string `json:"organization"`
	Accent        string `json:"accent"`
	DeliveryMode  string `json:"delivery_mode"`
	RelayHost     string `json:"relay_host,omitempty"`
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	var setup setupRequest
	err := s.Store.GetSetting(r.Context(), "installation", &setup)
	writeJSON(w, 200, map[string]any{"configured": err == nil, "installation": func() any {
		if err == nil {
			return setup
		}
		return nil
	}()})
}
func (s *Server) setupComplete(w http.ResponseWriter, r *http.Request) {
	if s.Config.BootstrapToken == "" || !hmac.Equal([]byte(r.Header.Get("X-Bootstrap-Token")), []byte(s.Config.BootstrapToken)) {
		problem(w, 401, "invalid_bootstrap_token", "The one-time bootstrap token is missing or invalid.")
		return
	}
	var in setupRequest
	if !decodeJSON(w, r, &in, 64<<10) {
		return
	}
	if !validHostname(in.Hostname) || !validDomain(in.PrimaryDomain) {
		problem(w, 422, "invalid_installation", "Hostname and primary domain must be valid DNS names.")
		return
	}
	if address, err := mail.ParseAddress(in.AdminEmail); err != nil || !strings.HasSuffix(strings.ToLower(address.Address), "@"+strings.ToLower(in.PrimaryDomain)) {
		problem(w, 422, "invalid_admin", "Administrator email must belong to the primary domain.")
		return
	}
	if in.DeliveryMode != "direct" && in.DeliveryMode != "relay" {
		problem(w, 422, "invalid_delivery_mode", "Delivery mode must be direct or relay.")
		return
	}
	bootstrapResult, err := s.Stalwart.Bootstrap(r.Context(), s.Config.StalwartBootstrapURL, s.Config.StalwartRecoveryAdmin, in.Hostname, in.PrimaryDomain)
	if err != nil {
		problem(w, 502, "mail_core_setup_failed", err.Error())
		return
	}
	if err := s.Store.SetSetting(r.Context(), "installation", in); err != nil {
		problem(w, 500, "storage_error", "Could not save the installation.")
		return
	}
	_ = s.Store.Audit(r.Context(), app.RandomID("aud_"), in.AdminEmail, "installation.completed", in.Hostname, in)
	writeJSON(w, 201, map[string]any{"configured": true, "next": "Save the one-time administrator result, restart Stalwart, verify permanent administrator sign-in, then run mailctl harden.", "mail_core_result": json.RawMessage(bootstrapResult), "dns_checks": []string{"A/AAAA", "MX", "PTR", "SPF", "DKIM", "DMARC", "TLS"}})
}

func (s *Server) sessionExchange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ClientCode   string `json:"client_code"`
		CodeVerifier string `json:"code_verifier"`
		Account      string `json:"account"`
		ClientID     string `json:"client_id"`
		RedirectURI  string `json:"redirect_uri"`
	}
	if !decodeJSON(w, r, &in, 16<<10) {
		return
	}
	if in.ClientID == "" {
		in.ClientID = "meovv-web"
	}
	if in.RedirectURI == "" {
		in.RedirectURI = s.Config.PublicURL + "/auth/callback"
	}
	tokens, err := s.Stalwart.Exchange(r.Context(), in.ClientID, in.ClientCode, in.CodeVerifier, in.RedirectURI)
	if err != nil {
		problem(w, 401, "exchange_failed", err.Error())
		return
	}
	access, _ := s.Cipher.Encrypt(tokens.AccessToken)
	refresh, _ := s.Cipher.Encrypt(tokens.RefreshToken)
	now := time.Now()
	id := app.RandomID("ses_")
	expiry := now.Add(time.Duration(tokens.ExpiresIn) * time.Second)
	if expiry.Equal(now) {
		expiry = now.Add(time.Hour)
	}
	sessionExpiry := now.Add(30 * 24 * time.Hour)
	if err = s.Store.CreateSession(r.Context(), store.Session{ID: id, Account: in.Account, AccessToken: access, RefreshToken: refresh, ExpiresAt: expiry, SessionExpiresAt: sessionExpiry, CreatedAt: now, LastSeenAt: now}); err != nil {
		problem(w, 500, "session_failed", "Could not establish the session.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "meovv_session", Value: id, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.Config.PublicURL, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: int(time.Until(sessionExpiry).Seconds())})
	writeJSON(w, 200, map[string]any{"authenticated": true, "account": in.Account})
}
func (s *Server) sessionStatus(w http.ResponseWriter, r *http.Request) {
	session, _, err := s.session(r)
	if err != nil {
		problem(w, 401, "authentication_required", "Sign in to continue.")
		return
	}
	writeJSON(w, 200, map[string]any{"authenticated": true, "account": session.Account})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("meovv_session"); err == nil {
		_ = s.Store.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "meovv_session", Value: "", Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.Config.PublicURL, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(204)
}
func (s *Server) session(r *http.Request) (store.Session, string, error) {
	cookie, err := r.Cookie("meovv_session")
	if err != nil {
		return store.Session{}, "", store.ErrNotFound
	}
	session, err := s.Store.GetSession(r.Context(), cookie.Value)
	if err != nil {
		return session, "", err
	}
	if time.Until(session.ExpiresAt) < time.Minute {
		refreshToken, decryptErr := s.Cipher.Decrypt(session.RefreshToken)
		if decryptErr != nil {
			return session, "", decryptErr
		}
		tokens, refreshErr := s.Stalwart.Refresh(r.Context(), "meovv-web", refreshToken)
		if refreshErr != nil {
			_ = s.Store.DeleteSession(r.Context(), session.ID)
			return session, "", refreshErr
		}
		access, encryptErr := s.Cipher.Encrypt(tokens.AccessToken)
		if encryptErr != nil {
			return session, "", encryptErr
		}
		if tokens.RefreshToken == "" {
			tokens.RefreshToken = refreshToken
		}
		refresh, encryptErr := s.Cipher.Encrypt(tokens.RefreshToken)
		if encryptErr != nil {
			return session, "", encryptErr
		}
		expires := time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
		if tokens.ExpiresIn <= 0 {
			expires = time.Now().Add(time.Hour)
		}
		if err = s.Store.UpdateSessionTokens(r.Context(), session.ID, access, refresh, expires); err != nil {
			return session, "", err
		}
		session.AccessToken = access
		session.RefreshToken = refresh
		session.ExpiresAt = expires
	}
	token, err := s.Cipher.Decrypt(session.AccessToken)
	return session, token, err
}
func (s *Server) jmap(w http.ResponseWriter, r *http.Request) {
	_, token, err := s.session(r)
	if err != nil {
		problem(w, 401, "authentication_required", "Sign in to access this mailbox.")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		problem(w, 400, "invalid_body", err.Error())
		return
	}
	status, headers, response, err := s.Stalwart.ProxyJMAP(r.Context(), token, raw)
	if err != nil {
		problem(w, 502, "mail_service_error", err.Error())
		return
	}
	if content := headers.Get("Content-Type"); content != "" {
		w.Header().Set("Content-Type", content)
	}
	w.WriteHeader(status)
	_, _ = w.Write(response)
}
func (s *Server) jmapSession(w http.ResponseWriter, r *http.Request) {
	_, token, err := s.session(r)
	if err != nil {
		problem(w, 401, "authentication_required", "Sign in to access this mailbox.")
		return
	}
	status, headers, response, err := s.Stalwart.JMAPSession(r.Context(), token)
	if err != nil {
		problem(w, 502, "mail_service_error", err.Error())
		return
	}
	if content := headers.Get("Content-Type"); content != "" {
		w.Header().Set("Content-Type", content)
	}
	w.WriteHeader(status)
	_, _ = w.Write(response)
}
func (s *Server) jmapEvents(w http.ResponseWriter, r *http.Request) {
	_, token, err := s.session(r)
	if err != nil {
		problem(w, 401, "authentication_required", "Sign in to access this mailbox.")
		return
	}
	response, err := s.Stalwart.EventStream(r.Context(), token)
	if err != nil {
		problem(w, 502, "mail_service_error", err.Error())
		return
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		problem(w, 502, "mail_service_error", strings.TrimSpace(string(body)))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	buffer := make([]byte, 16<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, err = w.Write(buffer[:n]); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}
func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	stats, err := s.Store.Stats(r.Context())
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	stats["account"] = session.Account
	stats["version"] = "0.1.0"
	stats["stalwart_version"] = "0.16.17"
	writeJSON(w, 200, stats)
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (store.Session, string, bool) {
	session, token, err := s.session(r)
	if err != nil {
		if os.Getenv("MEOVV_DEMO_MODE") == "1" {
			return store.Session{Account: "demo-admin"}, "", true
		}
		problem(w, 401, "authentication_required", "Administrator sign-in is required.")
		return store.Session{}, "", false
	}
	var installation setupRequest
	if err = s.Store.GetSetting(r.Context(), "installation", &installation); err == nil && !strings.EqualFold(session.Account, installation.AdminEmail) && !strings.EqualFold(session.Account, "admin") {
		problem(w, 403, "administrator_required", "This account is not the installation administrator.")
		return store.Session{}, "", false
	}
	return session, token, true
}

func (s *Server) adminStalwart(w http.ResponseWriter, r *http.Request) {
	_, token, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		problem(w, 400, "invalid_body", err.Error())
		return
	}
	status, headers, response, err := s.Stalwart.ProxyManagement(r.Context(), token, raw)
	if err != nil {
		problem(w, 502, "mail_service_error", err.Error())
		return
	}
	if value := headers.Get("Content-Type"); value != "" {
		w.Header().Set("Content-Type", value)
	}
	w.WriteHeader(status)
	_, _ = w.Write(response)
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	keys, err := s.Store.ListAPIKeys(r.Context())
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": keys})
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var in struct {
		Name           string   `json:"name"`
		Scopes         []string `json:"scopes"`
		AllowedSenders []string `json:"allowed_senders"`
		RateLimit      int      `json:"rate_limit"`
	}
	if !decodeJSON(w, r, &in, 64<<10) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || !validScopes(in.Scopes) || len(in.AllowedSenders) == 0 {
		problem(w, 422, "invalid_api_key", "Name, valid scopes, and at least one approved sender are required.")
		return
	}
	for _, sender := range in.AllowedSenders {
		if !validSenderPattern(sender) {
			problem(w, 422, "invalid_sender", "Approved senders must be complete addresses or domain wildcards such as *@example.com.")
			return
		}
	}
	if in.RateLimit <= 0 {
		in.RateLimit = s.Config.RateLimitPerMinute
	}
	secret := app.RandomID("meovv_")
	key := store.APIKey{ID: app.RandomID("key_"), Name: strings.TrimSpace(in.Name), Prefix: secret[:min(14, len(secret))], Scopes: in.Scopes, AllowedSenders: in.AllowedSenders, RateLimit: in.RateLimit, CreatedAt: time.Now().UTC()}
	if err := s.Store.CreateAPIKey(r.Context(), key, secret); err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	_ = s.Store.Audit(r.Context(), app.RandomID("aud_"), session.Account, "api_key.created", key.ID, map[string]any{"name": key.Name, "scopes": key.Scopes})
	writeJSON(w, 201, map[string]any{"api_key": key, "secret": secret, "warning": "This secret is shown once and cannot be recovered."})
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := s.Store.RevokeAPIKey(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			problem(w, 404, "not_found", "API key not found.")
		} else {
			problem(w, 500, "storage_error", err.Error())
		}
		return
	}
	_ = s.Store.Audit(r.Context(), app.RandomID("aud_"), session.Account, "api_key.revoked", r.PathValue("id"), nil)
	w.WriteHeader(204)
}

var allowedWebhookEvents = map[string]bool{"message.queued": true, "message.delivered": true, "message.deferred": true, "message.bounced": true, "message.failed": true}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	items, err := s.Store.ListWebhookEndpoints(r.Context())
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": items})
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var in struct {
		Name   string   `json:"name"`
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if !decodeJSON(w, r, &in, 64<<10) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Events) == 0 {
		problem(w, 422, "invalid_webhook", "Name and at least one event are required.")
		return
	}
	for _, event := range in.Events {
		if !allowedWebhookEvents[event] {
			problem(w, 422, "invalid_event", "The webhook contains an unsupported event type.")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := webhook.ValidatePublicURL(ctx, in.URL); err != nil {
		problem(w, 422, "unsafe_webhook_url", err.Error())
		return
	}
	rawSecret := app.RandomID("whsec_")
	encryptedSecret, err := s.Cipher.Encrypt(rawSecret)
	if err != nil {
		problem(w, 500, "secret_encryption_failed", "Could not protect the webhook signing secret.")
		return
	}
	endpoint := store.WebhookEndpoint{ID: app.RandomID("wh_"), Name: strings.TrimSpace(in.Name), URL: in.URL, Secret: base64.RawStdEncoding.EncodeToString(encryptedSecret), Events: in.Events, Enabled: true, CreatedAt: time.Now().UTC()}
	if err := s.Store.CreateWebhookEndpoint(r.Context(), endpoint); err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	_ = s.Store.Audit(r.Context(), app.RandomID("aud_"), session.Account, "webhook.created", endpoint.ID, map[string]any{"url": endpoint.URL, "events": endpoint.Events})
	writeJSON(w, 201, map[string]any{"webhook": endpoint, "secret": rawSecret, "warning": "This signing secret is shown once."})
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteWebhookEndpoint(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			problem(w, 404, "not_found", "Webhook endpoint not found.")
		} else {
			problem(w, 500, "storage_error", err.Error())
		}
		return
	}
	_ = s.Store.Audit(r.Context(), app.RandomID("aud_"), session.Account, "webhook.deleted", r.PathValue("id"), nil)
	w.WriteHeader(204)
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request, key store.APIKey) {
	var in struct {
		From        string            `json:"from"`
		To          []string          `json:"to"`
		CC          []string          `json:"cc"`
		BCC         []string          `json:"bcc"`
		ReplyTo     string            `json:"reply_to"`
		Subject     string            `json:"subject"`
		Text        string            `json:"text"`
		HTML        string            `json:"html"`
		Headers     map[string]string `json:"headers"`
		Attachments []struct {
			Filename    string `json:"filename"`
			ContentType string `json:"content_type"`
			Content     string `json:"content_base64"`
		} `json:"attachments"`
	}
	if !decodeJSON(w, r, &in, s.Config.MaxMessageBytes*2) {
		return
	}
	if in.Text == "" && in.HTML == "" {
		problem(w, 422, "body_required", "A text or HTML body is required.")
		return
	}
	if !allowedSender(key, in.From) {
		problem(w, 403, "sender_not_allowed", "The API key is not allowed to use this sender.")
		return
	}
	if len(in.To)+len(in.CC)+len(in.BCC) > s.Config.MaxRecipients {
		problem(w, 422, "too_many_recipients", fmt.Sprintf("At most %d recipients are allowed.", s.Config.MaxRecipients))
		return
	}
	id := app.RandomID("msg_")
	headers := in.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	headers["X-MEOVV-Message-ID"] = id
	attachments := make([]mailer.Attachment, len(in.Attachments))
	for i, a := range in.Attachments {
		attachments[i] = mailer.Attachment{Filename: a.Filename, ContentType: a.ContentType, ContentBase64: a.Content}
	}
	raw, recipients, err := mailer.Build(mailer.Message{From: in.From, To: in.To, CC: in.CC, BCC: in.BCC, ReplyTo: in.ReplyTo, Subject: in.Subject, Text: in.Text, HTML: in.HTML, Headers: headers, Attachments: attachments}, s.Config.MaxMessageBytes)
	if err != nil {
		problem(w, 422, "invalid_message", err.Error())
		return
	}
	now := time.Now().UTC()
	message := store.Message{ID: id, APIKeyID: key.ID, Sender: strings.ToLower(in.From), Subject: in.Subject, Status: "queued", SizeBytes: int64(len(raw)), SubmittedAt: now, UpdatedAt: now}
	for _, recipient := range recipients {
		message.Recipients = append(message.Recipients, store.Recipient{Address: recipient, Status: "queued"})
	}
	digest := sha256.Sum256(raw)
	existing, err := s.Store.InsertMessage(r.Context(), message, r.Header.Get("Idempotency-Key"), hex.EncodeToString(digest[:]))
	if err != nil {
		problem(w, 409, "idempotency_conflict", err.Error())
		return
	}
	if existing != "" {
		prior, err := s.Store.GetMessage(r.Context(), existing)
		if err != nil {
			problem(w, 500, "storage_error", err.Error())
			return
		}
		writeJSON(w, 202, receipt(prior))
		return
	}
	smtpID, err := s.Mailer.Send(r.Context(), in.From, recipients, raw)
	if err != nil {
		s.failed.Add(1)
		_ = s.Store.UpdateMessageStatus(r.Context(), id, "failed", err.Error(), "")
		problem(w, 502, "submission_failed", "The mail service did not accept this message.")
		return
	}
	s.accepted.Add(1)
	_ = s.Store.UpdateMessageStatus(r.Context(), id, "processing", "", smtpID)
	eventPayload, _ := json.Marshal(map[string]any{"id": app.RandomID("evt_"), "type": "message.queued", "created_at": now, "data": map[string]any{"message_id": id, "status": "queued"}})
	_ = s.Store.EnqueueEvent(r.Context(), app.RandomID("evt_"), "message.queued", eventPayload, now)
	w.Header().Set("Location", "/api/v1/messages/"+id)
	writeJSON(w, 202, map[string]any{"id": id, "status": "queued", "submitted_at": now})
}
func receipt(m store.Message) map[string]any {
	return map[string]any{"id": m.ID, "status": m.Status, "submitted_at": m.SubmittedAt}
}
func (s *Server) getMessage(w http.ResponseWriter, r *http.Request, key store.APIKey) {
	m, err := s.Store.GetMessage(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		problem(w, 404, "not_found", "Message not found.")
		return
	}
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	if m.APIKeyID != key.ID {
		problem(w, 404, "not_found", "Message not found.")
		return
	}
	writeJSON(w, 200, m)
}
func (s *Server) listMessages(w http.ResponseWriter, r *http.Request, key store.APIKey) {
	q := r.URL.Query()
	filter := store.MessageFilter{Status: q.Get("status"), Sender: q.Get("sender"), Recipient: q.Get("recipient"), Cursor: q.Get("cursor"), APIKeyID: key.ID}
	filter.Limit, _ = strconv.Atoi(q.Get("limit"))
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.From = &t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.To = &t
		}
	}
	items, next, err := s.Store.ListMessages(r.Context(), filter)
	if err != nil {
		problem(w, 500, "storage_error", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": items, "next_cursor": next})
}

func (s *Server) internalEvents(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		problem(w, 400, "invalid_body", err.Error())
		return
	}
	if s.Config.InternalWebhookSecret == "" || !verifyInternal(s.Config.InternalWebhookSecret, r.Header.Get("X-Signature"), raw) {
		problem(w, 401, "invalid_signature", "Invalid Stalwart webhook signature.")
		return
	}
	var envelope struct {
		Events []struct {
			ID, Type, CreatedAt string
			Data                map[string]any
		} `json:"events"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		problem(w, 400, "invalid_event", err.Error())
		return
	}
	for _, event := range envelope.Events {
		messageID := findString(event.Data, "message_id", "messageId", "queue_id", "queueId")
		recipient := findString(event.Data, "recipient", "to")
		status, external := normalizeEvent(event.Type)
		if messageID != "" && status != "" {
			if recipient != "" {
				_ = s.Store.UpdateRecipient(r.Context(), messageID, recipient, status, findString(event.Data, "response", "reason"), time.Now())
			} else {
				_ = s.Store.UpdateMessageStatus(r.Context(), messageID, status, findString(event.Data, "reason"), "")
			}
		}
		if external != "" {
			payload, _ := json.Marshal(map[string]any{"id": event.ID, "type": external, "created_at": event.CreatedAt, "data": map[string]any{"message_id": messageID, "recipient": recipient, "status": status}})
			_ = s.Store.EnqueueEvent(r.Context(), event.ID, external, payload, time.Now())
		}
	}
	w.WriteHeader(204)
}

type apiHandler func(http.ResponseWriter, *http.Request, store.APIKey)

func (s *Server) authAPI(scope string, next apiHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if value == "" {
			problem(w, 401, "authentication_required", "Provide a bearer API key.")
			return
		}
		key, err := s.Store.AuthenticateAPIKey(r.Context(), value)
		if err != nil {
			problem(w, 401, "invalid_api_key", "The API key is invalid or revoked.")
			return
		}
		if !hasScope(key, scope) {
			problem(w, 403, "insufficient_scope", "The API key does not have the required scope.")
			return
		}
		limit := key.RateLimit
		if limit <= 0 {
			limit = s.Config.RateLimitPerMinute
		}
		if !s.limiter.Allow(key.ID, limit) {
			w.Header().Set("Retry-After", "60")
			problem(w, 429, "rate_limited", "API key rate limit exceeded.")
			return
		}
		next(w, r, key)
	}
}

func (s *Server) frontend() http.Handler {
	files := http.FileServer(http.Dir(s.StaticDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(s.StaticDir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(s.StaticDir, "index.html"))
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		problem(w, 400, "invalid_json", err.Error())
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	writeJSON(w, status, map[string]any{"type": "https://docs.meovv.mail/problems/" + code, "title": strings.ReplaceAll(code, "_", " "), "status": status, "detail": detail, "code": code})
}
func validHostname(v string) bool { return validDomain(v) && strings.Contains(v, ".") }
func validDomain(v string) bool {
	if len(v) < 3 || len(v) > 253 || strings.ContainsAny(v, " /:@") {
		return false
	}
	for _, part := range strings.Split(v, ".") {
		if part == "" || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return false
		}
	}
	return true
}
func hasScope(k store.APIKey, scope string) bool {
	for _, s := range k.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}
func allowedSender(k store.APIKey, value string) bool {
	address, err := mail.ParseAddress(value)
	if err != nil {
		return false
	}
	for _, allowed := range k.AllowedSenders {
		if strings.EqualFold(allowed, address.Address) {
			return true
		}
		if strings.HasPrefix(allowed, "*@") && strings.HasSuffix(strings.ToLower(address.Address), strings.ToLower(allowed[1:])) {
			return true
		}
	}
	return false
}
func validSenderPattern(value string) bool {
	if strings.HasPrefix(value, "*@") {
		return validDomain(strings.TrimPrefix(value, "*@"))
	}
	_, err := mail.ParseAddress(value)
	return err == nil
}
func validScopes(scopes []string) bool {
	if len(scopes) == 0 {
		return false
	}
	for _, scope := range scopes {
		if scope != "messages.send" && scope != "messages.status" {
			return false
		}
	}
	return true
}
func verifyInternal(secret, signature string, body []byte) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
func normalizeEvent(event string) (string, string) {
	switch event {
	case "queue.queue-message", "queue.queue-message-authenticated":
		return "queued", "message.queued"
	case "delivery.delivered":
		return "delivered", "message.delivered"
	case "delivery.dsn-temp-fail", "queue.rescheduled":
		return "deferred", "message.deferred"
	case "delivery.dsn-perm-fail":
		return "bounced", "message.bounced"
	case "delivery.double-bounce":
		return "failed", "message.failed"
	}
	return "", ""
}
func findString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := data[key].(string); ok {
			return v
		}
	}
	return ""
}

type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
}
type rateEntry struct {
	minute int64
	count  int
}

func newRateLimiter() *rateLimiter { return &rateLimiter{entries: map[string]rateEntry{}} }
func (l *rateLimiter) Allow(id string, limit int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	minute := time.Now().Unix() / 60
	entry := l.entries[id]
	if entry.minute != minute {
		entry = rateEntry{minute: minute}
	}
	if entry.count >= limit {
		return false
	}
	entry.count++
	l.entries[id] = entry
	return true
}
