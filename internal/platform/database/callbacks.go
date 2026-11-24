package database

import (
	"reflect"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/ids"
)

// RegisterCallbacks installs the stamping callbacks: IDs, account and authorship on
// create, and the updating user on update (RN-03).
func RegisterCallbacks(db *gorm.DB) error {
	if err := db.Callback().Create().Before("gorm:create").Register("bancada:stamp_create", stampCreate); err != nil {
		return err
	}
	return db.Callback().Update().Before("gorm:update").Register("bancada:stamp_update", stampUpdate)
}

func stampCreate(db *gorm.DB) {
	if db.Statement.Schema == nil {
		return
	}
	a, _ := actor.From(db.Statement.Context)
	eachRecord(db, func(rv reflect.Value) {
		setIfZero(db, rv, "ID", func() any { return ids.New() })
		if a == nil {
			return
		}
		setIfZero(db, rv, "AccountID", func() any { return a.AccountID })
		setIfZero(db, rv, "CreatedBy", func() any { return ids.Ptr(a.UserID) })
		setIfZero(db, rv, "UpdatedBy", func() any { return ids.Ptr(a.UserID) })
		if a.DeviceID != nil {
			setIfZero(db, rv, "OriginDeviceID", func() any { return a.DeviceID })
		}
	})
}

func stampUpdate(db *gorm.DB) {
	if db.Statement.Schema == nil {
		return
	}
	a, ok := actor.From(db.Statement.Context)
	if !ok || a.UserID == uuid.Nil {
		return
	}
	if db.Statement.Schema.LookUpField("UpdatedBy") != nil {
		db.Statement.SetColumn("UpdatedBy", ids.Ptr(a.UserID), true)
	}
}

func eachRecord(db *gorm.DB, fn func(reflect.Value)) {
	rv := db.Statement.ReflectValue
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			fn(reflect.Indirect(rv.Index(i)))
		}
	case reflect.Struct:
		fn(rv)
	}
}

func setIfZero(db *gorm.DB, rv reflect.Value, name string, value func() any) {
	field := db.Statement.Schema.LookUpField(name)
	if field == nil {
		return
	}
	if _, zero := field.ValueOf(db.Statement.Context, rv); !zero {
		return
	}
	if err := field.Set(db.Statement.Context, rv, value()); err != nil {
		_ = db.AddError(err)
	}
}

// PrimaryKey returns the primary key value of the record in rv, if the schema has one.
func PrimaryKey(db *gorm.DB, s *schema.Schema, rv reflect.Value) any {
	if s == nil || s.PrioritizedPrimaryField == nil || rv.Kind() != reflect.Struct {
		return nil
	}
	v, zero := s.PrioritizedPrimaryField.ValueOf(db.Statement.Context, rv)
	if zero {
		return nil
	}
	return v
}
