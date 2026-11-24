// Package config loads runtime configuration from environment variables.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Env      string `env:"APP_ENV" envDefault:"development"`
	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`

	// DatabaseURL connects as bancada_app (subject to row-level security).
	DatabaseURL string `env:"DATABASE_URL"`
	// SystemDatabaseURL connects as bancada_system (BYPASSRLS). Used only for the
	// few cross-tenant lookups that happen before a tenant is known (login, refresh, invite).
	SystemDatabaseURL string `env:"SYSTEM_DATABASE_URL"`
	// MigrationDatabaseURL connects as bancada_owner, which owns the schema.
	MigrationDatabaseURL string `env:"MIGRATION_DATABASE_URL"`

	JWTSecret       string        `env:"JWT_SECRET"`
	AccessTokenTTL  time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"720h"`
	InvitationTTL   time.Duration `env:"INVITATION_TTL" envDefault:"168h"`

	// DevicePasswordKey is a base64-encoded 32-byte AES key (RN-31).
	DevicePasswordKey string `env:"DEVICE_PASSWORD_KEY"`

	CORSOrigins  []string `env:"CORS_ORIGINS" envSeparator:","`
	PublicAppURL string   `env:"PUBLIC_APP_URL" envDefault:"http://localhost:3000"`
}

func Load() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return c, fmt.Errorf("parse env: %w", err)
	}
	return c, nil
}

// ValidateServe checks the settings required by the HTTP server.
func (c Config) ValidateServe() error {
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if c.SystemDatabaseURL == "" {
		errs = append(errs, errors.New("SYSTEM_DATABASE_URL is required"))
	}
	if len(c.JWTSecret) < 32 {
		errs = append(errs, errors.New("JWT_SECRET must have at least 32 characters"))
	}
	if _, err := c.DevicePasswordKeyBytes(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// ValidateMigrate checks the settings required by the migrate command.
func (c Config) ValidateMigrate() error {
	if c.MigrationDatabaseURL == "" {
		return errors.New("MIGRATION_DATABASE_URL is required")
	}
	return nil
}

func (c Config) DevicePasswordKeyBytes() ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(c.DevicePasswordKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("DEVICE_PASSWORD_KEY must be a base64-encoded 32-byte key")
	}
	return key, nil
}
