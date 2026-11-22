package auth

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
)

const RefreshCookieName = "refreshToken"

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/auth", func(ar chi.Router) {
		ar.Post("/register", h.Register)
		ar.Post("/login", h.Login)
		ar.Post("/refresh", h.Refresh)
		ar.Post("/signout", h.Signout)
		ar.Post("/forgot-password", h.ForgotPassword)
		ar.Post("/reset-password", h.ResetPassword)

		ar.Group(func(pr chi.Router) {
			pr.Use(RequireAuth(h.svc))
			pr.Post("/change-password", h.ChangePassword)
			pr.Get("/me", h.Me)
		})
	})
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := decodeJSON(w, r, &req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON request body")
		return
	}

	resp, refreshTok, err := h.svc.Register(r.Context(), req)
	if err != nil {
		h.writeAuthError(w, err)
		return
	}

	h.setRefreshCookie(w, refreshTok)
	httpserver.WriteJSON(w, http.StatusCreated, resp)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON request body")
		return
	}

	clientIP := h.extractClientIP(r)
	resp, refreshTok, err := h.svc.Login(r.Context(), req, clientIP)
	if err != nil {
		h.writeAuthError(w, err)
		return
	}

	h.setRefreshCookie(w, refreshTok)
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(RefreshCookieName)
	if err != nil || cookie.Value == "" {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing refresh token cookie")
		return
	}

	resp, newRefreshTok, err := h.svc.Refresh(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) {
			h.clearRefreshCookie(w)
		}
		h.writeAuthError(w, err)
		return
	}

	h.setRefreshCookie(w, newRefreshTok)
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) Signout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(RefreshCookieName); err == nil && cookie.Value != "" {
		h.svc.Signout(r.Context(), cookie.Value)
	}
	h.clearRefreshCookie(w)
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{"message": "Signed out successfully"})
}

func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req ForgotPasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON request body")
		return
	}

	if err := h.svc.ForgotPassword(r.Context(), req.Email); err != nil {
		h.writeAuthError(w, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "If an account exists, password reset instructions have been sent",
	})
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req ResetPasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON request body")
		return
	}

	if err := h.svc.ResetPassword(r.Context(), req); err != nil {
		h.writeAuthError(w, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Password reset successfully",
	})
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	var req ChangePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON request body")
		return
	}

	if err := h.svc.ChangePassword(r.Context(), claims.UserID, req); err != nil {
		h.writeAuthError(w, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Password changed successfully",
	})
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	user, err := h.svc.Me(r.Context(), claims.UserID)
	if err != nil {
		h.writeAuthError(w, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, user)
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, token string) {
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Production config requires Secure. Local HTTP tests use false.
		Name:     RefreshCookieName,
		Value:    token,
		Path:     "/api/auth",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.svc.CookieSecure(),
		MaxAge:   h.svc.RefreshCookieMaxAge(),
	})
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Match the configured secure setting of the refresh cookie.
		Name:     RefreshCookieName,
		Value:    "",
		Path:     "/api/auth",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.svc.CookieSecure(),
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	})
}

func (h *Handler) writeAuthError(w http.ResponseWriter, err error) {
	var rlErr *RateLimitError
	if errors.As(err, &rlErr) {
		w.Header().Set("Retry-After", strconv.Itoa(rlErr.RetryAfterSeconds))
		httpserver.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", rlErr.Error())
		return
	}

	var valErr *ValidationError
	if errors.As(err, &valErr) {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", valErr.Message)
		return
	}

	switch {
	case errors.Is(err, ErrInvalidResetToken):
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_OR_EXPIRED_TOKEN", "reset token is invalid or expired")
	case errors.Is(err, ErrConflict):
		httpserver.WriteError(w, http.StatusConflict, "CONFLICT", "username, nickname, or email already exists")
	case errors.Is(err, ErrUnauthorized):
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired credentials")
	case errors.Is(err, ErrForbidden):
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "account is deactivated or lacks permissions")
	case errors.Is(err, ErrDemoRecipientFiltered):
		httpserver.WriteError(w, http.StatusServiceUnavailable, "DEMO_RECIPIENT_RESTRICTED", "recipient restricted in demo mode")
	case errors.Is(err, ErrServiceUnavailable):
		httpserver.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "authentication service temporarily unavailable")
	default:
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func (h *Handler) extractClientIP(r *http.Request) string {
	if h.svc.cfg.TrustProxyHeaders {
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			parts := strings.Split(xff, ",")
			for i := len(parts) - 1; i >= 0; i-- {
				if ip := normalizeClientIP(parts[i]); ip != "" {
					return ip
				}
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			if ip := normalizeClientIP(xri); ip != "" {
				return ip
			}
		}
	}
	if ip := normalizeClientIP(r.RemoteAddr); ip != "" {
		return ip
	}
	return "unknown"
}

func normalizeClientIP(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if addr, err := netip.ParseAddr(s); err == nil {
		return addr.Unmap().WithZone("").String()
	}
	return ""
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{
		Time:  t.UTC(),
		Valid: true,
	}
}
