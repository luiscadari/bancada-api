// Package app wires configuration, database, modules and HTTP routing.
package app

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/luiscadari/bancada-api/internal/api"
	"github.com/luiscadari/bancada-api/internal/api/gen"
	"github.com/luiscadari/bancada-api/internal/platform/audit"
	"github.com/luiscadari/bancada-api/internal/platform/auth"
	"github.com/luiscadari/bancada-api/internal/platform/config"
	"github.com/luiscadari/bancada-api/internal/platform/database"
	"github.com/luiscadari/bancada-api/internal/platform/httpx"
)

// App is the assembled HTTP application.
type App struct {
	Router   *gin.Engine
	AppDB    *gorm.DB
	SystemDB *gorm.DB
}

// New builds the application from cfg.
func New(cfg config.Config) (*App, error) {
	appDB, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("app database: %w", err)
	}
	if err := audit.Register(appDB); err != nil {
		return nil, err
	}
	systemDB, err := database.Open(cfg.SystemDatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("system database: %w", err)
	}

	spec, err := gen.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("load contract: %w", err)
	}
	policies, err := api.BuildPolicies(spec)
	if err != nil {
		return nil, err
	}
	issuer := auth.NewIssuer(cfg.JWTSecret, cfg.AccessTokenTTL)

	server := &api.Server{
		Health: api.Health{DB: appDB},
	}

	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.ContextWithFallback = true // handlers read the tenant transaction from the request context
	r.Use(
		httpx.RequestIDMiddleware(),
		httpx.LoggerMiddleware(),
		httpx.RecoveryMiddleware(),
		httpx.CORSMiddleware(cfg.CORSOrigins),
		httpx.ValidatorMiddleware(spec),
	)
	strict := gen.NewStrictHandlerWithOptions(server,
		[]gen.StrictMiddlewareFunc{httpx.TenancyMiddleware(appDB, issuer, nil, policies)},
		gen.StrictGinServerOptions{
			RequestErrorHandlerFunc:  httpx.RequestError,
			HandlerErrorFunc:         httpx.HandlerError,
			ResponseErrorHandlerFunc: httpx.ResponseError,
		})
	gen.RegisterHandlersWithOptions(r, strict, gen.GinServerOptions{
		ErrorHandler: func(c *gin.Context, err error, _ int) { httpx.RequestError(c, err) },
	})

	return &App{Router: r, AppDB: appDB, SystemDB: systemDB}, nil
}

// Close releases the database pools.
func (a *App) Close() {
	for _, db := range []*gorm.DB{a.AppDB, a.SystemDB} {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}
