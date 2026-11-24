// Package api composes the module handlers into the server generated from the OpenAPI
// contract.
package api

import (
	"context"

	"gorm.io/gorm"

	"github.com/luiscadari/bancada-api/internal/api/gen"
)

// Server implements gen.StrictServerInterface by embedding the module handlers.
type Server struct {
	Health
}

var _ gen.StrictServerInterface = (*Server)(nil)

// Health implements the liveness and readiness probes.
type Health struct {
	DB *gorm.DB
}

func (h Health) GetHealthz(context.Context, gen.GetHealthzRequestObject) (gen.GetHealthzResponseObject, error) {
	return gen.GetHealthz200JSONResponse{Status: "ok"}, nil
}

func (h Health) GetReadyz(ctx context.Context, _ gen.GetReadyzRequestObject) (gen.GetReadyzResponseObject, error) {
	sqlDB, err := h.DB.DB()
	if err == nil {
		err = sqlDB.PingContext(ctx)
	}
	if err != nil {
		return gen.GetReadyzdefaultApplicationProblemPlusJSONResponse{
			StatusCode: 503,
			Body: gen.Problem{Type: "about:blank", Title: "Service Unavailable", Status: 503,
				Code: "database_unavailable", Detail: ptr("Banco de dados indisponível.")},
		}, nil
	}
	return gen.GetReadyz200JSONResponse{Status: "ok"}, nil
}

func ptr[T any](v T) *T { return &v }
