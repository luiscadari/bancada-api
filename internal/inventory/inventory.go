// Package inventory is the boundary between service orders and stock (RN-20). The stock
// module is not built yet: NoOp accepts every operation, so orders work without stock
// control until it arrives.
package inventory

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// PartLine is a quantity of a product used by a service order.
type PartLine struct {
	ProductID uuid.UUID
	Quantity  decimal.Decimal
}

// Reserver holds, frees and consumes the parts of a service order in its branch.
type Reserver interface {
	// Reserve holds the parts of an approved quote, replacing an earlier reservation.
	Reserve(ctx context.Context, branchID, orderID uuid.UUID, parts []PartLine) error
	// Release frees the parts reserved for the order.
	Release(ctx context.Context, branchID, orderID uuid.UUID) error
	// Consume turns the order's reservation into a stock exit (parts applied).
	Consume(ctx context.Context, branchID, orderID uuid.UUID) error
}

// NoOp is the Reserver used while the stock module does not exist.
type NoOp struct{}

func (NoOp) Reserve(context.Context, uuid.UUID, uuid.UUID, []PartLine) error { return nil }
func (NoOp) Release(context.Context, uuid.UUID, uuid.UUID) error             { return nil }
func (NoOp) Consume(context.Context, uuid.UUID, uuid.UUID) error             { return nil }
