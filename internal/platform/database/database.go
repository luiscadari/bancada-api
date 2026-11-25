// Package database opens GORM connections and carries the per-request tenant
// transaction through the context.
//
// Row-level security (ADR-01): every business table has a policy comparing its
// account_id with current_setting('app.account_id'). The API connects as a role that
// cannot bypass RLS and sets app.account_id with set_config(..., true) at the start of
// each request transaction, so the setting never leaks to other requests on the pool.
package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/luiscadari/bancada-api/internal/platform/reqctx"
)

// Open connects to PostgreSQL and registers the Bancada callbacks.
func Open(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		NowFunc:                func() time.Time { return time.Now().UTC() },
		Logger:                 logger.Discard,
		SkipDefaultTransaction: true, // every request already runs inside one transaction
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := RegisterCallbacks(db); err != nil {
		return nil, err
	}
	return db, nil
}

type txKey struct{}

// WithTx stores tx in ctx.
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// Conn returns the tenant transaction stored in ctx, bound to ctx.
// It panics when called outside a tenant transaction: that is a programming error
// and running the query without the tenant setting would return no rows.
func Conn(ctx context.Context) *gorm.DB {
	ctx = reqctx.Unwrap(ctx)
	tx, ok := ctx.Value(txKey{}).(*gorm.DB)
	if !ok || tx == nil {
		panic("database.Conn: no tenant transaction in context")
	}
	return tx.WithContext(ctx)
}

// HasTx reports whether ctx carries a tenant transaction.
func HasTx(ctx context.Context) bool {
	tx, ok := reqctx.Unwrap(ctx).Value(txKey{}).(*gorm.DB)
	return ok && tx != nil
}

// SetTenant scopes tx to accountID for the rest of the transaction.
func SetTenant(tx *gorm.DB, accountID uuid.UUID) error {
	return tx.Exec("SELECT set_config('app.account_id', ?, true)", accountID.String()).Error
}

// TenantTx runs fn inside a transaction scoped to accountID. The transaction is
// available to fn through Conn(ctx). It commits when fn returns nil.
func TenantTx(ctx context.Context, db *gorm.DB, accountID uuid.UUID, fn func(ctx context.Context) error) (err error) {
	ctx = reqctx.Unwrap(ctx)
	tx := db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return fmt.Errorf("begin: %w", tx.Error)
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
		if err != nil {
			if rbErr := tx.Rollback().Error; rbErr != nil && !errors.Is(rbErr, gorm.ErrInvalidTransaction) {
				slog.ErrorContext(ctx, "rollback failed", "error", rbErr)
			}
		}
	}()
	if err = SetTenant(tx, accountID); err != nil {
		return fmt.Errorf("set tenant: %w", err)
	}
	if err = fn(WithTx(ctx, tx)); err != nil {
		return err
	}
	if err = tx.Commit().Error; err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
