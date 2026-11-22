package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
)

func TestDemoRecipientRestrictionReturns503(t *testing.T) {
	recorder := httptest.NewRecorder()
	NewHandler(nil).writeAuthError(recorder, ErrDemoRecipientFiltered)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	var response httpserver.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error != "DEMO_RECIPIENT_RESTRICTED" {
		t.Fatalf("error code = %q, want DEMO_RECIPIENT_RESTRICTED", response.Error)
	}
}
