package mail

import (
	"context"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type SMTPMailer struct {
	Host        string
	Port        int
	From        string
	DialTimeout time.Duration
}

func NewSMTPMailer(host string, port int, from string) *SMTPMailer {
	return &SMTPMailer{
		Host:        host,
		Port:        port,
		From:        from,
		DialTimeout: 5 * time.Second,
	}
}

func (m *SMTPMailer) SendPasswordReset(ctx context.Context, toEmail, resetURL string, expiresIn time.Duration) error {
	toEmail = strings.TrimSpace(toEmail)
	if toEmail == "" || strings.ContainsAny(toEmail, "\r\n") {
		return fmt.Errorf("invalid recipient email")
	}
	from := strings.TrimSpace(m.From)
	if from == "" || strings.ContainsAny(from, "\r\n") {
		return fmt.Errorf("invalid sender email")
	}

	addr := net.JoinHostPort(m.Host, fmt.Sprintf("%d", m.Port))
	dialer := net.Dialer{Timeout: m.DialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMailUnavailable, err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(m.DialTimeout))
	}

	client, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("%w: %v", ErrMailUnavailable, err)
	}
	defer client.Close()

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("%w: %v", ErrMailUnavailable, err)
	}
	if err := client.Rcpt(toEmail); err != nil {
		return fmt.Errorf("%w: %v", ErrMailUnavailable, err)
	}

	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMailUnavailable, err)
	}

	subject := "Password Reset Instructions"
	body := FormatPasswordResetBody(resetURL, expiresIn)
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		from,
		toEmail,
		subject,
		body,
	)

	if _, err := wc.Write([]byte(msg)); err != nil {
		_ = wc.Close()
		return fmt.Errorf("%w: %v", ErrMailUnavailable, err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("%w: %v", ErrMailUnavailable, err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("%w: %v", ErrMailUnavailable, err)
	}
	return nil
}
