package mail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSMTPMailerSendPasswordReset(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()

	var (
		mu       sync.Mutex
		captured string
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()

		r := bufio.NewReader(conn)
		writeLine := func(line string) {
			_, _ = conn.Write([]byte(line + "\r\n"))
		}
		writeLine("220 localhost ESMTP test")

		var dataMode bool
		var buf strings.Builder
		for {
			line, readErr := r.ReadString('\n')
			if readErr != nil {
				break
			}
			trimmed := strings.TrimRight(line, "\r\n")
			if dataMode {
				if trimmed == "." {
					dataMode = false
					mu.Lock()
					captured = buf.String()
					mu.Unlock()
					writeLine("250 2.0.0 Ok")
					continue
				}
				buf.WriteString(line)
				continue
			}
			upper := strings.ToUpper(trimmed)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				writeLine("250 localhost")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				writeLine("250 Ok")
			case strings.HasPrefix(upper, "RCPT TO:"):
				writeLine("250 Ok")
			case upper == "DATA":
				dataMode = true
				writeLine("354 End data with <CR><LF>.<CR><LF>")
			case upper == "QUIT":
				writeLine("221 Bye")
				return
			default:
				writeLine("250 Ok")
			}
		}
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	mailer := NewSMTPMailer(host, port, "no-reply@social-media.local")
	resetURL := "http://localhost:5173/reset-password?token=abc123token"
	if err := mailer.SendPasswordReset(context.Background(), "alice@example.com", resetURL, 30*time.Minute); err != nil {
		t.Fatalf("SendPasswordReset failed: %v", err)
	}
	<-done

	mu.Lock()
	msg := captured
	mu.Unlock()

	if !strings.Contains(msg, "To: alice@example.com") {
		t.Fatalf("expected To header in message, got:\n%s", msg)
	}
	if !strings.Contains(msg, resetURL) {
		t.Fatalf("expected reset URL in body, got:\n%s", msg)
	}
}

func TestSMTPMailerUnavailableReturnsSentinel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)
	ln.Close()

	mailer := NewSMTPMailer(host, port, "no-reply@social-media.local")
	err = mailer.SendPasswordReset(context.Background(), "alice@example.com", "http://localhost:5173/reset-password?token=x", 15*time.Minute)
	if !errors.Is(err, ErrMailUnavailable) {
		t.Fatalf("expected ErrMailUnavailable, got %v", err)
	}
}
