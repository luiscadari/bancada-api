package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"

	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/apperr"
)

const requestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// RequestIDMiddleware propagates or creates the X-Request-ID of each request.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if id == "" || len(id) > 128 {
			id = uuid.NewString()
		}
		c.Header(requestIDHeader, id)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), requestIDKey{}, id))
		c.Next()
	}
}

// RequestID returns the request id of c.
func RequestID(c *gin.Context) string {
	id, _ := c.Request.Context().Value(requestIDKey{}).(string)
	return id
}

// LoggerMiddleware writes one structured log line per request (RNF-09).
func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		attrs := []any{
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"request_id", RequestID(c),
			"ip", c.ClientIP(),
		}
		if a, ok := actor.From(c.Request.Context()); ok {
			attrs = append(attrs, "account_id", a.AccountID, "user_id", a.UserID)
		}
		level := slog.LevelInfo
		if c.Writer.Status() >= 500 {
			level = slog.LevelError
		}
		slog.Log(c.Request.Context(), level, "http_request", attrs...)
	}
}

// RecoveryMiddleware turns panics into a 500 problem.
func RecoveryMiddleware() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, rec any) {
		slog.ErrorContext(c.Request.Context(), "panic", "panic", rec, "request_id", RequestID(c))
		WriteError(c, apperr.New(http.StatusInternalServerError, "internal_error",
			"Algo deu errado do nosso lado. Tente de novo em instantes."))
	})
}

// CORSMiddleware allows the configured browser origins (web panel and portal).
func CORSMiddleware(origins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && (slices.Contains(origins, "*") || slices.Contains(origins, origin)) {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", "X-Request-ID")
			if c.Request.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Branch-ID, X-Request-ID")
				h.Set("Access-Control-Max-Age", "600")
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
		}
		c.Next()
	}
}

// ValidatorMiddleware validates requests against the OpenAPI contract (required fields,
// formats, enums, bounds). Authentication is handled by the tenancy middleware.
func ValidatorMiddleware(spec *openapi3.T) gin.HandlerFunc {
	spec.Servers = nil // match paths regardless of host
	return ginmiddleware.OapiRequestValidatorWithOptions(spec, &ginmiddleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			MultiError:         true,
		},
		ErrorHandler: func(c *gin.Context, message string, status int) {
			if status == http.StatusNotFound {
				WriteError(c, apperr.NotFound("route_not_found", "Endereço não encontrado."))
				return
			}
			if status == http.StatusMethodNotAllowed {
				WriteError(c, apperr.New(status, "method_not_allowed", "Método não permitido neste endereço."))
				return
			}
			WriteError(c, apperr.Validation("Confira os dados enviados.", validationFields(message)...))
		},
	})
}

func validationFields(message string) []apperr.FieldError {
	var fields []apperr.FieldError
	for _, line := range strings.Split(message, " | ") {
		line = strings.TrimSpace(line)
		if line != "" {
			fields = append(fields, apperr.FieldError{Field: "", Message: line})
		}
	}
	return fields
}
