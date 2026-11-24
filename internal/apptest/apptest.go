// Package apptest runs the full HTTP application against a disposable PostgreSQL for
// end-to-end tests.
package apptest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/luiscadari/bancada-api/internal/app"
	"github.com/luiscadari/bancada-api/internal/platform/config"
	"github.com/luiscadari/bancada-api/internal/platform/testdb"
)

// Env is a running application shared by the tests of a package.
type Env struct {
	DB  *testdb.Env
	App *app.App
}

// Main starts the database and the application, runs the tests and cleans up.
// Use it from TestMain: func TestMain(m *testing.M) { apptest.Main(m, &env) }.
func Main(m *testing.M, target **Env) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	db, err := testdb.Start(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apptest:", err)
		os.Exit(1)
	}
	a, err := app.New(Config(db))
	if err != nil {
		_ = db.Stop(ctx)
		fmt.Fprintln(os.Stderr, "apptest:", err)
		os.Exit(1)
	}
	*target = &Env{DB: db, App: a}
	code := m.Run()
	a.Close()
	_ = db.Stop(ctx)
	os.Exit(code)
}

// Config returns an application config pointing at db.
func Config(db *testdb.Env) config.Config {
	return config.Config{
		Env:               "test",
		DatabaseURL:       db.AppDSN,
		SystemDatabaseURL: db.SystemDSN,
		JWTSecret:         "test-secret-test-secret-test-secret",
		AccessTokenTTL:    15 * time.Minute,
		RefreshTokenTTL:   720 * time.Hour,
		InvitationTTL:     168 * time.Hour,
		DevicePasswordKey: "dGVzdC1kZXZpY2UtcGFzc3dvcmQta2V5LTMyYnl0ZXM=",
		PublicAppURL:      "http://app.test",
	}
}

// Response is a recorded HTTP response.
type Response struct {
	Status int
	Body   []byte
	Header http.Header
}

// Items returns the "items" array of a list response.
func (r Response) Items(t testing.TB) []map[string]any {
	t.Helper()
	raw, _ := r.JSON(t)["items"].([]any)
	out := make([]map[string]any, len(raw))
	for i, v := range raw {
		out[i], _ = v.(map[string]any)
	}
	return out
}

// JSON decodes the body into a generic map.
func (r Response) JSON(t testing.TB) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("decode body %q: %v", r.Body, err)
	}
	return m
}

// Decode decodes the body into v.
func (r Response) Decode(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode body %q: %v", r.Body, err)
	}
}

// Code returns the problem code of an error response.
func (r Response) Code(t testing.TB) string {
	t.Helper()
	code, _ := r.JSON(t)["code"].(string)
	return code
}

// Client sends requests to the application with optional credentials.
type Client struct {
	Env      *Env
	Token    string
	BranchID string
	IP       string
}

// Do sends a request with a JSON body (nil for none).
func (c Client) Do(t testing.TB, method, path string, body any) Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.BranchID != "" {
		req.Header.Set("X-Branch-ID", c.BranchID)
	}
	if c.IP != "" {
		req.RemoteAddr = c.IP + ":1234"
	}
	rec := httptest.NewRecorder()
	c.Env.App.Router.ServeHTTP(rec, req)
	return Response{Status: rec.Code, Body: rec.Body.Bytes(), Header: rec.Header()}
}

// Tenant is an account created by Signup, with its owner signed in.
type Tenant struct {
	Client
	AccountID    string
	BranchID     string
	UserID       string
	Email        string
	Password     string
	RefreshToken string
}

var seq atomic.Int64

// Unique returns a short unique suffix.
func Unique() string {
	return fmt.Sprintf("%d%s", seq.Add(1), strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
}

// Signup creates a new account (with the IT repair niche pack) and returns its
// signed-in owner.
func Signup(t testing.TB, env *Env) Tenant { return SignupWith(t, env, true) }

// SignupWith creates a new account, choosing whether to apply the niche pack.
func SignupWith(t testing.TB, env *Env, applyNichePack bool) Tenant {
	t.Helper()
	email := "owner" + Unique() + "@example.com"
	res := Client{Env: env}.Do(t, http.MethodPost, "/v1/auth/signup", map[string]any{
		"account":          map[string]any{"trade_name": "Loja " + Unique()},
		"branch":           map[string]any{"name": "Matriz", "os_prefix": "MAT"},
		"owner":            map[string]any{"name": "Dona", "email": email, "password": "senha-forte-123"},
		"apply_niche_pack": applyNichePack,
	})
	if res.Status != http.StatusCreated {
		t.Fatalf("signup: %d %s", res.Status, res.Body)
	}
	var s struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		User         struct {
			ID string `json:"id"`
		} `json:"user"`
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	res.Decode(t, &s)
	branches := Client{Env: env, Token: s.AccessToken}.Do(t, http.MethodGet, "/v1/branches", nil)
	var bl struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	branches.Decode(t, &bl)
	if len(bl.Items) != 1 {
		t.Fatalf("expected one branch, got %s", branches.Body)
	}
	return Tenant{
		Client:    Client{Env: env, Token: s.AccessToken, BranchID: bl.Items[0].ID},
		AccountID: s.Account.ID, BranchID: bl.Items[0].ID, UserID: s.User.ID,
		Email: email, Password: "senha-forte-123", RefreshToken: s.RefreshToken,
	}
}

// Invite invites a user with a role on the tenant's branch, accepts the invitation and
// returns a signed-in client for that user.
func (tn Tenant) Invite(t testing.TB, role string) (Client, string) {
	t.Helper()
	email := role + Unique() + "@example.com"
	res := tn.Do(t, http.MethodPost, "/v1/users/invitations", map[string]any{
		"name": "Pessoa " + role, "email": email,
		"grants": []map[string]any{{"branch_id": tn.BranchID, "role": role}},
	})
	if res.Status != http.StatusCreated {
		t.Fatalf("invite: %d %s", res.Status, res.Body)
	}
	link := res.JSON(t)["invitation_url"].(string)
	token := link[strings.Index(link, "token=")+len("token="):]
	acc := Client{Env: tn.Env}.Do(t, http.MethodPost, "/v1/auth/accept-invitation",
		map[string]any{"token": token, "password": "senha-do-" + role})
	if acc.Status != http.StatusOK {
		t.Fatalf("accept: %d %s", acc.Status, acc.Body)
	}
	user := acc.JSON(t)["user"].(map[string]any)
	return Client{Env: tn.Env, Token: acc.JSON(t)["access_token"].(string), BranchID: tn.BranchID}, user["id"].(string)
}
