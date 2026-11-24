// Package testdb starts a disposable PostgreSQL with the Bancada roles and migrations,
// so tests exercise the real row-level security policies.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/luiscadari/bancada-api/internal/platform/database"
)

const image = "postgres:18-alpine"

// Env holds the connection strings of a running test database.
type Env struct {
	OwnerDSN  string
	AppDSN    string
	SystemDSN string
	container *postgres.PostgresContainer
}

// Start runs PostgreSQL, creates the roles and applies every migration.
func Start(ctx context.Context) (*Env, error) {
	_, file, _, _ := runtime.Caller(0)
	initScript := filepath.Join(filepath.Dir(file), "..", "..", "..", "db", "init", "00-roles.sh")

	c, err := postgres.Run(ctx, image,
		postgres.WithDatabase("bancada"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		postgres.WithInitScripts(initScript),
		testcontainers.WithEnv(map[string]string{
			"BANCADA_OWNER_PASSWORD":  "owner",
			"BANCADA_APP_PASSWORD":    "app",
			"BANCADA_SYSTEM_PASSWORD": "system",
		}),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres: %w", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		return nil, err
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return nil, err
	}
	dsn := func(user, pass string) string {
		u := url.URL{Scheme: "postgres", User: url.UserPassword(user, pass), Host: host + ":" + port.Port(),
			Path: "/bancada", RawQuery: "sslmode=disable"}
		return u.String()
	}
	env := &Env{
		OwnerDSN:  dsn("bancada_owner", "owner"),
		AppDSN:    dsn("bancada_app", "app"),
		SystemDSN: dsn("bancada_system", "system"),
		container: c,
	}
	if _, err := database.Migrate(ctx, env.OwnerDSN, "up"); err != nil {
		_ = env.Stop(ctx)
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return env, nil
}

// Stop terminates the container.
func (e *Env) Stop(ctx context.Context) error {
	return testcontainers.TerminateContainer(e.container)
}
