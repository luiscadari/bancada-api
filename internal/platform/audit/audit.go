// Package audit records who did what, when and from which device (RN-03).
//
// A GORM callback writes one audit_log row for every create, update and delete issued
// through GORM, in the same transaction as the change. Services can also call Record
// for actions that are not row changes (for example, revealing a device password).
package audit

import (
	"context"
	"encoding/json"
	"reflect"

	"gorm.io/gorm"

	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/database"
	"github.com/luiscadari/bancada-api/internal/platform/ids"
)

// skipTables are never audited: the log itself and secret-bearing tables.
var skipTables = map[string]bool{
	"audit_log":      true,
	"refresh_tokens": true,
}

// Register installs the audit callbacks on db.
func Register(db *gorm.DB) error {
	cb := db.Callback()
	if err := cb.Create().After("gorm:create").Register("bancada:audit_create", logChange("create")); err != nil {
		return err
	}
	if err := cb.Update().After("gorm:update").Register("bancada:audit_update", logChange("update")); err != nil {
		return err
	}
	return cb.Delete().After("gorm:delete").Register("bancada:audit_delete", logChange("delete"))
}

func logChange(action string) func(*gorm.DB) {
	return func(db *gorm.DB) {
		st := db.Statement
		if db.Error != nil || db.RowsAffected == 0 || st.Schema == nil || skipTables[st.Table] {
			return
		}
		var changes any
		if action == "update" {
			changes = st.Dest
		}
		record := func(rv reflect.Value) {
			c := changes
			if action == "create" {
				c = rv.Interface()
			}
			insert(db, st.Table, database.PrimaryKey(db, st.Schema, rv), action, c)
		}
		rv := st.ReflectValue
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			for i := 0; i < rv.Len(); i++ {
				record(reflect.Indirect(rv.Index(i)))
			}
		case reflect.Struct:
			record(rv)
		default:
			insert(db, st.Table, nil, action, changes)
		}
	}
}

func insert(db *gorm.DB, entity string, entityID any, action string, changes any) {
	a, ok := actor.From(db.Statement.Context)
	if !ok {
		return // system operations outside a request (seeds, migrations) are not audited
	}
	payload, err := json.Marshal(changes)
	if err != nil || string(payload) == "null" {
		payload = []byte("{}")
	}
	err = db.Session(&gorm.Session{NewDB: true}).Exec(
		`INSERT INTO audit_log (id, account_id, branch_id, user_id, device_id, entity, entity_id, action, changes, request_id, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, '')::inet)`,
		ids.New(), a.AccountID, a.BranchID, ids.Ptr(a.UserID), a.DeviceID, entity, entityID, action,
		string(payload), a.RequestID, a.IP,
	).Error
	if err != nil {
		_ = db.AddError(err)
	}
}

// Record writes an audit entry for an action that is not a row change.
func Record(ctx context.Context, entity string, entityID any, action string, details any) error {
	db := database.Conn(ctx)
	insert(db, entity, entityID, action, details)
	return db.Error
}
