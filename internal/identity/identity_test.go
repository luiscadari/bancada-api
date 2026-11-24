package identity_test

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luiscadari/bancada-api/internal/apptest"
	"github.com/luiscadari/bancada-api/internal/identity"
	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/database"
)

var env *apptest.Env

func TestMain(m *testing.M) { apptest.Main(m, &env) }

func anon() apptest.Client { return apptest.Client{Env: env} }

func TestSignupCreatesTrialAccountWithDefaults(t *testing.T) {
	tn := apptest.Signup(t, env)

	me := tn.Do(t, http.MethodGet, "/v1/me", nil)
	require.Equal(t, http.StatusOK, me.Status, string(me.Body))
	body := me.JSON(t)
	account := body["account"].(map[string]any)
	assert.Equal(t, "trial", account["status"])
	assert.NotNil(t, account["trial_ends_at"])
	user := body["user"].(map[string]any)
	grants := user["grants"].([]any)
	require.Len(t, grants, 1)
	assert.Equal(t, "owner", grants[0].(map[string]any)["role"])
	assert.Nil(t, grants[0].(map[string]any)["branch_id"])

	branch := tn.Do(t, http.MethodGet, "/v1/branches/"+tn.BranchID, nil).JSON(t)
	settings := branch["settings"].(map[string]any)
	assert.EqualValues(t, 7, settings["quote_validity_days"])
	assert.EqualValues(t, 90, settings["default_warranty_days"])
	assert.EqualValues(t, 1095, settings["abandonment_days"])
	assert.Equal(t, "America/Sao_Paulo", branch["timezone"])

	// RN-03: the signup was audited under the new account.
	var n int64
	require.NoError(t, database.TenantTx(context.Background(), env.App.AppDB, uuid.MustParse(tn.AccountID),
		func(ctx context.Context) error {
			return database.Conn(ctx).Raw("SELECT count(*) FROM audit_log WHERE entity IN ('accounts','branches','users')").Scan(&n).Error
		}))
	assert.GreaterOrEqual(t, n, int64(3))
}

func TestSignupRejectsDuplicateEmailAndInvalidInput(t *testing.T) {
	tn := apptest.Signup(t, env)
	res := anon().Do(t, http.MethodPost, "/v1/auth/signup", map[string]any{
		"account": map[string]any{"trade_name": "Outra"},
		"branch":  map[string]any{"name": "Matriz", "os_prefix": "OUT"},
		"owner":   map[string]any{"name": "X", "email": tn.Email, "password": "outra-senha-123"},
	})
	assert.Equal(t, http.StatusConflict, res.Status)
	assert.Equal(t, "email_taken", res.Code(t))

	res = anon().Do(t, http.MethodPost, "/v1/auth/signup", map[string]any{
		"account": map[string]any{"trade_name": "Outra"},
		"branch":  map[string]any{"name": "Matriz", "os_prefix": "minusculo"},
		"owner":   map[string]any{"name": "X", "email": "x" + apptest.Unique() + "@example.com", "password": "curta"},
	})
	assert.Equal(t, http.StatusBadRequest, res.Status)
	assert.Equal(t, "validation_failed", res.Code(t))

	res = anon().Do(t, http.MethodPost, "/v1/auth/signup", map[string]any{
		"account": map[string]any{"trade_name": "Outra"},
		"branch":  map[string]any{"name": "Matriz", "os_prefix": "ABC", "timezone": "Marte/Base"},
		"owner":   map[string]any{"name": "X", "email": "x" + apptest.Unique() + "@example.com", "password": "senha-forte-123"},
	})
	assert.Equal(t, http.StatusBadRequest, res.Status)
}

func TestLoginAndRateLimit(t *testing.T) {
	tn := apptest.Signup(t, env)
	c := apptest.Client{Env: env, IP: "10.0.0.1"}

	res := c.Do(t, http.MethodPost, "/v1/auth/login", map[string]any{"email": tn.Email, "password": "errada"})
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Equal(t, "invalid_credentials", res.Code(t))

	res = c.Do(t, http.MethodPost, "/v1/auth/login", map[string]any{"email": "NAO" + tn.Email, "password": "x"})
	assert.Equal(t, http.StatusUnauthorized, res.Status)

	// Email is case-insensitive.
	res = c.Do(t, http.MethodPost, "/v1/auth/login", map[string]any{"email": "OWNER" + tn.Email[5:], "password": tn.Password})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.NotEmpty(t, res.JSON(t)["access_token"])

	limited := apptest.Client{Env: env, IP: "10.0.0.2"}
	for i := 0; i < 10; i++ {
		limited.Do(t, http.MethodPost, "/v1/auth/login", map[string]any{"email": tn.Email, "password": "errada"})
	}
	res = limited.Do(t, http.MethodPost, "/v1/auth/login", map[string]any{"email": tn.Email, "password": tn.Password})
	assert.Equal(t, http.StatusTooManyRequests, res.Status)
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	tn := apptest.Signup(t, env)

	first := anon().Do(t, http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": tn.RefreshToken})
	require.Equal(t, http.StatusOK, first.Status, string(first.Body))
	rotated := first.JSON(t)["refresh_token"].(string)
	assert.NotEqual(t, tn.RefreshToken, rotated)

	// Reusing the old token is treated as theft: the whole family is revoked.
	reuse := anon().Do(t, http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": tn.RefreshToken})
	assert.Equal(t, http.StatusUnauthorized, reuse.Status)
	after := anon().Do(t, http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": rotated})
	assert.Equal(t, http.StatusUnauthorized, after.Status)

	unknown := anon().Do(t, http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": "nope"})
	assert.Equal(t, http.StatusUnauthorized, unknown.Status)
}

func TestLogoutRevokesSession(t *testing.T) {
	tn := apptest.Signup(t, env)
	res := anon().Do(t, http.MethodPost, "/v1/auth/logout", map[string]any{"refresh_token": tn.RefreshToken})
	assert.Equal(t, http.StatusNoContent, res.Status)
	res = anon().Do(t, http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": tn.RefreshToken})
	assert.Equal(t, http.StatusUnauthorized, res.Status)
}

func TestAuthenticationRequired(t *testing.T) {
	res := anon().Do(t, http.MethodGet, "/v1/me", nil)
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Equal(t, "unauthenticated", res.Code(t))

	res = apptest.Client{Env: env, Token: "garbage"}.Do(t, http.MethodGet, "/v1/me", nil)
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Equal(t, "invalid_token", res.Code(t))
}

// ADR-01: an account never sees another account's data.
func TestTenantIsolation(t *testing.T) {
	a := apptest.Signup(t, env)
	b := apptest.Signup(t, env)

	res := b.Do(t, http.MethodGet, "/v1/branches/"+a.BranchID, nil)
	assert.Equal(t, http.StatusNotFound, res.Status)
	res = b.Do(t, http.MethodGet, "/v1/users/"+a.UserID, nil)
	assert.Equal(t, http.StatusNotFound, res.Status)
	res = b.Do(t, http.MethodPatch, "/v1/branches/"+a.BranchID, map[string]any{"name": "Invadida"})
	assert.Equal(t, http.StatusNotFound, res.Status)

	// X-Branch-ID of another account is rejected.
	other := b.Client
	other.BranchID = a.BranchID
	res = other.Do(t, http.MethodGet, "/v1/me", nil)
	assert.Equal(t, http.StatusForbidden, res.Status)
	assert.Equal(t, "branch_forbidden", res.Code(t))

	users := b.Do(t, http.MethodGet, "/v1/users", nil).JSON(t)["items"].([]any)
	require.Len(t, users, 1)
	assert.Equal(t, b.UserID, users[0].(map[string]any)["id"])

	// At the database level, the tenant setting filters every identity table.
	for _, table := range []string{"accounts", "branches", "users", "user_branch_roles", "refresh_tokens", "audit_log"} {
		var foreign int64
		require.NoError(t, database.TenantTx(context.Background(), env.App.AppDB, uuid.MustParse(b.AccountID),
			func(ctx context.Context) error {
				col := "account_id"
				if table == "accounts" {
					col = "id"
				}
				return database.Conn(ctx).Raw("SELECT count(*) FROM "+table+" WHERE "+col+" = ?", a.AccountID).Scan(&foreign).Error
			}))
		assert.Zero(t, foreign, table)
	}
}

func TestInvitationAndRolePermissions(t *testing.T) {
	tn := apptest.Signup(t, env)
	attendant, attendantID := tn.Invite(t, actor.RoleAttendant)

	// The attendant sees only their branch and cannot manage users.
	res := attendant.Do(t, http.MethodGet, "/v1/branches", nil)
	require.Equal(t, http.StatusOK, res.Status)
	assert.Len(t, res.JSON(t)["items"].([]any), 1)
	res = attendant.Do(t, http.MethodGet, "/v1/users", nil)
	assert.Equal(t, http.StatusForbidden, res.Status)
	res = attendant.Do(t, http.MethodPut, "/v1/branches/"+tn.BranchID+"/settings", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, res.Status) // invalid body is rejected before authorization
	res = attendant.Do(t, http.MethodPatch, "/v1/account", map[string]any{"trade_name": "Minha"})
	assert.Equal(t, http.StatusForbidden, res.Status)

	// A branch the attendant has no role in is rejected.
	other := tn.Do(t, http.MethodPost, "/v1/branches", map[string]any{"name": "Filial 2", "os_prefix": "FL2"})
	require.Equal(t, http.StatusCreated, other.Status, string(other.Body))
	scoped := attendant
	scoped.BranchID = other.JSON(t)["id"].(string)
	res = scoped.Do(t, http.MethodGet, "/v1/me", nil)
	assert.Equal(t, http.StatusForbidden, res.Status)

	// The invitation is single-use; inviting the same email again conflicts.
	user := tn.Do(t, http.MethodGet, "/v1/users/"+attendantID, nil).JSON(t)
	assert.Equal(t, "active", user["status"])
	res = tn.Do(t, http.MethodPost, "/v1/users/invitations", map[string]any{
		"name": "Dup", "email": user["email"], "grants": []map[string]any{{"branch_id": tn.BranchID, "role": "technician"}},
	})
	assert.Equal(t, http.StatusConflict, res.Status)

	// Grants are validated: owners are account-wide, other roles need a branch.
	res = tn.Do(t, http.MethodPut, "/v1/users/"+attendantID+"/grants", map[string]any{
		"grants": []map[string]any{{"branch_id": tn.BranchID, "role": "owner"}},
	})
	assert.Equal(t, http.StatusBadRequest, res.Status)
	res = tn.Do(t, http.MethodPut, "/v1/users/"+attendantID+"/grants", map[string]any{
		"grants": []map[string]any{{"branch_id": nil, "role": "technician"}},
	})
	assert.Equal(t, http.StatusBadRequest, res.Status)
	res = tn.Do(t, http.MethodPut, "/v1/users/"+attendantID+"/grants", map[string]any{
		"grants": []map[string]any{
			{"branch_id": tn.BranchID, "role": "technician"},
			{"branch_id": tn.BranchID, "role": "attendant"},
			{"branch_id": tn.BranchID, "role": "attendant"},
		},
	})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Len(t, res.JSON(t)["grants"].([]any), 2)
}

func TestDisableUserEndsAccess(t *testing.T) {
	tn := apptest.Signup(t, env)
	tech, techID := tn.Invite(t, actor.RoleTechnician)
	require.Equal(t, http.StatusOK, tech.Do(t, http.MethodGet, "/v1/me", nil).Status)

	res := tn.Do(t, http.MethodPost, "/v1/users/"+techID+"/disable", nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, "disabled", res.JSON(t)["status"])

	// The still-valid access token is rejected immediately.
	res = tech.Do(t, http.MethodGet, "/v1/me", nil)
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Equal(t, "user_disabled", res.Code(t))

	res = tn.Do(t, http.MethodPost, "/v1/users/"+techID+"/enable", nil)
	assert.Equal(t, "active", res.JSON(t)["status"])
	assert.Equal(t, http.StatusOK, tech.Do(t, http.MethodGet, "/v1/me", nil).Status)
}

func TestAccountKeepsAnActiveOwner(t *testing.T) {
	tn := apptest.Signup(t, env)

	res := tn.Do(t, http.MethodPost, "/v1/users/"+tn.UserID+"/disable", nil)
	assert.Equal(t, http.StatusUnprocessableEntity, res.Status)
	assert.Equal(t, "cannot_disable_self", res.Code(t))

	res = tn.Do(t, http.MethodPut, "/v1/users/"+tn.UserID+"/grants", map[string]any{
		"grants": []map[string]any{{"branch_id": tn.BranchID, "role": "attendant"}},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, res.Status)
	assert.Equal(t, "last_owner", res.Code(t))

	// With a second owner, the first can step down.
	_, otherID := tn.Invite(t, actor.RoleAttendant)
	res = tn.Do(t, http.MethodPut, "/v1/users/"+otherID+"/grants", map[string]any{
		"grants": []map[string]any{{"branch_id": nil, "role": "owner"}},
	})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	res = tn.Do(t, http.MethodPut, "/v1/users/"+tn.UserID+"/grants", map[string]any{
		"grants": []map[string]any{{"branch_id": tn.BranchID, "role": "attendant"}},
	})
	assert.Equal(t, http.StatusOK, res.Status, string(res.Body))
}

func TestBranchManagement(t *testing.T) {
	tn := apptest.Signup(t, env)
	id := uuid.Must(uuid.NewV7()).String()
	body := map[string]any{"id": id, "name": "Centro", "os_prefix": "CEN", "timezone": "America/Manaus"}

	res := tn.Do(t, http.MethodPost, "/v1/branches", body)
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	assert.Equal(t, id, res.JSON(t)["id"])
	// Repeating the create with the same id is idempotent (offline retries).
	res = tn.Do(t, http.MethodPost, "/v1/branches", body)
	assert.Equal(t, http.StatusCreated, res.Status)
	assert.Len(t, tn.Do(t, http.MethodGet, "/v1/branches", nil).JSON(t)["items"].([]any), 2)

	res = tn.Do(t, http.MethodPost, "/v1/branches", map[string]any{"name": "Outra", "os_prefix": "CEN"})
	assert.Equal(t, http.StatusConflict, res.Status)
	assert.Equal(t, "os_prefix_taken", res.Code(t))

	settings := map[string]any{
		"diagnostic_fee":                  map[string]any{"enabled": true, "amount_cents": 5000, "deduct_on_approval": true},
		"quote_validity_days":             10,
		"default_warranty_days":           120,
		"pickup_reminder_days":            []int{5, 10},
		"abandonment_days":                1095,
		"storage_fee_enabled":             false,
		"allow_negative_stock":            false,
		"technician_can_open_and_deliver": true,
		"attendant_max_discount_percent":  15,
		"terms":                           "Garantia de 120 dias.",
	}
	res = tn.Do(t, http.MethodPut, "/v1/branches/"+id+"/settings", settings)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	got := res.JSON(t)["settings"].(map[string]any)
	assert.EqualValues(t, 5000, got["diagnostic_fee"].(map[string]any)["amount_cents"])
	assert.EqualValues(t, 2, res.JSON(t)["version"])

	res = tn.Do(t, http.MethodPatch, "/v1/branches/"+id, map[string]any{"timezone": "Nowhere/City"})
	assert.Equal(t, http.StatusBadRequest, res.Status)
}

func TestDevicesAndNumberBlocks(t *testing.T) {
	tn := apptest.Signup(t, env)
	deviceID := uuid.Must(uuid.NewV7()).String()
	login := func() apptest.Response {
		return anon().Do(t, http.MethodPost, "/v1/auth/login", map[string]any{
			"email": tn.Email, "password": tn.Password,
			"device": map[string]any{"id": deviceID, "branch_id": tn.BranchID, "name": "Balcão 1", "platform": "windows"},
		})
	}
	res := login()
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, deviceID, res.JSON(t)["device_id"])
	device := apptest.Client{Env: env, Token: res.JSON(t)["access_token"].(string)}
	deviceRefresh := res.JSON(t)["refresh_token"].(string)

	block := device.Do(t, http.MethodPost, "/v1/devices/"+deviceID+"/number-blocks", nil)
	require.Equal(t, http.StatusCreated, block.Status, string(block.Body))
	assert.EqualValues(t, 1, block.JSON(t)["range_start"])
	assert.EqualValues(t, 50, block.JSON(t)["range_end"])
	assert.Equal(t, "MAT-000001", block.JSON(t)["first_number"])

	block = device.Do(t, http.MethodPost, "/v1/devices/"+deviceID+"/number-blocks", map[string]any{"size": 10})
	assert.EqualValues(t, 51, block.JSON(t)["range_start"])
	assert.Equal(t, "MAT-000060", block.JSON(t)["last_number"])

	// Another (non-owner) user cannot reserve for this device.
	tech, _ := tn.Invite(t, actor.RoleTechnician)
	res = tech.Do(t, http.MethodPost, "/v1/devices/"+deviceID+"/number-blocks", nil)
	assert.Equal(t, http.StatusForbidden, res.Status)

	// Revoking the device ends its sessions and blocks new logins from it (ADR-07).
	res = tn.Do(t, http.MethodPost, "/v1/devices/"+deviceID+"/revoke", nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.NotNil(t, res.JSON(t)["revoked_at"])
	assert.Equal(t, http.StatusUnauthorized, device.Do(t, http.MethodGet, "/v1/me", nil).Status)
	res = anon().Do(t, http.MethodPost, "/v1/auth/refresh", map[string]any{"refresh_token": deviceRefresh})
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	res = login()
	assert.Equal(t, http.StatusForbidden, res.Status)
	assert.Equal(t, "device_revoked", res.Code(t))
}

// RN-04: concurrent allocations never hand out the same number.
func TestNumberAllocationIsAtomic(t *testing.T) {
	tn := apptest.Signup(t, env)
	accountID, branchID := uuid.MustParse(tn.AccountID), uuid.MustParse(tn.BranchID)
	a := &actor.Actor{AccountID: accountID, UserID: uuid.MustParse(tn.UserID),
		Grants: []actor.Grant{{Role: actor.RoleOwner}}}

	const workers = 20
	var mu sync.Mutex
	var got []int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := database.TenantTx(context.Background(), env.App.AppDB, accountID, func(ctx context.Context) error {
				_, n, err := env.App.Identity.NextNumber(actor.With(ctx, a), branchID)
				if err == nil {
					mu.Lock()
					got = append(got, n)
					mu.Unlock()
				}
				return err
			})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	require.Len(t, got, workers)
	for i, n := range got {
		assert.EqualValues(t, i+1, n)
	}
	assert.Equal(t, "MAT-000123", identity.FormatNumber("MAT", 123))
}

func TestAccountProfileAndDeviceList(t *testing.T) {
	tn := apptest.Signup(t, env)

	res := tn.Do(t, http.MethodPatch, "/v1/account", map[string]any{"trade_name": "Bancada Informática", "document": "12.345.678/0001-90"})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, "12345678000190", res.JSON(t)["document"])
	assert.EqualValues(t, 2, res.JSON(t)["version"])
	assert.Equal(t, "Bancada Informática", tn.Do(t, http.MethodGet, "/v1/account", nil).JSON(t)["trade_name"])

	res = tn.Do(t, http.MethodPatch, "/v1/account", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, res.Status, "an empty patch is rejected by the contract")

	deviceID := uuid.Must(uuid.NewV7()).String()
	res = anon().Do(t, http.MethodPost, "/v1/auth/login", map[string]any{
		"email": tn.Email, "password": tn.Password,
		"device": map[string]any{"id": deviceID, "branch_id": tn.BranchID, "name": "Bancada 2", "platform": "linux"},
	})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	devices := tn.Do(t, http.MethodGet, "/v1/devices", nil).JSON(t)["items"].([]any)
	require.Len(t, devices, 1)
	assert.Equal(t, "Bancada 2", devices[0].(map[string]any)["name"])
	assert.Equal(t, "linux", devices[0].(map[string]any)["platform"])

	// A device id already used by another account cannot be taken over.
	other := apptest.Signup(t, env)
	res = anon().Do(t, http.MethodPost, "/v1/auth/login", map[string]any{
		"email": other.Email, "password": other.Password,
		"device": map[string]any{"id": deviceID, "branch_id": other.BranchID, "name": "Intruso", "platform": "linux"},
	})
	assert.Equal(t, http.StatusConflict, res.Status, string(res.Body))
}
