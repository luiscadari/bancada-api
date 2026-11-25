// Package actor carries the authenticated principal of a request through the context.
package actor

import (
	"context"
	"slices"

	"github.com/google/uuid"
)

// Role codes. The permission matrix of the requirements (section 3) is expressed in terms of them.
const (
	RoleOwner      = "owner"
	RoleAttendant  = "attendant"
	RoleTechnician = "technician"
)

// Grant is a role held by a user. A nil BranchID means the grant covers every branch of
// the account (used for owners).
type Grant struct {
	BranchID *uuid.UUID
	Role     string
}

// Actor is who performs the current request.
type Actor struct {
	AccountID uuid.UUID
	UserID    uuid.UUID
	DeviceID  *uuid.UUID
	// BranchID is the branch the request acts on (X-Branch-ID), when given.
	BranchID  *uuid.UUID
	Grants    []Grant
	IP        string
	RequestID string
}

type ctxKey struct{}

func With(ctx context.Context, a *Actor) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

// From returns the actor stored in ctx, if any.
func From(ctx context.Context) (*Actor, bool) {
	a, ok := ctx.Value(ctxKey{}).(*Actor)
	return a, ok && a != nil
}

// IsOwner reports whether the actor owns the account (all branches).
func (a *Actor) IsOwner() bool {
	for _, g := range a.Grants {
		if g.Role == RoleOwner {
			return true
		}
	}
	return false
}

// RolesIn returns the roles the actor holds in a branch. Owners hold "owner" everywhere.
func (a *Actor) RolesIn(branchID uuid.UUID) []string {
	var roles []string
	for _, g := range a.Grants {
		if g.BranchID == nil || *g.BranchID == branchID {
			roles = append(roles, g.Role)
		}
	}
	return roles
}

// HasAnyRole reports whether the actor holds one of roles, in the current branch when
// the request is branch-scoped, or in any branch otherwise.
func (a *Actor) HasAnyRole(roles ...string) bool {
	var held []string
	if a.BranchID != nil {
		held = a.RolesIn(*a.BranchID)
	} else {
		for _, g := range a.Grants {
			held = append(held, g.Role)
		}
	}
	for _, r := range held {
		if slices.Contains(roles, r) {
			return true
		}
	}
	return false
}

// CanAccessBranch reports whether the actor has any role in the branch.
func (a *Actor) CanAccessBranch(branchID uuid.UUID) bool {
	return len(a.RolesIn(branchID)) > 0
}
