package identity

import (
	"context"
	"time"

	"github.com/luiscadari/bancada-api/internal/api/gen"
	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/httpx"
)

// Handler implements the identity part of gen.StrictServerInterface.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func meta(ctx context.Context) RequestMeta {
	ip, ua, rid := httpx.ClientMeta(ctx)
	return RequestMeta{IP: ip, UserAgent: ua, RequestID: rid}
}

// ---- auth -------------------------------------------------------------------------

func (h *Handler) Signup(ctx context.Context, req gen.SignupRequestObject) (gen.SignupResponseObject, error) {
	b := req.Body
	in := SignupInput{
		TradeName: b.Account.TradeName, Document: b.Account.Document,
		BranchName: b.Branch.Name, OSPrefix: b.Branch.OsPrefix, Timezone: deref(b.Branch.Timezone),
		OwnerName: b.Owner.Name, OwnerEmail: string(b.Owner.Email), OwnerPassword: b.Owner.Password,
	}
	s, err := h.svc.Signup(ctx, in, meta(ctx))
	if err != nil {
		return nil, err
	}
	return gen.Signup201JSONResponse(h.session(s)), nil
}

func (h *Handler) Login(ctx context.Context, req gen.LoginRequestObject) (gen.LoginResponseObject, error) {
	var device *DeviceInput
	if d := req.Body.Device; d != nil {
		device = &DeviceInput{ID: d.Id, BranchID: d.BranchId, Name: d.Name, Platform: string(d.Platform)}
	}
	s, err := h.svc.Login(ctx, string(req.Body.Email), req.Body.Password, device, meta(ctx))
	if err != nil {
		return nil, err
	}
	return gen.Login200JSONResponse(h.session(s)), nil
}

func (h *Handler) RefreshSession(ctx context.Context, req gen.RefreshSessionRequestObject) (gen.RefreshSessionResponseObject, error) {
	s, err := h.svc.Refresh(ctx, req.Body.RefreshToken, meta(ctx))
	if err != nil {
		return nil, err
	}
	return gen.RefreshSession200JSONResponse(h.session(s)), nil
}

func (h *Handler) Logout(ctx context.Context, req gen.LogoutRequestObject) (gen.LogoutResponseObject, error) {
	if err := h.svc.Logout(ctx, req.Body.RefreshToken); err != nil {
		return nil, err
	}
	return gen.Logout204Response{}, nil
}

func (h *Handler) AcceptInvitation(ctx context.Context, req gen.AcceptInvitationRequestObject) (gen.AcceptInvitationResponseObject, error) {
	s, err := h.svc.AcceptInvitation(ctx, req.Body.Token, req.Body.Password, meta(ctx))
	if err != nil {
		return nil, err
	}
	return gen.AcceptInvitation200JSONResponse(h.session(s)), nil
}

// ---- me, account ------------------------------------------------------------------

func (h *Handler) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	user, account, branches, err := h.svc.Me(ctx)
	if err != nil {
		return nil, err
	}
	a, _ := actor.From(ctx)
	return gen.GetMe200JSONResponse{
		User: toGenUser(user), Account: toGenAccount(account), Branches: toGenBranches(branches), DeviceId: a.DeviceID,
	}, nil
}

func (h *Handler) GetAccount(ctx context.Context, _ gen.GetAccountRequestObject) (gen.GetAccountResponseObject, error) {
	acc, err := h.svc.GetAccount(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetAccount200JSONResponse(toGenAccount(acc)), nil
}

func (h *Handler) UpdateAccount(ctx context.Context, req gen.UpdateAccountRequestObject) (gen.UpdateAccountResponseObject, error) {
	acc, err := h.svc.UpdateAccount(ctx, AccountUpdate{TradeName: req.Body.TradeName, Document: req.Body.Document})
	if err != nil {
		return nil, err
	}
	return gen.UpdateAccount200JSONResponse(toGenAccount(acc)), nil
}

// ---- branches ---------------------------------------------------------------------

func (h *Handler) ListBranches(ctx context.Context, _ gen.ListBranchesRequestObject) (gen.ListBranchesResponseObject, error) {
	branches, err := h.svc.ListBranches(ctx)
	if err != nil {
		return nil, err
	}
	return gen.ListBranches200JSONResponse{Items: toGenBranches(branches)}, nil
}

func (h *Handler) CreateBranch(ctx context.Context, req gen.CreateBranchRequestObject) (gen.CreateBranchResponseObject, error) {
	b := req.Body
	in := BranchCreate{Name: b.Name, OSPrefix: b.OsPrefix, Timezone: deref(b.Timezone)}
	if b.Id != nil {
		in.ID = *b.Id
	}
	if b.Settings != nil {
		s := fromGenSettings(*b.Settings)
		in.Settings = &s
	}
	branch, _, err := h.svc.CreateBranch(ctx, in)
	if err != nil {
		return nil, err
	}
	return gen.CreateBranch201JSONResponse(toGenBranch(branch)), nil
}

func (h *Handler) GetBranch(ctx context.Context, req gen.GetBranchRequestObject) (gen.GetBranchResponseObject, error) {
	b, err := h.svc.GetBranch(ctx, req.BranchId)
	if err != nil {
		return nil, err
	}
	return gen.GetBranch200JSONResponse(toGenBranch(b)), nil
}

func (h *Handler) UpdateBranch(ctx context.Context, req gen.UpdateBranchRequestObject) (gen.UpdateBranchResponseObject, error) {
	b, err := h.svc.UpdateBranch(ctx, req.BranchId, BranchUpdate{
		Name: req.Body.Name, OSPrefix: req.Body.OsPrefix, Timezone: req.Body.Timezone,
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateBranch200JSONResponse(toGenBranch(b)), nil
}

func (h *Handler) UpdateBranchSettings(ctx context.Context, req gen.UpdateBranchSettingsRequestObject) (gen.UpdateBranchSettingsResponseObject, error) {
	b, err := h.svc.UpdateBranchSettings(ctx, req.BranchId, fromGenSettings(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateBranchSettings200JSONResponse(toGenBranch(b)), nil
}

// ---- users ------------------------------------------------------------------------

func (h *Handler) ListUsers(ctx context.Context, _ gen.ListUsersRequestObject) (gen.ListUsersResponseObject, error) {
	users, err := h.svc.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]gen.User, len(users))
	for i, u := range users {
		items[i] = toGenUser(u)
	}
	return gen.ListUsers200JSONResponse{Items: items}, nil
}

func (h *Handler) GetUser(ctx context.Context, req gen.GetUserRequestObject) (gen.GetUserResponseObject, error) {
	u, err := h.svc.GetUser(ctx, req.UserId)
	if err != nil {
		return nil, err
	}
	return gen.GetUser200JSONResponse(toGenUser(u)), nil
}

func (h *Handler) InviteUser(ctx context.Context, req gen.InviteUserRequestObject) (gen.InviteUserResponseObject, error) {
	u, link, inv, err := h.svc.Invite(ctx, InvitationInput{
		Name: req.Body.Name, Email: string(req.Body.Email), Grants: fromGenGrants(req.Body.Grants),
	})
	if err != nil {
		return nil, err
	}
	return gen.InviteUser201JSONResponse{User: toGenUser(u), InvitationUrl: link, ExpiresAt: inv.ExpiresAt}, nil
}

func (h *Handler) UpdateUserGrants(ctx context.Context, req gen.UpdateUserGrantsRequestObject) (gen.UpdateUserGrantsResponseObject, error) {
	u, err := h.svc.UpdateGrants(ctx, req.UserId, fromGenGrants(req.Body.Grants))
	if err != nil {
		return nil, err
	}
	return gen.UpdateUserGrants200JSONResponse(toGenUser(u)), nil
}

func (h *Handler) DisableUser(ctx context.Context, req gen.DisableUserRequestObject) (gen.DisableUserResponseObject, error) {
	u, err := h.svc.SetUserStatus(ctx, req.UserId, false)
	if err != nil {
		return nil, err
	}
	return gen.DisableUser200JSONResponse(toGenUser(u)), nil
}

func (h *Handler) EnableUser(ctx context.Context, req gen.EnableUserRequestObject) (gen.EnableUserResponseObject, error) {
	u, err := h.svc.SetUserStatus(ctx, req.UserId, true)
	if err != nil {
		return nil, err
	}
	return gen.EnableUser200JSONResponse(toGenUser(u)), nil
}

// ---- devices ----------------------------------------------------------------------

func (h *Handler) ListDevices(ctx context.Context, _ gen.ListDevicesRequestObject) (gen.ListDevicesResponseObject, error) {
	devices, err := h.svc.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Device, len(devices))
	for i, d := range devices {
		items[i] = toGenDevice(d)
	}
	return gen.ListDevices200JSONResponse{Items: items}, nil
}

func (h *Handler) RevokeDevice(ctx context.Context, req gen.RevokeDeviceRequestObject) (gen.RevokeDeviceResponseObject, error) {
	d, err := h.svc.RevokeDevice(ctx, req.DeviceId)
	if err != nil {
		return nil, err
	}
	return gen.RevokeDevice200JSONResponse(toGenDevice(d)), nil
}

func (h *Handler) ReserveNumberBlock(ctx context.Context, req gen.ReserveNumberBlockRequestObject) (gen.ReserveNumberBlockResponseObject, error) {
	size := DefaultBlockSize
	if req.Body != nil && req.Body.Size != nil {
		size = *req.Body.Size
	}
	b, err := h.svc.ReserveNumberBlock(ctx, req.DeviceId, size)
	if err != nil {
		return nil, err
	}
	return gen.ReserveNumberBlock201JSONResponse{
		Id: b.ID, DeviceId: b.DeviceID, BranchId: b.BranchID, OsPrefix: b.OSPrefix,
		RangeStart: b.RangeStart, RangeEnd: b.RangeEnd,
		FirstNumber: FormatNumber(b.OSPrefix, b.RangeStart), LastNumber: FormatNumber(b.OSPrefix, b.RangeEnd),
	}, nil
}

// ---- mapping ----------------------------------------------------------------------

func (h *Handler) session(s *Session) gen.Session {
	return gen.Session{
		AccessToken:      s.AccessToken,
		TokenType:        "Bearer",
		ExpiresIn:        int(time.Until(s.AccessExpiresAt).Round(time.Second).Seconds()),
		RefreshToken:     s.RefreshToken,
		RefreshExpiresAt: s.RefreshExpiresAt,
		DeviceId:         s.DeviceID,
		User:             toGenUser(s.User),
		Account:          toGenAccount(s.Account),
	}
}

func toGenAccount(a Account) gen.Account {
	return gen.Account{
		Id: a.ID, TradeName: a.TradeName, Document: a.Document, Niche: a.Niche,
		Status: gen.AccountStatus(a.Status), TrialEndsAt: a.TrialEndsAt,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, Version: a.Version,
	}
}

func toGenUser(u UserView) gen.User {
	grants := make([]gen.Grant, len(u.Grants))
	for i, g := range u.Grants {
		grants[i] = gen.Grant{BranchId: g.BranchID, Role: gen.Role(g.Role)}
	}
	return gen.User{
		Id: u.ID, Name: u.Name, Email: u.Email, Status: gen.UserStatus(u.Status), MfaEnabled: u.MFAEnabled,
		LastLoginAt: u.LastLoginAt, Grants: grants, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, Version: u.Version,
	}
}

func toGenBranches(bs []Branch) []gen.Branch {
	out := make([]gen.Branch, len(bs))
	for i, b := range bs {
		out[i] = toGenBranch(b)
	}
	return out
}

func toGenBranch(b Branch) gen.Branch {
	return gen.Branch{
		Id: b.ID, Name: b.Name, OsPrefix: b.OSPrefix, Timezone: b.Timezone, Settings: toGenSettings(b.Settings.Data()),
		CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt, Version: b.Version,
	}
}

func toGenSettings(s BranchSettings) gen.BranchSettings {
	days := s.PickupReminderDays
	if days == nil {
		days = []int{}
	}
	return gen.BranchSettings{
		DiagnosticFee: gen.DiagnosticFee{
			Enabled: s.DiagnosticFee.Enabled, AmountCents: s.DiagnosticFee.AmountCents,
			DeductOnApproval: s.DiagnosticFee.DeductOnApproval,
		},
		QuoteValidityDays:           s.QuoteValidityDays,
		DefaultWarrantyDays:         s.DefaultWarrantyDays,
		PickupReminderDays:          days,
		AbandonmentDays:             s.AbandonmentDays,
		StorageFeeEnabled:           s.StorageFeeEnabled,
		AllowNegativeStock:          s.AllowNegativeStock,
		TechnicianCanOpenAndDeliver: s.TechnicianCanOpenAndDeliver,
		AttendantMaxDiscountPercent: s.AttendantMaxDiscountPercent,
		Terms:                       s.Terms,
	}
}

func fromGenSettings(s gen.BranchSettings) BranchSettings {
	return BranchSettings{
		DiagnosticFee: DiagnosticFee{
			Enabled: s.DiagnosticFee.Enabled, AmountCents: s.DiagnosticFee.AmountCents,
			DeductOnApproval: s.DiagnosticFee.DeductOnApproval,
		},
		QuoteValidityDays:           s.QuoteValidityDays,
		DefaultWarrantyDays:         s.DefaultWarrantyDays,
		PickupReminderDays:          s.PickupReminderDays,
		AbandonmentDays:             s.AbandonmentDays,
		StorageFeeEnabled:           s.StorageFeeEnabled,
		AllowNegativeStock:          s.AllowNegativeStock,
		TechnicianCanOpenAndDeliver: s.TechnicianCanOpenAndDeliver,
		AttendantMaxDiscountPercent: s.AttendantMaxDiscountPercent,
		Terms:                       s.Terms,
	}
}

func fromGenGrants(gs []gen.Grant) []GrantInput {
	out := make([]GrantInput, len(gs))
	for i, g := range gs {
		out[i] = GrantInput{BranchID: g.BranchId, Role: string(g.Role)}
	}
	return out
}

func toGenDevice(d Device) gen.Device {
	return gen.Device{
		Id: d.ID, BranchId: d.BranchID, Name: d.Name, Platform: gen.DevicePlatform(d.Platform),
		LastSeenAt: d.LastSeenAt, RevokedAt: d.RevokedAt, CreatedAt: d.CreatedAt,
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
