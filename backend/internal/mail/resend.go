package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	stdmail "net/mail"
	"strings"
	"time"
)

type ResendMailer struct {
	token  string
	owner  string
	client *http.Client
}

func NewResendMailer(apiKey, ownerEmail string, transport http.RoundTripper) (*ResendMailer, error) {
	apiKey = strings.TrimSpace(apiKey)
	owner := strings.ToLower(strings.TrimSpace(ownerEmail))
	address, err := stdmail.ParseAddress(owner)
	if apiKey == "" || strings.ContainsAny(apiKey, "\r\n") || err != nil || address.Address != owner {
		return nil, errors.New("the Resend demo requires RESEND_API_KEY and a valid RESEND_OWNER_EMAIL")
	}
	return &ResendMailer{
		token: apiKey, owner: owner,
		client: &http.Client{
			Timeout:       8 * time.Second,
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (m *ResendMailer) CheckResetRecipient(email string) error {
	if !strings.EqualFold(strings.TrimSpace(email), m.owner) {
		return ErrDemoRecipientRestricted
	}
	return nil
}

func (m *ResendMailer) SendPasswordReset(ctx context.Context, toEmail, resetURL string, expiresIn time.Duration) error {
	if err := m.CheckResetRecipient(toEmail); err != nil {
		return err
	}
	body, err := json.Marshal(struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
	}{
		From:    "onboarding@resend.dev",
		To:      []string{m.owner},
		Subject: "Reset your password",
		Text:    FormatPasswordResetBody(resetURL, expiresIn),
	})
	if err != nil {
		return ErrMailUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return ErrMailUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+m.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return ErrMailUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ErrMailUnavailable
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil || result.ID == "" {
		return ErrMailUnavailable
	}
	return nil
}
