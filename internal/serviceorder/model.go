// Package serviceorder runs the life of a service order (OS): opening and numbering,
// the status flow of section 8, versioned quotes and their approval, execution,
// delivery with per-item warranty, cancelation, abandonment and warranty returns.
package serviceorder

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/luiscadari/bancada-api/internal/platform/database"
)

// Order is a service order. CustomerName and ApprovedTotalCents are read-only values
// loaded with the order (see orderQuery).
type Order struct {
	database.Base
	BranchID                uuid.UUID                     `gorm:"type:uuid" json:"branch_id"`
	Sequence                int64                         `json:"sequence"`
	Number                  string                        `json:"number"`
	CustomerID              uuid.UUID                     `gorm:"type:uuid" json:"customer_id"`
	EquipmentID             uuid.UUID                     `gorm:"type:uuid" json:"equipment_id"`
	TechnicianID            *uuid.UUID                    `gorm:"type:uuid" json:"technician_id"`
	Priority                string                        `json:"priority"`
	OriginOrderID           *uuid.UUID                    `gorm:"type:uuid" json:"origin_order_id"`
	RequesterContactID      *uuid.UUID                    `gorm:"type:uuid" json:"requester_contact_id"`
	ApproverContactID       *uuid.UUID                    `gorm:"type:uuid" json:"approver_contact_id"`
	CostCenterID            *uuid.UUID                    `gorm:"type:uuid" json:"cost_center_id"`
	Status                  string                        `json:"status"`
	ReportedDefect          string                        `json:"reported_defect"`
	Diagnosis               *string                       `json:"diagnosis"`
	TechnicalReport         *string                       `json:"technical_report"`
	EntryChecklist          database.JSON[map[string]any] `json:"entry_checklist"`
	Accessories             *string                       `json:"accessories"`
	DevicePasswordEncrypted []byte                        `json:"-"` // never audited nor serialized (RN-31)
	PreAuthorizedCents      *int64                        `json:"pre_authorized_cents"`
	DiagnosticFeeCents      int64                         `json:"diagnostic_fee_cents"`
	ExpectedDeliveryAt      *time.Time                    `json:"expected_delivery_at"`
	ReadyAt                 *time.Time                    `json:"ready_at"`
	DeliveredAt             *time.Time                    `json:"delivered_at"`
	CanceledAt              *time.Time                    `json:"canceled_at"`
	AbandonedAt             *time.Time                    `json:"abandoned_at"`
	CancelReason            *string                       `json:"cancel_reason"`
	PickedUpByName          *string                       `json:"picked_up_by_name"`
	PickedUpByDocument      *string                       `json:"picked_up_by_document"`
	OpenBalanceCents        int64                         `json:"open_balance_cents"`
	BalanceAuthorizedBy     *uuid.UUID                    `gorm:"type:uuid" json:"balance_authorized_by"`

	CustomerName       string `gorm:"->" json:"-"`
	ApprovedTotalCents int64  `gorm:"->" json:"-"`
}

func (Order) TableName() string { return "service_orders" }

// AmountDueCents is what the customer owes for the order: the approved quote plus the
// diagnostic fee due (RN-13, RN-14).
func (o Order) AmountDueCents() int64 { return o.ApprovedTotalCents + o.DiagnosticFeeCents }

// HasDevicePassword reports whether the order keeps an equipment password.
func (o Order) HasDevicePassword() bool { return len(o.DevicePasswordEncrypted) > 0 }

// Quote statuses.
const (
	QuoteDraft      = "draft"
	QuoteSent       = "sent"
	QuoteApproved   = "approved"
	QuoteRejected   = "rejected"
	QuoteSuperseded = "superseded"
)

// Decision channels.
const (
	ChannelPreAuthorized = "pre_authorized"
	ChannelNoCharge      = "no_charge"
)

// Item kinds.
const (
	KindService = "service"
	KindPart    = "part"
)

type Quote struct {
	database.Base
	OrderID            uuid.UUID   `gorm:"type:uuid" json:"order_id"`
	Number             int         `json:"number"`
	Status             string      `json:"status"`
	Notes              *string     `json:"notes"`
	SubtotalCents      int64       `json:"subtotal_cents"`
	DiscountCents      int64       `json:"discount_cents"`
	TotalCents         int64       `json:"total_cents"`
	SentAt             *time.Time  `json:"sent_at"`
	ValidUntil         *time.Time  `json:"valid_until"`
	DecisionChannel    *string     `json:"decision_channel"`
	DecidedByName      *string     `json:"decided_by_name"`
	DecidedByContactID *uuid.UUID  `gorm:"type:uuid" json:"decided_by_contact_id"`
	DecidedByUserID    *uuid.UUID  `gorm:"type:uuid" json:"decided_by_user_id"`
	DecidedAt          *time.Time  `json:"decided_at"`
	DecisionIP         *string     `gorm:"column:decision_ip" json:"decision_ip"`
	RejectionReason    *string     `json:"rejection_reason"`
	PreviousQuoteID    *uuid.UUID  `gorm:"type:uuid" json:"previous_quote_id"`
	Items              []QuoteItem `gorm:"-" json:"-"`
}

type QuoteItem struct {
	database.Base
	QuoteID        uuid.UUID       `gorm:"type:uuid" json:"quote_id"`
	Position       int             `json:"position"`
	Kind           string          `json:"kind"`
	ServiceID      *uuid.UUID      `gorm:"type:uuid" json:"service_id"`
	ProductID      *uuid.UUID      `gorm:"type:uuid" json:"product_id"`
	Description    string          `json:"description"`
	Quantity       decimal.Decimal `json:"quantity"`
	ListPriceCents int64           `json:"list_price_cents"`
	UnitPriceCents int64           `json:"unit_price_cents"`
	DiscountCents  int64           `json:"discount_cents"`
	TotalCents     int64           `json:"total_cents"`
	Optional       bool            `json:"optional"`
	WarrantyDays   int             `json:"warranty_days"`
	WarrantyUntil  *time.Time      `gorm:"type:date" json:"warranty_until"`
	CoveredItemID  *uuid.UUID      `gorm:"type:uuid" json:"covered_item_id"`
}

// StatusChange is an entry of the append-only status history (RN-07).
type StatusChange struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	AccountID  uuid.UUID  `gorm:"type:uuid" json:"account_id"`
	OrderID    uuid.UUID  `gorm:"type:uuid" json:"order_id"`
	FromStatus *string    `json:"from_status"`
	ToStatus   string     `json:"to_status"`
	Reason     *string    `json:"reason"`
	UserID     *uuid.UUID `gorm:"type:uuid" json:"user_id"`
	DeviceID   *uuid.UUID `gorm:"type:uuid" json:"device_id"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (StatusChange) TableName() string { return "order_status_history" }

// Warranty is an item of a delivered order still under warranty (RN-18, RF-36).
type Warranty struct {
	ItemID        uuid.UUID
	OrderID       uuid.UUID
	OrderNumber   string
	Kind          string
	Description   string
	WarrantyUntil time.Time
	DeliveredAt   *time.Time
}
