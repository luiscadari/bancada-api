// Package registry holds the account-wide records (cadastros): customers with contacts
// and cost centers, equipment types with custom fields and entry checklists, equipment,
// and the catalog of services, products and suppliers (RN-02).
package registry

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/luiscadari/bancada-api/internal/platform/database"
)

const (
	CustomerIndividual = "individual"
	CustomerCompany    = "company"
)

// Consent records a communication consent per channel (RF-01).
type Consent struct {
	Granted   bool       `json:"granted"`
	UpdatedAt *time.Time `json:"updated_at"`
}

type Consents struct {
	WhatsApp Consent `json:"whatsapp"`
	Email    Consent `json:"email"`
}

type Customer struct {
	database.Base
	Type       string                  `json:"type"`
	Name       string                  `json:"name"`
	Document   *string                 `json:"document"`
	Notes      *string                 `json:"notes"`
	LegalBasis string                  `json:"legal_basis"`
	Consents   database.JSON[Consents] `json:"consents"`
}

type Contact struct {
	database.Base
	CustomerID         uuid.UUID `gorm:"type:uuid" json:"customer_id"`
	Name               string    `json:"name"`
	Phone              *string   `json:"phone"`
	Email              *string   `json:"email"`
	IsPrimary          bool      `json:"is_primary"`
	IsRequester        bool      `json:"is_requester"`
	IsApprover         bool      `json:"is_approver"`
	ApprovalLimitCents *int64    `json:"approval_limit_cents"`
}

func (Contact) TableName() string { return "customer_contacts" }

type CostCenter struct {
	database.Base
	CustomerID uuid.UUID `gorm:"type:uuid" json:"customer_id"`
	Name       string    `json:"name"`
	Code       *string   `json:"code"`
	Active     bool      `json:"active"`
}

// CustomerView is a customer with its contacts and cost centers.
type CustomerView struct {
	Customer
	Contacts    []Contact
	CostCenters []CostCenter
}

const (
	FieldText    = "text"
	FieldNumber  = "number"
	FieldInteger = "integer"
	FieldBoolean = "boolean"
	FieldDate    = "date"
	FieldSelect  = "select"
)

type EquipmentType struct {
	database.Base
	Name        string                        `json:"name"`
	Description *string                       `json:"description"`
	Active      bool                          `json:"active"`
	FieldSchema database.JSON[map[string]any] `json:"field_schema"`
}

type CustomField struct {
	database.Base
	TypeID   uuid.UUID               `gorm:"type:uuid" json:"type_id"`
	Key      string                  `json:"key"`
	Label    string                  `json:"label"`
	Kind     string                  `json:"kind"`
	Required bool                    `json:"required"`
	Options  database.JSON[[]string] `json:"options"`
	Position int                     `json:"position"`
}

type ChecklistItem struct {
	database.Base
	TypeID   uuid.UUID `gorm:"type:uuid" json:"type_id"`
	Key      string    `json:"key"`
	Label    string    `json:"label"`
	Kind     string    `json:"kind"`
	Required bool      `json:"required"`
	Position int       `json:"position"`
}

// EquipmentTypeView is a type with its fields and checklist, in order.
type EquipmentTypeView struct {
	EquipmentType
	Fields    []CustomField
	Checklist []ChecklistItem
}

type Equipment struct {
	database.Base
	CustomerID   uuid.UUID                     `gorm:"type:uuid" json:"customer_id"`
	TypeID       uuid.UUID                     `gorm:"type:uuid" json:"type_id"`
	Brand        *string                       `json:"brand"`
	Model        *string                       `json:"model"`
	SerialNumber *string                       `json:"serial_number"`
	Attributes   database.JSON[map[string]any] `json:"attributes"`
	Notes        *string                       `json:"notes"`
}

func (Equipment) TableName() string { return "equipment" }

// CatalogService is a service offered by the shop (table services).
type CatalogService struct {
	database.Base
	Name              string          `json:"name"`
	Description       *string         `json:"description"`
	PriceCents        int64           `json:"price_cents"`
	EstimatedMinutes  *int            `json:"estimated_minutes"`
	WarrantyDays      int             `json:"warranty_days"`
	CommissionPercent decimal.Decimal `json:"commission_percent"`
	Active            bool            `json:"active"`
}

func (CatalogService) TableName() string { return "services" }

type Product struct {
	database.Base
	Name         string          `json:"name"`
	Description  *string         `json:"description"`
	SKU          *string         `gorm:"column:sku" json:"sku"`
	Barcode      *string         `json:"barcode"`
	Unit         string          `json:"unit"`
	CostCents    int64           `json:"cost_cents"`
	PriceCents   int64           `json:"price_cents"`
	MinStock     decimal.Decimal `json:"min_stock"`
	WarrantyDays int             `json:"warranty_days"`
	Active       bool            `json:"active"`
}

type Supplier struct {
	database.Base
	Name        string  `json:"name"`
	Document    *string `json:"document"`
	Email       *string `json:"email"`
	Phone       *string `json:"phone"`
	ContactName *string `json:"contact_name"`
	Notes       *string `json:"notes"`
	Active      bool    `json:"active"`
}
