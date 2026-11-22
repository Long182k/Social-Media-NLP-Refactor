package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
)

type claimsContextKey struct{}

func ClaimsFromContext(ctx context.Context) (*AuthClaims, bool) {
	c, ok := ctx.Value(claimsContextKey{}).(*AuthClaims)
	return c, ok && c != nil
}

func RequireAuth(svc *Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid authorization header")
				return
			}
			raw := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
			claims, err := svc.ParseAccessToken(raw)
			if err != nil {
				httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired access token")
				return
			}
			ctx := context.WithValue(r.Context(), claimsContextKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireRole(allowedRoles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedRoles))
	for _, role := range allowedRoles {
		allowed[strings.ToUpper(strings.TrimSpace(role))] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFromContext(r.Context())
			if !ok {
				httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
				return
			}
			if _, ok := allowed[strings.ToUpper(claims.Role)]; !ok {
				httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
