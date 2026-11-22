package auth_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/social-media-nlp/refactor/backend/internal/mail"
)

type resendTestTransport func(*http.Request) (*http.Response, error)

func (transport resendTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestForgotPasswordResendDemo(t *testing.T) {
	tag := "resend-" + uuid.NewString()
	owner := tag + "@example.test"
	thirdParty := tag + "-other@example.test"
	attempts := 0
	mailer, err := mail.NewResendMailer("testing-only", owner, resendTestTransport(func(req *http.Request) (*http.Response, error) {
		attempts++
		body, err := io.ReadAll(req.Body)
		if err != nil || !strings.Contains(string(body), owner) || strings.Contains(string(body), thirdParty) {
			t.Error("owner expected in the provider request")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"test-email"}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	pool, _, _, router := setupAuthTestEnvWithMail(t, mailer)
	defer pool.Exec(context.Background(), "DELETE FROM users WHERE email = ANY($1)", []string{owner, thirdParty})
	for index, email := range []string{owner, thirdParty} {
		response := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
			"email": email, "username": []string{tag, tag + "-other"}[index], "password": "testing-password",
		}, nil)
		if response.Code != http.StatusCreated {
			t.Fatalf("register fixture: %d", response.Code)
		}
	}
	for _, email := range []string{thirdParty, "unknown@example.test"} {
		response := doJSONReq(t, router, http.MethodPost, "/api/auth/forgot-password", map[string]string{"email": email}, nil)
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "DEMO_RECIPIENT_RESTRICTED") || attempts != 0 {
			t.Fatalf("third-party reset: status %d attempts %d, body %s", response.Code, attempts, response.Body)
		}
	}
	response := doJSONReq(t, router, http.MethodPost, "/api/auth/forgot-password", map[string]string{"email": owner}, nil)
	if response.Code != http.StatusOK || attempts != 1 {
		t.Fatalf("owner reset: status %d attempts %d, body %s", response.Code, attempts, response.Body)
	}
}
