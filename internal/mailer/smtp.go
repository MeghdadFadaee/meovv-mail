package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
)

type Sender interface {
	Send(context.Context, string, []string, []byte) (string, error)
}

type SMTP struct {
	Address, Username, Password string
	TLSConfig                   *tls.Config
}

func (s SMTP) Send(ctx context.Context, from string, to []string, raw []byte) (string, error) {
	host, _, err := net.SplitHostPort(s.Address)
	if err != nil {
		return "", err
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", s.Address)
	if err != nil {
		return "", fmt.Errorf("connect to submission service: %w", err)
	}
	defer conn.Close()
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return "", err
	}
	defer c.Close()
	tlsConfig := s.TLSConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err = c.StartTLS(tlsConfig); err != nil {
			return "", fmt.Errorf("start TLS: %w", err)
		}
	} else {
		return "", fmt.Errorf("submission service did not offer STARTTLS")
	}
	if s.Username != "" {
		auth := smtp.PlainAuth("", s.Username, s.Password, host)
		if err = c.Auth(auth); err != nil {
			return "", fmt.Errorf("authenticate: %w", err)
		}
	}
	address, err := mailbox(from)
	if err != nil {
		return "", err
	}
	if err = c.Mail(address); err != nil {
		return "", err
	}
	for _, recipient := range to {
		if err = c.Rcpt(recipient); err != nil {
			return "", err
		}
	}
	w, err := c.Data()
	if err != nil {
		return "", err
	}
	if _, err = w.Write(raw); err != nil {
		return "", err
	}
	if err = w.Close(); err != nil {
		return "", err
	}
	if err = c.Quit(); err != nil {
		return "", err
	}
	return "smtp-accepted", nil
}

func mailbox(value string) (string, error) {
	start := strings.LastIndex(value, "<")
	end := strings.LastIndex(value, ">")
	if start >= 0 && end > start {
		return strings.TrimSpace(value[start+1 : end]), nil
	}
	if strings.Contains(value, "@") {
		return strings.TrimSpace(value), nil
	}
	return "", fmt.Errorf("invalid mailbox")
}
