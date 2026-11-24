package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luiscadari/bancada-api/internal/app"
	"github.com/luiscadari/bancada-api/internal/apptest"
	"github.com/luiscadari/bancada-api/internal/platform/testdb"
)

func TestHealthAndMigrations(t *testing.T) {
	ctx := context.Background()
	env, err := testdb.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop(ctx) })

	a, err := app.New(apptest.Config(env))
	require.NoError(t, err)
	t.Cleanup(a.Close)

	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		a.Router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, rec.Code, path)
	}

	rec := httptest.NewRecorder()
	a.Router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	var p map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "route_not_found", p["code"])

	// The API role cannot see tenant rows without app.account_id, and cannot alter the audit log.
	var n int64
	require.NoError(t, a.AppDB.Raw("SELECT count(*) FROM audit_log").Scan(&n).Error)
	assert.Zero(t, n)
	err = a.AppDB.Exec("DELETE FROM audit_log").Error
	assert.ErrorContains(t, err, "permission denied")
}
