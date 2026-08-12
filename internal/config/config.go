package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr                  string
	DataDir               string
	PublicURL             string
	StalwartURL           string
	StalwartBootstrapURL  string
	StalwartTLSInsecure   bool
	StalwartRecoveryAdmin string
	SMTPAddress           string
	SMTPTLSServerName     string
	SMTPUsername          string
	SMTPPassword          string
	SessionKey            []byte
	InternalWebhookSecret string
	BootstrapToken        string
	MessageRetention      time.Duration
	WebhookRetention      time.Duration
	AuditRetention        time.Duration
	MaxMessageBytes       int64
	MaxRecipients         int
	RateLimitPerMinute    int
}

func Load() (Config, error) {
	dataDir := value("MEOVV_DATA_DIR", "/var/lib/meovv-mail")
	cfg := Config{
		Addr:                  value("MEOVV_ADDR", ":8080"),
		DataDir:               dataDir,
		PublicURL:             strings.TrimRight(value("MEOVV_PUBLIC_URL", "http://localhost:8080"), "/"),
		StalwartURL:           strings.TrimRight(value("STALWART_URL", "http://stalwart:8080"), "/"),
		StalwartBootstrapURL:  strings.TrimRight(value("STALWART_BOOTSTRAP_URL", "http://stalwart:8080"), "/"),
		StalwartTLSInsecure:   strings.EqualFold(value("STALWART_TLS_INSECURE", "false"), "true"),
		StalwartRecoveryAdmin: secret("STALWART_RECOVERY_ADMIN", "STALWART_RECOVERY_ADMIN_FILE"),
		SMTPAddress:           value("STALWART_SMTP_ADDRESS", "stalwart:587"),
		SMTPTLSServerName:     value("STALWART_TLS_SERVER_NAME", ""),
		SMTPUsername:          os.Getenv("STALWART_SMTP_USERNAME"),
		SMTPPassword:          secret("STALWART_SMTP_PASSWORD", "STALWART_SMTP_PASSWORD_FILE"),
		InternalWebhookSecret: secret("MEOVV_INTERNAL_WEBHOOK_SECRET", "MEOVV_INTERNAL_WEBHOOK_SECRET_FILE"),
		BootstrapToken:        secret("MEOVV_BOOTSTRAP_TOKEN", "MEOVV_BOOTSTRAP_TOKEN_FILE"),
		MessageRetention:      duration("MEOVV_MESSAGE_RETENTION", 30*24*time.Hour),
		WebhookRetention:      duration("MEOVV_WEBHOOK_RETENTION", 7*24*time.Hour),
		AuditRetention:        duration("MEOVV_AUDIT_RETENTION", 180*24*time.Hour),
		MaxMessageBytes:       int64Value("MEOVV_MAX_MESSAGE_BYTES", 25*1024*1024),
		MaxRecipients:         intValue("MEOVV_MAX_RECIPIENTS", 100),
		RateLimitPerMinute:    intValue("MEOVV_RATE_LIMIT_PER_MINUTE", 60),
	}

	sessionSecret := secret("MEOVV_SESSION_KEY", "MEOVV_SESSION_KEY_FILE")
	if sessionSecret == "" {
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return Config{}, fmt.Errorf("create data directory: %w", err)
		}
		path := filepath.Join(dataDir, "session.key")
		contents, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			key := make([]byte, 32)
			if _, err = rand.Read(key); err != nil {
				return Config{}, fmt.Errorf("generate session key: %w", err)
			}
			sessionSecret = base64.RawURLEncoding.EncodeToString(key)
			if err = os.WriteFile(path, []byte(sessionSecret+"\n"), 0o600); err != nil {
				return Config{}, fmt.Errorf("write session key: %w", err)
			}
		} else if err != nil {
			return Config{}, fmt.Errorf("read session key: %w", err)
		} else {
			sessionSecret = strings.TrimSpace(string(contents))
		}
	}
	key, err := base64.RawURLEncoding.DecodeString(sessionSecret)
	if err != nil || len(key) != 32 {
		return Config{}, errors.New("MEOVV_SESSION_KEY must be a base64url encoded 32-byte key")
	}
	cfg.SessionKey = key
	return cfg, nil
}

func value(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func secret(valueName, fileName string) string {
	if v := strings.TrimSpace(os.Getenv(valueName)); v != "" {
		return v
	}
	if path := strings.TrimSpace(os.Getenv(fileName)); path != "" {
		if contents, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(contents))
		}
	}
	return ""
}

func duration(name string, fallback time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			return parsed
		}
	}
	return fallback
}

func intValue(name string, fallback int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && v > 0 {
		return v
	}
	return fallback
}

func int64Value(name string, fallback int64) int64 {
	if v, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64); err == nil && v > 0 {
		return v
	}
	return fallback
}
