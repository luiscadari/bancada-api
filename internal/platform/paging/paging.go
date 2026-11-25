// Package paging implements keyset pagination over UUIDv7 ids (newest first).
package paging

import (
	"encoding/base64"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/luiscadari/bancada-api/internal/platform/apperr"
)

const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Page is a requested page.
type Page struct {
	Limit int
	After *uuid.UUID
}

// Parse validates the limit and cursor query parameters.
func Parse(limit *int, cursor *string) (Page, error) {
	p := Page{Limit: DefaultLimit}
	if limit != nil {
		p.Limit = min(max(*limit, 1), MaxLimit)
	}
	if cursor != nil && *cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(*cursor)
		if err != nil {
			return p, invalidCursor()
		}
		id, err := uuid.ParseBytes(raw)
		if err != nil {
			return p, invalidCursor()
		}
		p.After = &id
	}
	return p, nil
}

func invalidCursor() error {
	return apperr.Validation("Página inválida. Recarregue a lista.", apperr.Field("cursor", "inválido"))
}

// Apply orders by idColumn descending and fetches one extra row to detect a next page.
func (p Page) Apply(db *gorm.DB, idColumn string) *gorm.DB {
	if p.After != nil {
		db = db.Where(idColumn+" < ?", *p.After)
	}
	return db.Order(idColumn + " DESC").Limit(p.Limit + 1)
}

// Trim drops the extra row fetched by Apply and returns the cursor of the next page.
func Trim[T any](p Page, items []T, id func(T) uuid.UUID) ([]T, *string) {
	if len(items) <= p.Limit {
		return items, nil
	}
	items = items[:p.Limit]
	next := base64.RawURLEncoding.EncodeToString([]byte(id(items[len(items)-1]).String()))
	return items, &next
}

// Like escapes the LIKE wildcards of a user search term.
func Like(term string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(term))
}
