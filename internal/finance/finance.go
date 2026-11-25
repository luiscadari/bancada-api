// Package finance is the boundary between service orders and money received (RN-15,
// UC-06). The finance module is not built yet: NoPayments reports nothing paid, so every
// delivery with an amount due needs the owner's authorization until it arrives.
package finance

import (
	"context"

	"github.com/google/uuid"
)

// Ledger answers what was paid for an order and records balances left open.
type Ledger interface {
	// Paid returns the amount received for the order, in cents.
	Paid(ctx context.Context, orderID uuid.UUID) (int64, error)
	// OpenReceivable records a balance the owner authorized to leave open at delivery
	// (UC-05 3a).
	OpenReceivable(ctx context.Context, branchID, orderID uuid.UUID, amountCents int64) error
}

// NoPayments is the Ledger used while the finance module does not exist.
type NoPayments struct{}

func (NoPayments) Paid(context.Context, uuid.UUID) (int64, error) { return 0, nil }
func (NoPayments) OpenReceivable(context.Context, uuid.UUID, uuid.UUID, int64) error {
	return nil
}
