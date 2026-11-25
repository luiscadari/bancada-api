// Package reqctx detaches request contexts from gin.
//
// The strict server hands handlers the *gin.Context as their context.Context. gin
// recycles that object through a pool once the request ends, so it must never reach
// code that may keep using the context afterwards (database/sql watches ctx.Done() from
// its own goroutines). Unwrap returns the underlying http.Request context instead.
package reqctx

import (
	"context"

	"github.com/gin-gonic/gin"
)

// Unwrap returns the request context of a *gin.Context, or ctx itself otherwise.
func Unwrap(ctx context.Context) context.Context {
	if gc, ok := ctx.(*gin.Context); ok && gc != nil && gc.Request != nil {
		return gc.Request.Context()
	}
	return ctx
}
