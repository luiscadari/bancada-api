// Package httpx holds the HTTP plumbing shared by every module: middlewares, the
// problem+json error writer and the tenant/authorization middleware of the strict server.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/luiscadari/bancada-api/internal/platform/apperr"
)

type problem struct {
	Type      string              `json:"type"`
	Title     string              `json:"title"`
	Status    int                 `json:"status"`
	Code      string              `json:"code"`
	Detail    string              `json:"detail,omitempty"`
	RequestID string              `json:"request_id,omitempty"`
	Errors    []apperr.FieldError `json:"errors,omitempty"`
}

// WriteError renders err as application/problem+json and aborts the gin chain.
func WriteError(c *gin.Context, err error) {
	e := apperr.From(err)
	if e.Status >= 500 {
		slog.ErrorContext(c.Request.Context(), "request failed", "error", err, "request_id", RequestID(c))
	}
	body, _ := json.Marshal(problem{
		Type:      "about:blank",
		Title:     http.StatusText(e.Status),
		Status:    e.Status,
		Code:      e.Code,
		Detail:    e.Detail,
		RequestID: RequestID(c),
		Errors:    e.Fields,
	})
	_ = c.Error(err)
	c.Abort()
	c.Data(e.Status, "application/problem+json", body)
}
