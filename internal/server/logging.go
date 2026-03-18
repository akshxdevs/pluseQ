package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

type ctxKey string

const correlationIDKey ctxKey = "correlation_id"

func CorrelationIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(correlationIDKey).(string); ok {
		return id
	}
	return ""
}

func withCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

func (s *Server) CorrelationIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Correlation-ID")
		if id == "" {
			id = uuid.NewString()
		}
		ctx := withCorrelationID(r.Context(), id)
		w.Header().Set("X-Correlation-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) logAttrs(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	attrs = append(attrs, slog.String("correlation_id", CorrelationIDFromContext(ctx)))
	s.logger.LogAttrs(ctx, level, msg, attrs...)
}

func (s *Server) logInfo(ctx context.Context, msg string, attrs ...slog.Attr) {
	s.logAttrs(ctx, slog.LevelInfo, msg, attrs...)
}

func (s *Server) logWarn(ctx context.Context, msg string, attrs ...slog.Attr) {
	s.logAttrs(ctx, slog.LevelWarn, msg, attrs...)
}

func (s *Server) logError(ctx context.Context, msg string, attrs ...slog.Attr) {
	s.logAttrs(ctx, slog.LevelError, msg, attrs...)
}
