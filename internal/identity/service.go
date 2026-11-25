package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/apperr"
	"github.com/luiscadari/bancada-api/internal/platform/auth"
	"github.com/luiscadari/bancada-api/internal/platform/database"
	"github.com/luiscadari/bancada-api/internal/platform/ids"
	"github.com/luiscadari/bancada-api/internal/platform/reqctx"
)

// AccountCreatedHook runs inside the signup transaction, after the account, its first
// branch and its owner exist. Other modules use it to seed data (for example, the
// niche pack of RF-08).
type AccountCreatedHook func(ctx context.Context, accountID, branchID uuid.UUID, niche string, applyNichePack bool) error

// Options configures the identity service.
type Options struct {
	RefreshTTL    time.Duration
	InvitationTTL time.Duration
	PublicAppURL  string
}

// Service implements authentication and the management of accounts, branches, users
// and devices.
type Service struct {
	app     *gorm.DB // bancada_app: every tenant transaction
	sys     *gorm.DB // bancada_system: pre-tenant lookups only (login, refresh, invitation)
	issuer  *auth.Issuer
	opts    Options
	limiter *loginLimiter
	hooks   []AccountCreatedHook
	now     func() time.Time
}

func NewService(app, sys *gorm.DB, issuer *auth.Issuer, opts Options) *Service {
	return &Service{
		app:     app,
		sys:     sys,
		issuer:  issuer,
		opts:    opts,
		limiter: newLoginLimiter(10, 15*time.Minute),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// OnAccountCreated registers a hook run at signup.
func (s *Service) OnAccountCreated(h AccountCreatedHook) { s.hooks = append(s.hooks, h) }

// RequestMeta describes the client of a public request.
type RequestMeta struct {
	IP        string
	UserAgent string
	RequestID string
}

// UserView is a user with its grants.
type UserView struct {
	User
	Grants []UserBranchRole
}

// Session is the result of a successful authentication.
type Session struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	DeviceID         *uuid.UUID
	User             UserView
	Account          Account
}

// ---- signup ----------------------------------------------------------------------

type SignupInput struct {
	TradeName      string
	Document       *string
	BranchName     string
	OSPrefix       string
	Timezone       string
	OwnerName      string
	OwnerEmail     string
	OwnerPassword  string
	ApplyNichePack bool
}

// Signup creates an account in trial with its first branch and owner (RF-S01, RN-S01).
func (s *Service) Signup(ctx context.Context, in SignupInput, meta RequestMeta) (*Session, error) {
	tz, err := normalizeTimezone(in.Timezone)
	if err != nil {
		return nil, err
	}
	hash, err := auth.HashPassword(in.OwnerPassword)
	if err != nil {
		return nil, err
	}
	accountID, userID, branchID := ids.New(), ids.New(), ids.New()
	trialEnds := s.now().AddDate(0, 0, TrialDays)
	const niche = "it_repair"

	var session *Session
	err = database.TenantTx(ctx, s.app, accountID, func(ctx context.Context) error {
		ctx = actor.With(ctx, &actor.Actor{AccountID: accountID, UserID: userID, BranchID: &branchID,
			IP: meta.IP, RequestID: meta.RequestID})
		db := database.Conn(ctx)

		account := Account{ID: accountID, TradeName: strings.TrimSpace(in.TradeName), Document: cleanDocument(in.Document),
			Niche: niche, Status: AccountTrial, TrialEndsAt: &trialEnds}
		if err := db.Create(&account).Error; err != nil {
			return err
		}
		branch := Branch{Name: strings.TrimSpace(in.BranchName), OSPrefix: in.OSPrefix, Timezone: tz,
			Settings: database.NewJSON(DefaultBranchSettings())}
		branch.ID = branchID
		if err := db.Create(&branch).Error; err != nil {
			return err
		}
		if err := db.Create(&BranchCounter{BranchID: branchID, AccountID: accountID, NextNumber: 1}).Error; err != nil {
			return err
		}
		user := User{Name: strings.TrimSpace(in.OwnerName), Email: normalizeEmail(in.OwnerEmail),
			PasswordHash: &hash, Status: UserActive}
		user.ID = userID
		if err := db.Create(&user).Error; err != nil {
			return mapUserError(err)
		}
		if err := db.Create(&UserBranchRole{UserID: userID, Role: actor.RoleOwner}).Error; err != nil {
			return err
		}
		for _, h := range s.hooks {
			if err := h(ctx, accountID, branchID, niche, in.ApplyNichePack); err != nil {
				return err
			}
		}
		session, err = s.newSession(ctx, user, nil, ids.New(), meta)
		return err
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

// ---- login, refresh, logout ------------------------------------------------------

type DeviceInput struct {
	ID       uuid.UUID
	BranchID uuid.UUID
	Name     string
	Platform string
}

type userLookup struct {
	ID           uuid.UUID
	AccountID    uuid.UUID
	PasswordHash *string
	Status       string
}

// Login authenticates by email and password. When device is given, the installation is
// registered on (or moved to) that branch and the session is bound to it.
func (s *Service) Login(ctx context.Context, email, password string, device *DeviceInput, meta RequestMeta) (*Session, error) {
	email = normalizeEmail(email)
	key := meta.IP + "|" + email
	if !s.limiter.Allowed(key) {
		return nil, errTooManyAttempts
	}

	var u userLookup
	err := s.sys.WithContext(reqctx.Unwrap(ctx)).Raw(
		`SELECT id, account_id, password_hash, status FROM users WHERE lower(email) = ? AND deleted_at IS NULL`,
		email).Scan(&u).Error
	if err != nil {
		return nil, err
	}
	if u.ID == uuid.Nil || u.PasswordHash == nil {
		auth.EqualizeTiming(password)
		s.limiter.Fail(key)
		return nil, errInvalidCredentials
	}
	ok, err := auth.VerifyPassword(password, *u.PasswordHash)
	if err != nil {
		return nil, err
	}
	if !ok {
		s.limiter.Fail(key)
		return nil, errInvalidCredentials
	}
	if u.Status != UserActive {
		return nil, errUserDisabled
	}
	s.limiter.Reset(key)

	var session *Session
	err = database.TenantTx(ctx, s.app, u.AccountID, func(ctx context.Context) error {
		a := &actor.Actor{AccountID: u.AccountID, UserID: u.ID, IP: meta.IP, RequestID: meta.RequestID}
		ctx = actor.With(ctx, a)
		db := database.Conn(ctx)

		var user User
		if err := db.First(&user, "id = ?", u.ID).Error; err != nil {
			return err
		}
		var deviceID *uuid.UUID
		if device != nil {
			grants, err := grantsOf(ctx, u.ID)
			if err != nil {
				return err
			}
			a.Grants = toActorGrants(grants)
			if !a.CanAccessBranch(device.BranchID) {
				return errBranchAccess
			}
			a.BranchID = &device.BranchID
			a.DeviceID = &device.ID
			if err := s.registerDevice(ctx, *device); err != nil {
				return err
			}
			deviceID = &device.ID
		}
		now := s.now()
		if err := db.Model(&user).Update("last_login_at", now).Error; err != nil {
			return err
		}
		user.LastLoginAt = &now
		var err error
		session, err = s.newSession(ctx, user, deviceID, ids.New(), meta)
		return err
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s *Service) registerDevice(ctx context.Context, in DeviceInput) error {
	db := database.Conn(ctx)
	if err := branchExists(ctx, in.BranchID); err != nil {
		return err
	}
	var d Device
	err := db.First(&d, "id = ?", in.ID).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		now := s.now()
		d = Device{BranchID: in.BranchID, Name: strings.TrimSpace(in.Name), Platform: in.Platform, LastSeenAt: &now}
		d.ID = in.ID
		if err := db.Create(&d).Error; err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				// The id belongs to a device of another account.
				return apperr.Conflict("device_id_taken", "Este identificador de computador já está em uso.")
			}
			return err
		}
		return nil
	case err != nil:
		return err
	}
	if d.RevokedAt != nil {
		return errDeviceRevoked
	}
	return db.Model(&d).Updates(map[string]any{
		"branch_id": in.BranchID, "name": strings.TrimSpace(in.Name), "platform": in.Platform, "last_seen_at": s.now(),
	}).Error
}

// newSession issues an access token and a refresh token of family familyID.
func (s *Service) newSession(ctx context.Context, user User, deviceID *uuid.UUID, familyID uuid.UUID, meta RequestMeta) (*Session, error) {
	db := database.Conn(ctx)
	secret, hash, err := auth.NewSecret()
	if err != nil {
		return nil, err
	}
	rt := RefreshToken{
		ID: ids.New(), AccountID: user.AccountID, UserID: user.ID, DeviceID: deviceID, FamilyID: familyID,
		TokenHash: hash, ExpiresAt: s.now().Add(s.opts.RefreshTTL), IP: nonEmpty(meta.IP), UserAgent: nonEmpty(meta.UserAgent),
	}
	if err := db.Create(&rt).Error; err != nil {
		return nil, err
	}
	access, exp, err := s.issuer.Issue(user.ID, user.AccountID, deviceID)
	if err != nil {
		return nil, err
	}
	var account Account
	if err := db.First(&account, "id = ?", user.AccountID).Error; err != nil {
		return nil, err
	}
	view, err := s.userView(ctx, user)
	if err != nil {
		return nil, err
	}
	return &Session{
		AccessToken: access, AccessExpiresAt: exp,
		RefreshToken: secret, RefreshExpiresAt: rt.ExpiresAt,
		DeviceID: deviceID, User: view, Account: account,
	}, nil
}

// accountOfToken finds the account of a refresh token before a tenant is known.
func (s *Service) accountOfToken(ctx context.Context, table, hash string) (uuid.UUID, error) {
	var row struct{ AccountID uuid.UUID }
	err := s.sys.WithContext(reqctx.Unwrap(ctx)).Raw(`SELECT account_id FROM `+table+` WHERE token_hash = ?`, hash).Scan(&row).Error
	return row.AccountID, err
}

// Refresh rotates a refresh token. Presenting a token that was already used revokes
// its whole family, since it means the token leaked.
func (s *Service) Refresh(ctx context.Context, token string, meta RequestMeta) (*Session, error) {
	hash := auth.HashSecret(token)
	accountID, err := s.accountOfToken(ctx, "refresh_tokens", hash)
	if err != nil {
		return nil, err
	}
	if accountID == uuid.Nil {
		return nil, errInvalidRefresh
	}
	var session *Session
	var reused bool
	err = database.TenantTx(ctx, s.app, accountID, func(ctx context.Context) error {
		db := database.Conn(ctx)
		var rt RefreshToken
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&rt, "token_hash = ?", hash).Error; err != nil {
			return err
		}
		now := s.now()
		if rt.RevokedAt != nil || now.After(rt.ExpiresAt) {
			return errInvalidRefresh
		}
		if rt.UsedAt != nil {
			reused = true
			return db.Model(&RefreshToken{}).Where("family_id = ? AND revoked_at IS NULL", rt.FamilyID).
				Update("revoked_at", now).Error
		}
		ctx = actor.With(ctx, &actor.Actor{AccountID: accountID, UserID: rt.UserID, DeviceID: rt.DeviceID,
			IP: meta.IP, RequestID: meta.RequestID})
		db = database.Conn(ctx)

		var user User
		if err := db.First(&user, "id = ?", rt.UserID).Error; err != nil {
			return err
		}
		if user.Status != UserActive {
			return errUserDisabled
		}
		if rt.DeviceID != nil {
			var d Device
			if err := db.First(&d, "id = ?", *rt.DeviceID).Error; err != nil {
				return err
			}
			if d.RevokedAt != nil {
				return errDeviceRevoked
			}
			if err := db.Model(&d).Update("last_seen_at", now).Error; err != nil {
				return err
			}
		}
		if err := db.Model(&rt).Update("used_at", now).Error; err != nil {
			return err
		}
		var err error
		session, err = s.newSession(ctx, user, rt.DeviceID, rt.FamilyID, meta)
		return err
	})
	if err != nil {
		return nil, err
	}
	if reused {
		return nil, errInvalidRefresh
	}
	return session, nil
}

// Logout revokes the session (token family) of a refresh token. Unknown tokens are ignored.
func (s *Service) Logout(ctx context.Context, token string) error {
	hash := auth.HashSecret(token)
	accountID, err := s.accountOfToken(ctx, "refresh_tokens", hash)
	if err != nil || accountID == uuid.Nil {
		return err
	}
	return database.TenantTx(ctx, s.app, accountID, func(ctx context.Context) error {
		db := database.Conn(ctx)
		var rt RefreshToken
		if err := db.First(&rt, "token_hash = ?", hash).Error; err != nil {
			return err
		}
		return db.Model(&RefreshToken{}).Where("family_id = ? AND revoked_at IS NULL", rt.FamilyID).
			Update("revoked_at", s.now()).Error
	})
}

// AcceptInvitation activates an invited user with the chosen password (RF-41).
func (s *Service) AcceptInvitation(ctx context.Context, token, password string, meta RequestMeta) (*Session, error) {
	hash := auth.HashSecret(token)
	accountID, err := s.accountOfToken(ctx, "invitations", hash)
	if err != nil {
		return nil, err
	}
	if accountID == uuid.Nil {
		return nil, errInvitationInvalid
	}
	pwHash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	var session *Session
	err = database.TenantTx(ctx, s.app, accountID, func(ctx context.Context) error {
		db := database.Conn(ctx)
		var inv Invitation
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&inv, "token_hash = ?", hash).Error; err != nil {
			return errInvitationInvalid
		}
		if inv.AcceptedAt != nil {
			return errInvitationInvalid
		}
		if s.now().After(inv.ExpiresAt) {
			return errInvitationExpired
		}
		ctx = actor.With(ctx, &actor.Actor{AccountID: accountID, UserID: inv.UserID, IP: meta.IP, RequestID: meta.RequestID})
		db = database.Conn(ctx)

		var user User
		if err := db.First(&user, "id = ?", inv.UserID).Error; err != nil {
			return err
		}
		if user.Status != UserInvited {
			return errInvitationInvalid
		}
		now := s.now()
		if err := db.Model(&inv).Update("accepted_at", now).Error; err != nil {
			return err
		}
		if err := db.Model(&user).Updates(map[string]any{
			"password_hash": pwHash, "status": UserActive, "last_login_at": now,
		}).Error; err != nil {
			return err
		}
		user.Status, user.LastLoginAt = UserActive, &now
		session, err = s.newSession(ctx, user, nil, ids.New(), meta)
		return err
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

// ---- helpers ----------------------------------------------------------------------

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func normalizeTimezone(tz string) (string, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return "America/Sao_Paulo", nil
	}
	if _, err := time.LoadLocation(tz); err != nil || tz == "Local" {
		return "", errInvalidTimezone
	}
	return tz, nil
}

func cleanDocument(doc *string) *string {
	if doc == nil {
		return nil
	}
	var b strings.Builder
	for _, r := range *doc {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return nil
	}
	v := b.String()
	return &v
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && (constraint == "" || pgErr.ConstraintName == constraint)
}

func mapUserError(err error) error {
	if isUniqueViolation(err, "users_email_key") {
		return errEmailTaken
	}
	return err
}

func mapBranchError(err error) error {
	if isUniqueViolation(err, "branches_prefix_key") {
		return errPrefixTaken
	}
	return err
}
