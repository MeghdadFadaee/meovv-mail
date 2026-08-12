package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

type APIKey struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Prefix         string     `json:"prefix"`
	Scopes         []string   `json:"scopes"`
	AllowedSenders []string   `json:"allowed_senders"`
	RateLimit      int        `json:"rate_limit"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
}

type Recipient struct {
	Address       string     `json:"address"`
	Status        string     `json:"status"`
	LastResponse  string     `json:"last_response,omitempty"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
}

type Message struct {
	ID            string      `json:"id"`
	APIKeyID      string      `json:"api_key_id,omitempty"`
	Sender        string      `json:"sender"`
	Subject       string      `json:"subject"`
	Status        string      `json:"status"`
	SizeBytes     int64       `json:"size_bytes"`
	SubmittedAt   time.Time   `json:"submitted_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
	StalwartID    string      `json:"stalwart_id,omitempty"`
	FailureReason string      `json:"failure_reason,omitempty"`
	Recipients    []Recipient `json:"recipients"`
}

type MessageFilter struct {
	Status, Sender, Recipient, Cursor, APIKeyID string
	From, To                                    *time.Time
	Limit                                       int
}

type WebhookEndpoint struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Secret    string    `json:"-"`
	Events    []string  `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type WebhookDelivery struct {
	ID, EndpointID, EventID, EventType string
	Payload                            []byte
	Attempt                            int
	NextAttemptAt, CreatedAt           time.Time
}

type Session struct {
	ID, Account                                        string
	AccessToken, RefreshToken                          []byte
	ExpiresAt, SessionExpiresAt, CreatedAt, LastSeenAt time.Time
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err = s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS api_keys (id TEXT PRIMARY KEY, name TEXT NOT NULL, prefix TEXT NOT NULL, digest TEXT NOT NULL UNIQUE, scopes TEXT NOT NULL, allowed_senders TEXT NOT NULL, rate_limit INTEGER NOT NULL, created_at TEXT NOT NULL, last_used_at TEXT, revoked_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS messages (id TEXT PRIMARY KEY, api_key_id TEXT, sender TEXT NOT NULL, subject TEXT NOT NULL, status TEXT NOT NULL, size_bytes INTEGER NOT NULL, submitted_at TEXT NOT NULL, updated_at TEXT NOT NULL, stalwart_id TEXT NOT NULL DEFAULT '', failure_reason TEXT NOT NULL DEFAULT '', FOREIGN KEY(api_key_id) REFERENCES api_keys(id))`,
		`CREATE TABLE IF NOT EXISTS message_recipients (message_id TEXT NOT NULL, address TEXT NOT NULL, status TEXT NOT NULL, last_response TEXT NOT NULL DEFAULT '', last_attempt_at TEXT, PRIMARY KEY(message_id,address), FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS idempotency_keys (api_key_id TEXT NOT NULL, token TEXT NOT NULL, message_id TEXT NOT NULL, request_digest TEXT NOT NULL, expires_at TEXT NOT NULL, PRIMARY KEY(api_key_id,token), FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS webhook_endpoints (id TEXT PRIMARY KEY, name TEXT NOT NULL, url TEXT NOT NULL, secret TEXT NOT NULL, events TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS webhook_deliveries (id TEXT PRIMARY KEY, endpoint_id TEXT NOT NULL, event_id TEXT NOT NULL, event_type TEXT NOT NULL, payload BLOB NOT NULL, attempt INTEGER NOT NULL DEFAULT 0, next_attempt_at TEXT NOT NULL, delivered_at TEXT, last_error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, FOREIGN KEY(endpoint_id) REFERENCES webhook_endpoints(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS audit_events (id TEXT PRIMARY KEY, actor TEXT NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, detail TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, account TEXT NOT NULL, access_token BLOB NOT NULL, refresh_token BLOB NOT NULL, expires_at TEXT NOT NULL, session_expires_at TEXT NOT NULL, created_at TEXT NOT NULL, last_seen_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_submitted ON messages(submitted_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_status_submitted ON messages(status, submitted_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_recipients_address ON message_recipients(address, message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_due ON webhook_deliveries(next_attempt_at) WHERE delivered_at IS NULL`,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range statements {
		if _, err = tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration: %w", err)
		}
	}
	if err = ensureSessionExpiryColumn(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `PRAGMA optimize`); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureSessionExpiryColumn(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(sessions)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "session_expires_at" {
			found = true
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `ALTER TABLE sessions ADD COLUMN session_expires_at TEXT`); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sessions SET session_expires_at=expires_at WHERE session_expires_at IS NULL`)
	return err
}

func DigestSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CreateAPIKey(ctx context.Context, key APIKey, secret string) error {
	scopes, _ := json.Marshal(key.Scopes)
	senders, _ := json.Marshal(key.AllowedSenders)
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_keys(id,name,prefix,digest,scopes,allowed_senders,rate_limit,created_at) VALUES(?,?,?,?,?,?,?,?)`, key.ID, key.Name, key.Prefix, DigestSecret(secret), string(scopes), string(senders), key.RateLimit, key.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) AuthenticateAPIKey(ctx context.Context, secret string) (APIKey, error) {
	var k APIKey
	var scopes, senders, created string
	var last sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,name,prefix,scopes,allowed_senders,rate_limit,created_at,last_used_at FROM api_keys WHERE digest=? AND revoked_at IS NULL`, DigestSecret(secret)).Scan(&k.ID, &k.Name, &k.Prefix, &scopes, &senders, &k.RateLimit, &created, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return k, ErrNotFound
	}
	if err != nil {
		return k, err
	}
	_ = json.Unmarshal([]byte(scopes), &k.Scopes)
	_ = json.Unmarshal([]byte(senders), &k.AllowedSenders)
	k.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if last.Valid {
		t, _ := time.Parse(time.RFC3339Nano, last.String)
		k.LastUsedAt = &t
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), k.ID)
	return k, nil
}

func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,prefix,scopes,allowed_senders,rate_limit,created_at,last_used_at FROM api_keys WHERE revoked_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		var key APIKey
		var scopes, senders, created string
		var last sql.NullString
		if err = rows.Scan(&key.ID, &key.Name, &key.Prefix, &scopes, &senders, &key.RateLimit, &created, &last); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopes), &key.Scopes)
		_ = json.Unmarshal([]byte(senders), &key.AllowedSenders)
		key.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if last.Valid {
			t, _ := time.Parse(time.RFC3339Nano, last.String)
			key.LastUsedAt = &t
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func (s *Store) RevokeAPIKey(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE api_keys SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) InsertMessage(ctx context.Context, m Message, idemToken, requestDigest string) (existing string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if idemToken != "" {
		var priorID, priorDigest string
		err = tx.QueryRowContext(ctx, `SELECT message_id,request_digest FROM idempotency_keys WHERE api_key_id=? AND token=? AND expires_at>?`, m.APIKeyID, idemToken, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&priorID, &priorDigest)
		if err == nil {
			if priorDigest != requestDigest {
				return "", fmt.Errorf("idempotency key reused with a different request")
			}
			return priorID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO messages(id,api_key_id,sender,subject,status,size_bytes,submitted_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, m.ID, m.APIKeyID, m.Sender, m.Subject, m.Status, m.SizeBytes, m.SubmittedAt.UTC().Format(time.RFC3339Nano), m.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", err
	}
	for _, r := range m.Recipients {
		if _, err = tx.ExecContext(ctx, `INSERT INTO message_recipients(message_id,address,status) VALUES(?,?,?)`, m.ID, strings.ToLower(r.Address), r.Status); err != nil {
			return "", err
		}
	}
	if idemToken != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO idempotency_keys(api_key_id,token,message_id,request_digest,expires_at) VALUES(?,?,?,?,?)`, m.APIKeyID, idemToken, m.ID, requestDigest, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339Nano))
		if err != nil {
			return "", err
		}
	}
	return "", tx.Commit()
}

func (s *Store) UpdateMessageStatus(ctx context.Context, id, status, reason, stalwartID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET status=?,failure_reason=?,stalwart_id=CASE WHEN ?='' THEN stalwart_id ELSE ? END,updated_at=? WHERE id=?`, status, reason, stalwartID, stalwartID, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) UpdateRecipient(ctx context.Context, id, address, status, response string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE message_recipients SET status=?,last_response=?,last_attempt_at=? WHERE message_id=? AND address=?`, status, response, at.UTC().Format(time.RFC3339Nano), id, strings.ToLower(address))
	if err != nil {
		return err
	}
	return s.recalculate(ctx, id)
}

func (s *Store) recalculate(ctx context.Context, id string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT status,COUNT(*) FROM message_recipients WHERE message_id=? GROUP BY status`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	counts := map[string]int{}
	total := 0
	for rows.Next() {
		var status string
		var n int
		if err = rows.Scan(&status, &n); err != nil {
			return err
		}
		counts[status] = n
		total += n
	}
	status := "processing"
	switch {
	case total > 0 && counts["delivered"] == total:
		status = "delivered"
	case counts["delivered"] > 0 && (counts["bounced"]+counts["failed"] > 0):
		status = "partial"
	case total > 0 && counts["bounced"] == total:
		status = "bounced"
	case total > 0 && counts["failed"] == total:
		status = "failed"
	case counts["deferred"] > 0:
		status = "deferred"
	case counts["queued"] == total:
		status = "queued"
	}
	return s.UpdateMessageStatus(ctx, id, status, "", "")
}

func (s *Store) GetMessage(ctx context.Context, id string) (Message, error) {
	var m Message
	var submitted, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,COALESCE(api_key_id,''),sender,subject,status,size_bytes,submitted_at,updated_at,stalwart_id,failure_reason FROM messages WHERE id=?`, id).Scan(&m.ID, &m.APIKeyID, &m.Sender, &m.Subject, &m.Status, &m.SizeBytes, &submitted, &updated, &m.StalwartID, &m.FailureReason)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	m.SubmittedAt, _ = time.Parse(time.RFC3339Nano, submitted)
	m.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	rows, err := s.db.QueryContext(ctx, `SELECT address,status,last_response,last_attempt_at FROM message_recipients WHERE message_id=? ORDER BY address`, id)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Recipient
		var at sql.NullString
		if err = rows.Scan(&r.Address, &r.Status, &r.LastResponse, &at); err != nil {
			return m, err
		}
		if at.Valid {
			t, _ := time.Parse(time.RFC3339Nano, at.String)
			r.LastAttemptAt = &t
		}
		m.Recipients = append(m.Recipients, r)
	}
	return m, rows.Err()
}

func (s *Store) ListMessages(ctx context.Context, f MessageFilter) ([]Message, string, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 25
	}
	args := []any{}
	where := []string{"1=1"}
	if f.APIKeyID != "" {
		where = append(where, "m.api_key_id=?")
		args = append(args, f.APIKeyID)
	}
	if f.Status != "" {
		where = append(where, "m.status=?")
		args = append(args, f.Status)
	}
	if f.Sender != "" {
		where = append(where, "m.sender=?")
		args = append(args, strings.ToLower(f.Sender))
	}
	if f.Recipient != "" {
		where = append(where, "EXISTS(SELECT 1 FROM message_recipients mr WHERE mr.message_id=m.id AND mr.address=?)")
		args = append(args, strings.ToLower(f.Recipient))
	}
	if f.From != nil {
		where = append(where, "m.submitted_at>=?")
		args = append(args, f.From.UTC().Format(time.RFC3339Nano))
	}
	if f.To != nil {
		where = append(where, "m.submitted_at<=?")
		args = append(args, f.To.UTC().Format(time.RFC3339Nano))
	}
	if f.Cursor != "" {
		where = append(where, "m.submitted_at<?")
		args = append(args, f.Cursor)
	}
	args = append(args, f.Limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT m.id FROM messages m WHERE `+strings.Join(where, " AND ")+` ORDER BY m.submitted_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, "", err
		}
		ids = append(ids, id)
	}
	next := ""
	if len(ids) > f.Limit {
		last, err := s.GetMessage(ctx, ids[f.Limit-1])
		if err == nil {
			next = last.SubmittedAt.UTC().Format(time.RFC3339Nano)
		}
		ids = ids[:f.Limit]
	}
	out := make([]Message, 0, len(ids))
	for _, id := range ids {
		m, err := s.GetMessage(ctx, id)
		if err != nil {
			return nil, "", err
		}
		out = append(out, m)
	}
	return out, next, nil
}

func (s *Store) SetSetting(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) GetSetting(ctx context.Context, key string, dst any) error {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), dst)
}

func (s *Store) EnqueueEvent(ctx context.Context, eventID, eventType string, payload []byte, now time.Time) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM webhook_endpoints WHERE enabled=1 AND EXISTS(SELECT 1 FROM json_each(events) WHERE value=?)`, eventType)
	if err != nil {
		return err
	}
	var endpointIDs []string
	for rows.Next() {
		var endpointID string
		if err = rows.Scan(&endpointID); err != nil {
			rows.Close()
			return err
		}
		endpointIDs = append(endpointIDs, endpointID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, endpointID := range endpointIDs {
		id := eventID + "_" + endpointID
		if _, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO webhook_deliveries(id,endpoint_id,event_id,event_type,payload,next_attempt_at,created_at) VALUES(?,?,?,?,?,?,?)`, id, endpointID, eventID, eventType, payload, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) DueWebhooks(ctx context.Context, now time.Time, limit int) ([]WebhookDelivery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,endpoint_id,event_id,event_type,payload,attempt,next_attempt_at,created_at FROM webhook_deliveries WHERE delivered_at IS NULL AND next_attempt_at<=? ORDER BY next_attempt_at LIMIT ?`, now.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebhookDelivery
	for rows.Next() {
		var d WebhookDelivery
		var next, created string
		if err = rows.Scan(&d.ID, &d.EndpointID, &d.EventID, &d.EventType, &d.Payload, &d.Attempt, &next, &created); err != nil {
			return nil, err
		}
		d.NextAttemptAt, _ = time.Parse(time.RFC3339Nano, next)
		d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) GetWebhookEndpoint(ctx context.Context, id string) (WebhookEndpoint, error) {
	var e WebhookEndpoint
	var events, created string
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT id,name,url,secret,events,enabled,created_at FROM webhook_endpoints WHERE id=?`, id).Scan(&e.ID, &e.Name, &e.URL, &e.Secret, &events, &enabled, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	_ = json.Unmarshal([]byte(events), &e.Events)
	e.Enabled = enabled == 1
	e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return e, err
}

func (s *Store) CreateWebhookEndpoint(ctx context.Context, endpoint WebhookEndpoint) error {
	events, _ := json.Marshal(endpoint.Events)
	_, err := s.db.ExecContext(ctx, `INSERT INTO webhook_endpoints(id,name,url,secret,events,enabled,created_at) VALUES(?,?,?,?,?,?,?)`, endpoint.ID, endpoint.Name, endpoint.URL, endpoint.Secret, string(events), boolInt(endpoint.Enabled), endpoint.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListWebhookEndpoints(ctx context.Context) ([]WebhookEndpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,url,events,enabled,created_at FROM webhook_endpoints ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebhookEndpoint
	for rows.Next() {
		var endpoint WebhookEndpoint
		var events, created string
		var enabled int
		if err = rows.Scan(&endpoint.ID, &endpoint.Name, &endpoint.URL, &events, &enabled, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(events), &endpoint.Events)
		endpoint.Enabled = enabled == 1
		endpoint.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, endpoint)
	}
	return out, rows.Err()
}

func (s *Store) DeleteWebhookEndpoint(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM webhook_endpoints WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) CompleteWebhook(ctx context.Context, id string, delivered bool, next time.Time, lastErr string) error {
	if delivered {
		_, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET attempt=attempt+1,delivered_at=?,last_error='' WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET attempt=attempt+1,next_attempt_at=?,last_error=? WHERE id=?`, next.UTC().Format(time.RFC3339Nano), lastErr, id)
	return err
}

func (s *Store) Stats(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	for key, query := range map[string]string{"messages_24h": `SELECT COUNT(*) FROM messages WHERE submitted_at>=datetime('now','-1 day')`, "queued": `SELECT COUNT(*) FROM messages WHERE status IN ('queued','processing','deferred')`, "failed_webhooks": `SELECT COUNT(*) FROM webhook_deliveries WHERE delivered_at IS NULL AND attempt>0`, "api_keys": `SELECT COUNT(*) FROM api_keys WHERE revoked_at IS NULL`} {
		var n int
		if err := s.db.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, nil
}

func (s *Store) Cleanup(ctx context.Context, messageBefore, webhookBefore, auditBefore time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []struct {
		sql string
		arg time.Time
	}{{`DELETE FROM messages WHERE submitted_at<?`, messageBefore}, {`DELETE FROM webhook_deliveries WHERE created_at<?`, webhookBefore}, {`DELETE FROM audit_events WHERE created_at<?`, auditBefore}, {`DELETE FROM idempotency_keys WHERE expires_at<?`, time.Now()}, {`DELETE FROM sessions WHERE session_expires_at<?`, time.Now()}} {
		if _, err = tx.ExecContext(ctx, q.sql, q.arg.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CreateSession(ctx context.Context, v Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id,account,access_token,refresh_token,expires_at,session_expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?,?,?,?)`, v.ID, v.Account, v.AccessToken, v.RefreshToken, v.ExpiresAt.UTC().Format(time.RFC3339Nano), v.SessionExpiresAt.UTC().Format(time.RFC3339Nano), v.CreatedAt.UTC().Format(time.RFC3339Nano), v.LastSeenAt.UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	var v Session
	var expires, sessionExpires, created, seen string
	err := s.db.QueryRowContext(ctx, `SELECT id,account,access_token,refresh_token,expires_at,session_expires_at,created_at,last_seen_at FROM sessions WHERE id=? AND session_expires_at>?`, id, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&v.ID, &v.Account, &v.AccessToken, &v.RefreshToken, &expires, &sessionExpires, &created, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
	v.SessionExpiresAt, _ = time.Parse(time.RFC3339Nano, sessionExpires)
	v.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	v.LastSeenAt, _ = time.Parse(time.RFC3339Nano, seen)
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), id)
	return v, nil
}
func (s *Store) UpdateSessionTokens(ctx context.Context, id string, access, refresh []byte, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET access_token=?,refresh_token=?,expires_at=?,last_seen_at=? WHERE id=?`, access, refresh, expiresAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	return err
}

func (s *Store) Audit(ctx context.Context, id, actor, action, target string, detail any) error {
	raw, _ := json.Marshal(detail)
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events(id,actor,action,target,detail,created_at) VALUES(?,?,?,?,?,?)`, id, actor, action, target, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
