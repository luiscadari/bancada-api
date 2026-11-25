package identity

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/apperr"
	"github.com/luiscadari/bancada-api/internal/platform/auth"
	"github.com/luiscadari/bancada-api/internal/platform/database"
)

// The methods in this file run inside the request's tenant transaction.

func mustActor(ctx context.Context) *actor.Actor {
	a, ok := actor.From(ctx)
	if !ok {
		panic("identity: no actor in context")
	}
	return a
}

// ---- grants -----------------------------------------------------------------------

func grantsOf(ctx context.Context, userIDs ...uuid.UUID) ([]UserBranchRole, error) {
	var grants []UserBranchRole
	err := database.Conn(ctx).Where("user_id IN ?", userIDs).Order("role, branch_id").Find(&grants).Error
	return grants, err
}

func toActorGrants(grants []UserBranchRole) []actor.Grant {
	out := make([]actor.Grant, 0, len(grants))
	for _, g := range grants {
		out = append(out, actor.Grant{BranchID: g.BranchID, Role: g.Role})
	}
	return out
}

func (s *Service) userView(ctx context.Context, u User) (UserView, error) {
	grants, err := grantsOf(ctx, u.ID)
	return UserView{User: u, Grants: grants}, err
}

func (s *Service) userViews(ctx context.Context, users []User) ([]UserView, error) {
	if len(users) == 0 {
		return []UserView{}, nil
	}
	userIDs := make([]uuid.UUID, len(users))
	for i, u := range users {
		userIDs[i] = u.ID
	}
	grants, err := grantsOf(ctx, userIDs...)
	if err != nil {
		return nil, err
	}
	byUser := map[uuid.UUID][]UserBranchRole{}
	for _, g := range grants {
		byUser[g.UserID] = append(byUser[g.UserID], g)
	}
	views := make([]UserView, len(users))
	for i, u := range users {
		views[i] = UserView{User: u, Grants: byUser[u.ID]}
		if views[i].Grants == nil {
			views[i].Grants = []UserBranchRole{}
		}
	}
	return views, nil
}

// GrantInput is a requested role, optionally bound to a branch.
type GrantInput struct {
	BranchID *uuid.UUID
	Role     string
}

// validateGrants checks the shape of grants: owners are account-wide, other roles need
// an existing branch. Duplicates are removed.
func validateGrants(ctx context.Context, in []GrantInput) ([]GrantInput, error) {
	var out []GrantInput
	seen := map[string]bool{}
	for i, g := range in {
		field := "grants[" + strconv.Itoa(i) + "]"
		switch g.Role {
		case actor.RoleOwner:
			if g.BranchID != nil {
				return nil, apperr.Validation("O dono tem acesso a todas as filiais; não informe a filial.",
					apperr.Field(field+".branch_id", "deve ser nulo para o papel owner"))
			}
		case actor.RoleAttendant, actor.RoleTechnician:
			if g.BranchID == nil {
				return nil, apperr.Validation("Informe a filial de cada papel.",
					apperr.Field(field+".branch_id", "obrigatório para este papel"))
			}
			if err := branchExists(ctx, *g.BranchID); err != nil {
				return nil, err
			}
		default:
			return nil, apperr.Validation("Papel inválido.", apperr.Field(field+".role", "papel desconhecido"))
		}
		key := g.Role
		if g.BranchID != nil {
			key += g.BranchID.String()
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, g)
		}
	}
	return out, nil
}

func hasOwner(grants []GrantInput) bool {
	return slices.ContainsFunc(grants, func(g GrantInput) bool { return g.Role == actor.RoleOwner })
}

// ensureAnotherOwner fails when userID is the last active owner of the account.
func ensureAnotherOwner(ctx context.Context, userID uuid.UUID) error {
	var n int64
	err := database.Conn(ctx).Model(&UserBranchRole{}).
		Joins("JOIN users ON users.id = user_branch_roles.user_id AND users.deleted_at IS NULL").
		Where("user_branch_roles.role = ? AND users.status = ? AND user_branch_roles.user_id <> ?",
			actor.RoleOwner, UserActive, userID).
		Count(&n).Error
	if err != nil {
		return err
	}
	if n == 0 {
		return errLastOwner
	}
	return nil
}

func replaceGrants(ctx context.Context, userID uuid.UUID, grants []GrantInput) error {
	db := database.Conn(ctx)
	if err := db.Where("user_id = ?", userID).Delete(&UserBranchRole{}).Error; err != nil {
		return err
	}
	for _, g := range grants {
		if err := db.Create(&UserBranchRole{UserID: userID, BranchID: g.BranchID, Role: g.Role}).Error; err != nil {
			return err
		}
	}
	return nil
}

// ---- me, account ------------------------------------------------------------------

// Me returns the current user, account and accessible branches.
func (s *Service) Me(ctx context.Context) (UserView, Account, []Branch, error) {
	a := mustActor(ctx)
	db := database.Conn(ctx)
	var user User
	var account Account
	if err := db.First(&user, "id = ?", a.UserID).Error; err != nil {
		return UserView{}, Account{}, nil, err
	}
	if err := db.First(&account, "id = ?", a.AccountID).Error; err != nil {
		return UserView{}, Account{}, nil, err
	}
	view, err := s.userView(ctx, user)
	if err != nil {
		return UserView{}, Account{}, nil, err
	}
	branches, err := s.ListBranches(ctx)
	return view, account, branches, err
}

func (s *Service) GetAccount(ctx context.Context) (Account, error) {
	var account Account
	err := database.Conn(ctx).First(&account, "id = ?", mustActor(ctx).AccountID).Error
	return account, err
}

type AccountUpdate struct {
	TradeName *string
	Document  *string
}

func (s *Service) UpdateAccount(ctx context.Context, in AccountUpdate) (Account, error) {
	account, err := s.GetAccount(ctx)
	if err != nil {
		return account, err
	}
	changes := map[string]any{}
	if in.TradeName != nil {
		changes["trade_name"] = strings.TrimSpace(*in.TradeName)
	}
	if in.Document != nil {
		changes["document"] = cleanDocument(in.Document)
	}
	if len(changes) == 0 {
		return account, nil
	}
	if err := database.Conn(ctx).Model(&account).Clauses(clause.Returning{}).Updates(changes).Error; err != nil {
		return account, err
	}
	return account, nil
}

// ---- branches ---------------------------------------------------------------------

func branchExists(ctx context.Context, id uuid.UUID) error {
	var n int64
	if err := database.Conn(ctx).Model(&Branch{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return errBranchNotFound
	}
	return nil
}

// ListBranches returns the branches the actor can access.
func (s *Service) ListBranches(ctx context.Context) ([]Branch, error) {
	a := mustActor(ctx)
	q := database.Conn(ctx).Order("name")
	if !a.IsOwner() {
		var allowed []uuid.UUID
		for _, g := range a.Grants {
			if g.BranchID != nil {
				allowed = append(allowed, *g.BranchID)
			}
		}
		if len(allowed) == 0 {
			return []Branch{}, nil
		}
		q = q.Where("id IN ?", allowed)
	}
	var branches []Branch
	err := q.Find(&branches).Error
	return branches, err
}

func (s *Service) GetBranch(ctx context.Context, id uuid.UUID) (Branch, error) {
	var b Branch
	if !mustActor(ctx).CanAccessBranch(id) {
		return b, errBranchNotFound
	}
	err := database.Conn(ctx).First(&b, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return b, errBranchNotFound
	}
	return b, err
}

type BranchCreate struct {
	ID       uuid.UUID
	Name     string
	OSPrefix string
	Timezone string
	Settings *BranchSettings
}

// CreateBranch adds a branch with its own numbering (RF-40). Idempotent by ID.
func (s *Service) CreateBranch(ctx context.Context, in BranchCreate) (Branch, bool, error) {
	db := database.Conn(ctx)
	if in.ID != uuid.Nil {
		var existing Branch
		if err := db.Unscoped().Where("id = ?", in.ID).Limit(1).Find(&existing).Error; err != nil {
			return existing, false, err
		}
		if existing.ID != uuid.Nil {
			return existing, false, nil
		}
	}
	tz, err := normalizeTimezone(in.Timezone)
	if err != nil {
		return Branch{}, false, err
	}
	settings := DefaultBranchSettings()
	if in.Settings != nil {
		settings = *in.Settings
	}
	b := Branch{Name: strings.TrimSpace(in.Name), OSPrefix: in.OSPrefix, Timezone: tz,
		Settings: database.NewJSON(settings)}
	b.ID = in.ID
	if err := db.Create(&b).Error; err != nil {
		return b, false, mapBranchError(err)
	}
	if err := db.Create(&BranchCounter{BranchID: b.ID, AccountID: b.AccountID, NextNumber: 1}).Error; err != nil {
		return b, false, err
	}
	return b, true, nil
}

type BranchUpdate struct {
	Name     *string
	OSPrefix *string
	Timezone *string
}

func (s *Service) UpdateBranch(ctx context.Context, id uuid.UUID, in BranchUpdate) (Branch, error) {
	b, err := s.GetBranch(ctx, id)
	if err != nil {
		return b, err
	}
	changes := map[string]any{}
	if in.Name != nil {
		changes["name"] = strings.TrimSpace(*in.Name)
	}
	if in.OSPrefix != nil {
		changes["os_prefix"] = *in.OSPrefix
	}
	if in.Timezone != nil {
		tz, err := normalizeTimezone(*in.Timezone)
		if err != nil {
			return b, err
		}
		changes["timezone"] = tz
	}
	if len(changes) == 0 {
		return b, nil
	}
	err = database.Conn(ctx).Model(&b).Clauses(clause.Returning{}).Updates(changes).Error
	return b, mapBranchError(err)
}

func (s *Service) UpdateBranchSettings(ctx context.Context, id uuid.UUID, settings BranchSettings) (Branch, error) {
	b, err := s.GetBranch(ctx, id)
	if err != nil {
		return b, err
	}
	if settings.PickupReminderDays == nil {
		settings.PickupReminderDays = []int{}
	}
	err = database.Conn(ctx).Model(&b).Clauses(clause.Returning{}).
		Update("settings", database.NewJSON(settings)).Error
	return b, err
}

// Settings returns the settings of a branch. Used by other modules.
func (s *Service) Settings(ctx context.Context, branchID uuid.UUID) (BranchSettings, string, error) {
	var b Branch
	if err := database.Conn(ctx).First(&b, "id = ?", branchID).Error; err != nil {
		return BranchSettings{}, "", err
	}
	return b.Settings.Data(), b.Timezone, nil
}

// ---- users ------------------------------------------------------------------------

func (s *Service) ListUsers(ctx context.Context) ([]UserView, error) {
	var users []User
	if err := database.Conn(ctx).Order("name").Find(&users).Error; err != nil {
		return nil, err
	}
	return s.userViews(ctx, users)
}

func (s *Service) findUser(ctx context.Context, id uuid.UUID) (User, error) {
	var u User
	err := database.Conn(ctx).First(&u, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return u, errUserNotFound
	}
	return u, err
}

func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (UserView, error) {
	u, err := s.findUser(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	return s.userView(ctx, u)
}

type InvitationInput struct {
	Name   string
	Email  string
	Grants []GrantInput
}

// Invite creates an invited user with its grants and returns the invitation link (RF-41).
func (s *Service) Invite(ctx context.Context, in InvitationInput) (UserView, string, Invitation, error) {
	db := database.Conn(ctx)
	grants, err := validateGrants(ctx, in.Grants)
	if err != nil {
		return UserView{}, "", Invitation{}, err
	}
	user := User{Name: strings.TrimSpace(in.Name), Email: normalizeEmail(in.Email), Status: UserInvited}
	if err := db.Create(&user).Error; err != nil {
		return UserView{}, "", Invitation{}, mapUserError(err)
	}
	if err := replaceGrants(ctx, user.ID, grants); err != nil {
		return UserView{}, "", Invitation{}, err
	}
	secret, hash, err := auth.NewSecret()
	if err != nil {
		return UserView{}, "", Invitation{}, err
	}
	inv := Invitation{UserID: user.ID, TokenHash: hash, ExpiresAt: s.now().Add(s.opts.InvitationTTL)}
	if err := db.Create(&inv).Error; err != nil {
		return UserView{}, "", Invitation{}, err
	}
	view, err := s.userView(ctx, user)
	link := strings.TrimRight(s.opts.PublicAppURL, "/") + "/accept-invitation?token=" + url.QueryEscape(secret)
	return view, link, inv, err
}

// UpdateGrants replaces the roles of a user. The account always keeps an active owner.
func (s *Service) UpdateGrants(ctx context.Context, userID uuid.UUID, in []GrantInput) (UserView, error) {
	user, err := s.findUser(ctx, userID)
	if err != nil {
		return UserView{}, err
	}
	grants, err := validateGrants(ctx, in)
	if err != nil {
		return UserView{}, err
	}
	if !hasOwner(grants) {
		if err := ensureAnotherOwner(ctx, userID); err != nil {
			return UserView{}, err
		}
	}
	if err := replaceGrants(ctx, userID, grants); err != nil {
		return UserView{}, err
	}
	return s.userView(ctx, user)
}

// SetUserStatus disables or re-enables a user. Disabling ends every session of the user.
func (s *Service) SetUserStatus(ctx context.Context, userID uuid.UUID, enable bool) (UserView, error) {
	a := mustActor(ctx)
	db := database.Conn(ctx)
	user, err := s.findUser(ctx, userID)
	if err != nil {
		return UserView{}, err
	}
	switch {
	case enable && user.Status == UserDisabled:
		user.Status = UserActive
	case !enable && user.Status != UserDisabled:
		if userID == a.UserID {
			return UserView{}, errSelfDisable
		}
		grants, err := grantsOf(ctx, userID)
		if err != nil {
			return UserView{}, err
		}
		isOwner := slices.ContainsFunc(grants, func(g UserBranchRole) bool { return g.Role == actor.RoleOwner })
		if isOwner && user.Status == UserActive {
			if err := ensureAnotherOwner(ctx, userID); err != nil {
				return UserView{}, err
			}
		}
		user.Status = UserDisabled
		if err := db.Model(&RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", userID).
			Update("revoked_at", s.now()).Error; err != nil {
			return UserView{}, err
		}
	default:
		return s.userView(ctx, user)
	}
	if err := db.Model(&user).Clauses(clause.Returning{}).Update("status", user.Status).Error; err != nil {
		return UserView{}, err
	}
	return s.userView(ctx, user)
}

// ---- devices and number blocks ----------------------------------------------------

func (s *Service) ListDevices(ctx context.Context) ([]Device, error) {
	var devices []Device
	err := database.Conn(ctx).Order("name").Find(&devices).Error
	return devices, err
}

func (s *Service) findDevice(ctx context.Context, id uuid.UUID) (Device, error) {
	var d Device
	err := database.Conn(ctx).First(&d, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return d, errDeviceNotFound
	}
	return d, err
}

// RevokeDevice blocks a device and ends its sessions (ADR-07).
func (s *Service) RevokeDevice(ctx context.Context, id uuid.UUID) (Device, error) {
	a := mustActor(ctx)
	db := database.Conn(ctx)
	d, err := s.findDevice(ctx, id)
	if err != nil || d.RevokedAt != nil {
		return d, err
	}
	now := s.now()
	if err := db.Model(&d).Clauses(clause.Returning{}).
		Updates(map[string]any{"revoked_at": now, "revoked_by": a.UserID}).Error; err != nil {
		return d, err
	}
	err = db.Model(&RefreshToken{}).Where("device_id = ? AND revoked_at IS NULL", id).Update("revoked_at", now).Error
	return d, err
}

// DefaultBlockSize is how many numbers a device reserves at once (ADR-03).
const DefaultBlockSize = 50

// ReservedBlock is a number block with its branch prefix.
type ReservedBlock struct {
	NumberBlock
	OSPrefix string
}

// ReserveNumberBlock reserves the next size service order numbers of the device's branch
// for offline use (RN-04). Only the device itself or an owner can reserve.
func (s *Service) ReserveNumberBlock(ctx context.Context, deviceID uuid.UUID, size int) (ReservedBlock, error) {
	a := mustActor(ctx)
	d, err := s.findDevice(ctx, deviceID)
	if err != nil {
		return ReservedBlock{}, err
	}
	if d.RevokedAt != nil {
		return ReservedBlock{}, errDeviceRevoked
	}
	isSelf := a.DeviceID != nil && *a.DeviceID == deviceID
	if !isSelf && !a.IsOwner() {
		return ReservedBlock{}, apperr.Forbidden("not_this_device", "Só o próprio computador pode reservar números.")
	}
	if !a.CanAccessBranch(d.BranchID) {
		return ReservedBlock{}, errBranchAccess
	}
	if size <= 0 {
		size = DefaultBlockSize
	}
	start, err := s.allocate(ctx, d.BranchID, int64(size))
	if err != nil {
		return ReservedBlock{}, err
	}
	block := NumberBlock{BranchID: d.BranchID, DeviceID: d.ID, RangeStart: start, RangeEnd: start + int64(size) - 1}
	if err := database.Conn(ctx).Create(&block).Error; err != nil {
		return ReservedBlock{}, err
	}
	b, err := s.GetBranch(ctx, d.BranchID)
	return ReservedBlock{NumberBlock: block, OSPrefix: b.OSPrefix}, err
}

// allocate atomically takes n numbers from the branch counter and returns the first.
func (s *Service) allocate(ctx context.Context, branchID uuid.UUID, n int64) (int64, error) {
	var next int64
	err := database.Conn(ctx).Raw(
		`UPDATE branch_counters SET next_number = next_number + ? WHERE branch_id = ? RETURNING next_number`,
		n, branchID).Scan(&next).Error
	if err != nil {
		return 0, err
	}
	if next == 0 {
		return 0, errBranchNotFound
	}
	return next - n, nil
}

// NextNumber allocates the next service order number of a branch and returns it
// formatted (e.g. MAT-000124) together with its sequence. Used when an order is opened
// online.
func (s *Service) NextNumber(ctx context.Context, branchID uuid.UUID) (string, int64, error) {
	n, err := s.allocate(ctx, branchID, 1)
	if err != nil {
		return "", 0, err
	}
	b, err := s.GetBranch(ctx, branchID)
	if err != nil {
		return "", 0, err
	}
	return FormatNumber(b.OSPrefix, n), n, nil
}

// OwnsReservedNumber reports whether n belongs to a block reserved by deviceID on branchID.
func (s *Service) OwnsReservedNumber(ctx context.Context, branchID, deviceID uuid.UUID, n int64) (bool, error) {
	var count int64
	err := database.Conn(ctx).Model(&NumberBlock{}).
		Where("branch_id = ? AND device_id = ? AND ? BETWEEN range_start AND range_end", branchID, deviceID, n).
		Count(&count).Error
	return count > 0, err
}

// ReservedNumber checks that n belongs to a block reserved by deviceID on branchID and
// returns it formatted (RN-04).
func (s *Service) ReservedNumber(ctx context.Context, branchID, deviceID uuid.UUID, n int64) (string, error) {
	ok, err := s.OwnsReservedNumber(ctx, branchID, deviceID, n)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errNumberNotReserved
	}
	b, err := s.GetBranch(ctx, branchID)
	if err != nil {
		return "", err
	}
	return FormatNumber(b.OSPrefix, n), nil
}

// WorksInBranch reports whether userID is an active user with a role in branchID.
func (s *Service) WorksInBranch(ctx context.Context, userID, branchID uuid.UUID) (bool, error) {
	var users []User
	if err := database.Conn(ctx).Select("id", "status").Where("id = ?", userID).Limit(1).Find(&users).Error; err != nil {
		return false, err
	}
	if len(users) == 0 || users[0].Status != UserActive {
		return false, nil
	}
	grants, err := grantsOf(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, g := range grants {
		if g.BranchID == nil || *g.BranchID == branchID {
			return true, nil
		}
	}
	return false, nil
}

// ---- principal loader ---------------------------------------------------------------

// LoadGrants implements httpx.PrincipalLoader.
func (s *Service) LoadGrants(ctx context.Context, userID uuid.UUID, deviceID *uuid.UUID) ([]actor.Grant, error) {
	db := database.Conn(ctx)
	var status string
	if err := db.Model(&User{}).Select("status").Where("id = ?", userID).Scan(&status).Error; err != nil {
		return nil, err
	}
	if status != UserActive {
		return nil, apperr.Unauthorized("user_disabled", "Seu acesso foi desativado. Fale com o dono da conta.")
	}
	if deviceID != nil {
		var revoked int64
		if err := db.Model(&Device{}).Where("id = ? AND revoked_at IS NULL", *deviceID).Count(&revoked).Error; err != nil {
			return nil, err
		}
		if revoked == 0 {
			return nil, apperr.Unauthorized("device_revoked",
				"Este computador foi desconectado da conta. Peça ao dono para liberá-lo de novo.")
		}
	}
	grants, err := grantsOf(ctx, userID)
	if err != nil {
		return nil, err
	}
	return toActorGrants(grants), nil
}

// BranchExists implements httpx.PrincipalLoader.
func (s *Service) BranchExists(ctx context.Context, branchID uuid.UUID) (bool, error) {
	err := branchExists(ctx, branchID)
	if errors.Is(err, errBranchNotFound) {
		return false, nil
	}
	return err == nil, err
}
