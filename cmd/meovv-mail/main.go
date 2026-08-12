package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/meovv-mail/meovv-mail/internal/app"
	"github.com/meovv-mail/meovv-mail/internal/config"
	"github.com/meovv-mail/meovv-mail/internal/httpapi"
	"github.com/meovv-mail/meovv-mail/internal/mailer"
	"github.com/meovv-mail/meovv-mail/internal/stalwart"
	"github.com/meovv-mail/meovv-mail/internal/store"
	"github.com/meovv-mail/meovv-mail/internal/webhook"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	if err = os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		logger.Error("create data directory", "error", err)
		os.Exit(1)
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "meovv.sqlite"))
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	cipher, err := app.NewTokenCipher(cfg.SessionKey)
	if err != nil {
		logger.Error("initialize token encryption", "error", err)
		os.Exit(1)
	}
	stalwartClient := stalwart.New(cfg.StalwartURL, cfg.StalwartTLSInsecure)
	smtpSender := mailer.SMTP{Address: cfg.SMTPAddress, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, TLSConfig: &tls.Config{ServerName: cfg.SMTPTLSServerName, MinVersion: tls.VersionTLS12}}
	api := httpapi.New(cfg, db, smtpSender, stalwartClient, cipher, logger, env("MEOVV_STATIC_DIR", "/app/web"))
	server := &http.Server{Addr: cfg.Addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 2 * time.Minute}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	dispatcher := webhook.New(db, cipher)
	go dispatcher.Run(ctx)
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				_ = db.Cleanup(ctx, now.Add(-cfg.MessageRetention), now.Add(-cfg.WebhookRetention), now.Add(-cfg.AuditRetention))
			}
		}
	}()
	go func() {
		logger.Info("MEOVV Mail listening", "address", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server failed", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}
func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
