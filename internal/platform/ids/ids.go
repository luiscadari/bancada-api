// Package ids generates identifiers. Every entity uses UUIDv7, which is time-ordered
// and can be generated on the client device for offline creation (ADR-03).
package ids

import "github.com/google/uuid"

// New returns a new UUIDv7.
func New() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

// Ptr returns a pointer to id, or nil when id is the zero UUID.
func Ptr(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
