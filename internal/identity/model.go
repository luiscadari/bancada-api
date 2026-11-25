// Package identity manages accounts (tenants), branches, users and their roles,
// devices, service order number blocks and authentication.
package identity

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/luiscadari/bancada-api/internal/platform/database"
)

// Account status values.
const (
	AccountTrial      = "trial"
	AccountActive     = "active"
	AccountDelinquent = "delinquent"
	AccountSuspended  = "suspended"
	AccountClosed     = "closed"
)

// User status values.
const (
	UserInvited  = "invited"
	UserActive   = "active"
	UserDisabled = "disabled"
)

// TrialDays is the trial length of a new account (RN-S01).
const TrialDays = 14

type Account struct {
	ID             uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	TradeName      string         `json:"trade_name"`
	Document       *string        `json:"document"`
	Niche          string         `json:"niche"`
	Status         string         `json:"status"`
	TrialEndsAt    *time.Time     `json:"trial_ends_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `json:"deleted_at,omitempty"`
	Version        int64          `gorm:"not null;default:1" json:"version"`
	OriginDeviceID *uuid.UUID     `gorm:"type:uuid" json:"origin_device_id,omitempty"`
	CreatedBy      *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	UpdatedBy      *uuid.UUID     `gorm:"type:uuid" json:"updated_by,omitempty"`
}

// DiagnosticFee configures the diagnostic fee of a branch (RN-13).
type DiagnosticFee struct {
	Enabled          bool  `json:"enabled"`
	AmountCents      int64 `json:"amount_cents"`
	DeductOnApproval bool  `json:"deduct_on_approval"`
}

// BranchSettings are the per-branch rules (RF-40).
type BranchSettings struct {
	DiagnosticFee               DiagnosticFee `json:"diagnostic_fee"`
	QuoteValidityDays           int           `json:"quote_validity_days"`
	DefaultWarrantyDays         int           `json:"default_warranty_days"`
	PickupReminderDays          []int         `json:"pickup_reminder_days"`
	AbandonmentDays             int           `json:"abandonment_days"`
	StorageFeeEnabled           bool          `json:"storage_fee_enabled"`
	AllowNegativeStock          bool          `json:"allow_negative_stock"`
	TechnicianCanOpenAndDeliver bool          `json:"technician_can_open_and_deliver"`
	AttendantMaxDiscountPercent int           `json:"attendant_max_discount_percent"`
	Terms                       string        `json:"terms"`
}

// DefaultBranchSettings returns the defaults of the requirements document.
func DefaultBranchSettings() BranchSettings {
	return BranchSettings{
		DiagnosticFee:               DiagnosticFee{},
		QuoteValidityDays:           7,                // RN-09
		DefaultWarrantyDays:         90,               // RN-18
		PickupReminderDays:          []int{7, 15, 30}, // RN-16
		AbandonmentDays:             1095,             // RN-16, three years
		AttendantMaxDiscountPercent: 10,               // RN-12
	}
}

type Branch struct {
	database.Base
	Name     string                        `json:"name"`
	OSPrefix string                        `gorm:"column:os_prefix" json:"os_prefix"`
	Timezone string                        `json:"timezone"`
	Settings database.JSON[BranchSettings] `json:"settings"`
}

type BranchCounter struct {
	BranchID   uuid.UUID `gorm:"type:uuid;primaryKey" json:"branch_id"`
	AccountID  uuid.UUID `gorm:"type:uuid" json:"account_id"`
	NextNumber int64     `json:"next_number"`
}

type User struct {
	database.Base
	Name         string     `json:"name"`
	Email        string     `json:"email"`
	PasswordHash *string    `json:"-"`
	MFAEnabled   bool       `gorm:"column:mfa_enabled" json:"mfa_enabled"`
	Status       string     `json:"status"`
	LastLoginAt  *time.Time `json:"last_login_at"`
}

type UserBranchRole struct {
	database.Base
	UserID   uuid.UUID  `gorm:"type:uuid" json:"user_id"`
	BranchID *uuid.UUID `gorm:"type:uuid" json:"branch_id"`
	Role     string     `json:"role"`
}

type Invitation struct {
	database.Base
	UserID     uuid.UUID  `gorm:"type:uuid" json:"user_id"`
	TokenHash  string     `json:"-"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at"`
}

type Device struct {
	database.Base
	BranchID   uuid.UUID  `gorm:"type:uuid" json:"branch_id"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	RevokedBy  *uuid.UUID `gorm:"type:uuid" json:"revoked_by"`
}

type NumberBlock struct {
	database.Base
	BranchID   uuid.UUID `gorm:"type:uuid" json:"branch_id"`
	DeviceID   uuid.UUID `gorm:"type:uuid" json:"device_id"`
	RangeStart int64     `json:"range_start"`
	RangeEnd   int64     `json:"range_end"`
}

type RefreshToken struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey"`
	AccountID uuid.UUID  `gorm:"type:uuid"`
	UserID    uuid.UUID  `gorm:"type:uuid"`
	DeviceID  *uuid.UUID `gorm:"type:uuid"`
	FamilyID  uuid.UUID  `gorm:"type:uuid"`
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
	IP        *string `gorm:"column:ip"`
	UserAgent *string
	CreatedAt time.Time
}

// FormatNumber formats a service order number, e.g. MAT-000123 (RN-04).
func FormatNumber(prefix string, n int64) string {
	return fmt.Sprintf("%s-%06d", prefix, n)
}
