package database

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Base holds the columns every business table has (requirements, section 9):
// a client-generatable UUIDv7, the owning account, timestamps with logical deletion,
// a version counter for synchronization and the authoring user and device.
//
// ID, AccountID, CreatedBy, UpdatedBy and OriginDeviceID are filled by the callbacks
// in callbacks.go; Version and UpdatedAt are maintained by the touch_row trigger.
type Base struct {
	ID             uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	AccountID      uuid.UUID      `gorm:"type:uuid;not null" json:"account_id"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `json:"deleted_at,omitempty"`
	Version        int64          `gorm:"not null;default:1" json:"version"`
	OriginDeviceID *uuid.UUID     `gorm:"type:uuid" json:"origin_device_id,omitempty"`
	CreatedBy      *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	UpdatedBy      *uuid.UUID     `gorm:"type:uuid" json:"updated_by,omitempty"`
}
