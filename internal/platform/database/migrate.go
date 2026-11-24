package database

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for database/sql
	"github.com/pressly/goose/v3"

	"github.com/luiscadari/bancada-api/migrations"
)

// Migrate runs the goose command ("up", "down", "status") against dsn, which must
// connect as the schema owner.
func Migrate(ctx context.Context, dsn, command string) ([]*goose.MigrationResult, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return nil, err
	}
	switch command {
	case "up":
		return provider.Up(ctx)
	case "down":
		r, err := provider.Down(ctx)
		return []*goose.MigrationResult{r}, err
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range statuses {
			fmt.Printf("%-8s %s\n", s.State, s.Source.Path)
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown migrate command %q (use up, down or status)", command)
	}
}
