package mail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type resendTransport func(*http.Request) (*http.Response, error)

func (f resendTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestResendOnlyAllowsOwner(t *testing.T) {
	attempts := 0
	mailer, err := NewResendMailer("testing-only", "owner@example.test", resendTransport(func(r *http.Request) (*http.Response, error) {
		attempts++
		if r.URL.String() != "https://api.resend.com/emails" || r.Method != http.MethodPost ||
			r.Header.Get("Authorization") != "Bearer testing-only" {
			t.Error("unexpected Resend request")
		}
		var payload struct {
			From    string
			To      []string
			Subject string
			Text    string
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.From != "onboarding@resend.dev" || len(payload.To) != 1 || payload.To[0] != "owner@example.test" ||
			payload.Subject == "" || !strings.Contains(payload.Text, "/reset-password?token=example") {
			t.Errorf("invalid reset payload: %+v", payload)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"test-id"}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	url := "https://local.example/reset-password?token=example"
	err = mailer.SendPasswordReset(ctx, "unrelated@example.test", url, 30*time.Minute)
	if !errors.Is(err, ErrDemoRecipientRestricted) || attempts != 0 {
		t.Fatalf("unrelated recipient attempts=%d err=%v", attempts, err)
	}
	err = mailer.SendPasswordReset(ctx, " Owner@Example.Test ", url, 30*time.Minute)
	if err != nil || attempts != 1 {
		t.Fatalf("owner attempts=%d err=%v", attempts, err)
	}
}
