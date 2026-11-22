package mail

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrDemoRecipientRestricted = errors.New("recipient restricted in demo mode")
	ErrMailUnavailable         = errors.New("mail provider unavailable")
)

type MailProvider interface {
	SendPasswordReset(ctx context.Context, toEmail, resetURL string, expiresIn time.Duration) error
}

type ResetRecipientPolicy interface {
	CheckResetRecipient(email string) error
}

func FormatPasswordResetBody(resetURL string, expiresIn time.Duration) string {
	minutes := int(expiresIn.Minutes())
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf(
		"You requested a password reset.\r\n\r\n"+
			"Open the link below to set a new password (valid for %d minutes):\r\n%s\r\n\r\n"+
			"If you did not request this change, you can safely ignore this email.\r\n",
		minutes,
		resetURL,
	)
}
