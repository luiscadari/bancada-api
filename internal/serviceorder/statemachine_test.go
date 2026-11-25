package serviceorder

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"github.com/luiscadari/bancada-api/internal/identity"
	"github.com/luiscadari/bancada-api/internal/platform/actor"
)

// TestFlow checks the status graph against the table of section 8.
func TestFlow(t *testing.T) {
	all := []string{StatusReceived, StatusDiagnosing, StatusAwaitingApproval, StatusApproved, StatusInProgress,
		StatusAwaitingPart, StatusTesting, StatusReady, StatusAwaitingPickup, StatusDelivered, StatusAbandoned, StatusCanceled}
	allowed := map[[2]string]bool{
		{StatusReceived, StatusDiagnosing}: true, {StatusReceived, StatusCanceled}: true,
		{StatusDiagnosing, StatusAwaitingApproval}: true, {StatusDiagnosing, StatusAwaitingPickup}: true,
		{StatusDiagnosing, StatusCanceled}:       true,
		{StatusAwaitingApproval, StatusApproved}: true, {StatusAwaitingApproval, StatusAwaitingPickup}: true,
		{StatusAwaitingApproval, StatusCanceled}: true,
		{StatusApproved, StatusInProgress}:       true, {StatusApproved, StatusAwaitingApproval}: true,
		{StatusInProgress, StatusAwaitingPart}: true, {StatusInProgress, StatusTesting}: true,
		{StatusInProgress, StatusAwaitingApproval}: true,
		{StatusAwaitingPart, StatusInProgress}:     true,
		{StatusTesting, StatusReady}:               true, {StatusTesting, StatusInProgress}: true,
		{StatusReady, StatusDelivered}: true, {StatusReady, StatusAbandoned}: true,
		{StatusAwaitingPickup, StatusDelivered}: true, {StatusAwaitingPickup, StatusAbandoned}: true,
		{StatusAbandoned, StatusDelivered}: true,
	}
	for _, from := range all {
		for _, to := range all {
			assert.Equal(t, allowed[[2]string{from, to}], CanMove(from, to), "%s → %s", from, to)
		}
	}
	for move := range manualMoves {
		assert.True(t, CanMove(move[0], move[1]), "manual move %v must follow the flow", move)
	}
	assert.True(t, IsFinal(StatusDelivered))
	assert.True(t, IsFinal(StatusCanceled))
	assert.False(t, IsFinal(StatusReady))
}

func TestCheckDiscount(t *testing.T) {
	branch := uuid.New()
	as := func(role string) *actor.Actor {
		return &actor.Actor{BranchID: &branch, Grants: []actor.Grant{{BranchID: &branch, Role: role}}}
	}
	settings := identity.DefaultBranchSettings() // attendants up to 10%
	item := func(qty string, list, unit, discount int64) QuoteItem {
		q := decimal.RequireFromString(qty)
		return QuoteItem{Quantity: q, ListPriceCents: list, UnitPriceCents: unit, DiscountCents: discount,
			TotalCents: cents(q, unit) - discount}
	}
	cases := []struct {
		name string
		role string
		item QuoteItem
		want error
	}{
		{"no discount, technician", actor.RoleTechnician, item("1", 10000, 10000, 0), nil},
		{"any discount, technician", actor.RoleTechnician, item("1", 10000, 10000, 1), errDiscountForbidden},
		{"10% attendant", actor.RoleAttendant, item("2", 10000, 10000, 2000), nil},
		{"10.01% attendant", actor.RoleAttendant, item("1", 10000, 10000, 1001), errAny},
		{"lower price attendant", actor.RoleAttendant, item("1", 10000, 8000, 0), errAny},
		{"higher price attendant", actor.RoleAttendant, item("1", 10000, 12000, 0), nil},
		{"90% owner", actor.RoleOwner, item("1", 10000, 10000, 9000), nil},
	}
	for _, c := range cases {
		err := checkDiscount(as(c.role), settings, c.item)
		switch c.want {
		case nil:
			assert.NoError(t, err, c.name)
		case errAny:
			assert.Error(t, err, c.name)
		default:
			assert.Equal(t, c.want, err, c.name)
		}
	}
}

var errAny = &struct{ error }{}

func TestMoneyHelpers(t *testing.T) {
	assert.Equal(t, "R$ 0,05", brl(5))
	assert.Equal(t, "R$ 300,00", brl(30000))
	assert.Equal(t, "R$ 1.234.567,89", brl(123456789))
	assert.Equal(t, "-R$ 1.000,00", brl(-100000))
	assert.EqualValues(t, 42, cents(decimal.RequireFromString("0.125"), 333)) // 41.625 rounds up
	assert.EqualValues(t, 15000, cents(decimal.RequireFromString("1.5"), 10000))
	sub, disc, total := totals([]QuoteItem{{TotalCents: 900, DiscountCents: 100}, {TotalCents: 500}})
	assert.Equal(t, [3]int64{1500, 100, 1400}, [3]int64{sub, disc, total})
}
