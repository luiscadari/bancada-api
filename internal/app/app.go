// Package app wires configuration, database, modules and HTTP routing.
package app

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/luiscadari/bancada-api/internal/api"
	"github.com/luiscadari/bancada-api/internal/api/gen"
	"github.com/luiscadari/bancada-api/internal/finance"
	"github.com/luiscadari/bancada-api/internal/identity"
	"github.com/luiscadari/bancada-api/internal/inventory"
	"github.com/luiscadari/bancada-api/internal/platform/audit"
	"github.com/luiscadari/bancada-api/internal/platform/auth"
	"github.com/luiscadari/bancada-api/internal/platform/config"
	"github.com/luiscadari/bancada-api/internal/platform/crypto"
	"github.com/luiscadari/bancada-api/internal/platform/database"
	"github.com/luiscadari/bancada-api/internal/platform/httpx"
	"github.com/luiscadari/bancada-api/internal/registry"
	"github.com/luiscadari/bancada-api/internal/serviceorder"
)

// App is the assembled HTTP application.
type App struct {
	Router   *gin.Engine
	AppDB    *gorm.DB
	SystemDB *gorm.DB
	Identity *identity.Service
	Registry *registry.Service
	Orders   *serviceorder.Service
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

	identitySvc := identity.NewService(appDB, systemDB, issuer, identity.Options{
		RefreshTTL:    cfg.RefreshTokenTTL,
		InvitationTTL: cfg.InvitationTTL,
		PublicAppURL:  cfg.PublicAppURL,
	})

	registrySvc := registry.NewService()
	identitySvc.OnAccountCreated(registrySvc.OnAccountCreated)

	key, err := cfg.DevicePasswordKeyBytes()
	if err != nil {
		return nil, err
	}
	sealer, err := crypto.NewSealer(key)
	if err != nil {
		return nil, err
	}
	// Stock and finance are not built yet: their stubs keep the order flow complete.
	ordersSvc := serviceorder.NewService(registrySvc, identitySvc, inventory.NoOp{}, finance.NoPayments{}, sealer)

	server := &api.Server{
		Health:              api.Health{DB: appDB},
		IdentityHandler:     identity.NewHandler(identitySvc),
		RegistryHandler:     registry.NewHandler(registrySvc),
		ServiceOrderHandler: serviceorder.NewHandler(ordersSvc),
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
		[]gen.StrictMiddlewareFunc{httpx.TenancyMiddleware(appDB, issuer, identitySvc, policies)},
		gen.StrictGinServerOptions{
			RequestErrorHandlerFunc:  httpx.RequestError,
			HandlerErrorFunc:         httpx.HandlerError,
			ResponseErrorHandlerFunc: httpx.ResponseError,
		})
	gen.RegisterHandlersWithOptions(r, strict, gen.GinServerOptions{
		ErrorHandler: func(c *gin.Context, err error, _ int) { httpx.RequestError(c, err) },
	})

	return &App{Router: r, AppDB: appDB, SystemDB: systemDB, Identity: identitySvc, Registry: registrySvc, Orders: ordersSvc}, nil
}

// Close releases the database pools.
func (a *App) Close() {
	for _, db := range []*gorm.DB{a.AppDB, a.SystemDB} {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}
