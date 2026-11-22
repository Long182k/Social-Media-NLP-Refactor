package graphql

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	gql "github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
)

type requestKey struct{}

type queryRequest struct {
	*loaders
	writer http.ResponseWriter
}

func NewHandler(db sqlc.DBTX, authService *auth.Service, logger *slog.Logger) http.Handler {
	queries := sqlc.New(db)
	server := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{queries: queries}}))
	server.AddTransport(transport.POST{})
	server.Use(extension.FixedComplexityLimit(750))
	server.SetErrorPresenter(func(ctx context.Context, err error) *gqlerror.Error {
		var clientError *gqlerror.Error
		if errors.As(err, &clientError) {
			return gql.DefaultErrorPresenter(ctx, err)
		}
		logger.ErrorContext(ctx, "GraphQL resolver failed", slog.String("error", err.Error()))
		return gql.DefaultErrorPresenter(ctx, gqlerror.Errorf("query temporarily unavailable"))
	})
	server.AroundOperations(func(ctx context.Context, next gql.OperationHandler) gql.ResponseHandler {
		claims, _ := auth.ClaimsFromContext(ctx)
		operation := gql.GetOperationContext(ctx)
		if claims.Role != "ADMIN" {
			fields := gql.CollectFields(operation, operation.Operation.SelectionSet, []string{"Query"})
			for _, field := range fields {
				if field.Name == "adminDashboardStats" {
					ctx.Value(requestKey{}).(queryRequest).writer.WriteHeader(http.StatusForbidden)
					return gql.OneShot(&gql.Response{Errors: gqlerror.List{
						{Message: "administrator access required", Extensions: map[string]any{"code": "FORBIDDEN"}},
					}})
				}
			}
		}
		return next(ctx)
	})
	return auth.RequireAuth(authService)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := auth.ClaimsFromContext(r.Context())
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		ctx := context.WithValue(r.Context(), requestKey{}, queryRequest{
			loaders: newLoaders(queries, claims.UserID), writer: w,
		})
		server.ServeHTTP(w, r.WithContext(ctx))
	}))
}

func requestLoaders(ctx context.Context) *loaders {
	return ctx.Value(requestKey{}).(queryRequest).loaders
}
