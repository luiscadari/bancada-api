package serviceorder

import (
	"slices"

	"github.com/luiscadari/bancada-api/internal/platform/actor"
)

// Order statuses (section 8).
const (
	StatusReceived         = "received"
	StatusDiagnosing       = "diagnosing"
	StatusAwaitingApproval = "awaiting_approval"
	StatusApproved         = "approved"
	StatusInProgress       = "in_progress"
	StatusAwaitingPart     = "awaiting_part"
	StatusTesting          = "testing"
	StatusReady            = "ready"
	StatusAwaitingPickup   = "awaiting_pickup"
	StatusDelivered        = "delivered"
	StatusAbandoned        = "abandoned"
	StatusCanceled         = "canceled"
)

// flow is the status graph of section 8. approved → awaiting_approval is the new quote
// of RN-11 before the execution starts.
var flow = map[string][]string{
	StatusReceived:         {StatusDiagnosing, StatusCanceled},
	StatusDiagnosing:       {StatusAwaitingApproval, StatusAwaitingPickup, StatusCanceled},
	StatusAwaitingApproval: {StatusApproved, StatusAwaitingPickup, StatusCanceled},
	StatusApproved:         {StatusInProgress, StatusAwaitingApproval},
	StatusInProgress:       {StatusAwaitingPart, StatusTesting, StatusAwaitingApproval},
	StatusAwaitingPart:     {StatusInProgress},
	StatusTesting:          {StatusReady, StatusInProgress},
	StatusReady:            {StatusDelivered, StatusAbandoned},
	StatusAwaitingPickup:   {StatusDelivered, StatusAbandoned},
	StatusAbandoned:        {StatusDelivered},
	StatusDelivered:        nil,
	StatusCanceled:         nil,
}

// CanMove reports whether the status graph allows from → to.
func CanMove(from, to string) bool {
	return slices.Contains(flow[from], to)
}

// IsFinal reports whether no further work can happen on an order in status.
func IsFinal(status string) bool {
	return status == StatusDelivered || status == StatusCanceled || status == StatusAbandoned
}

var (
	benchRoles   = []string{actor.RoleOwner, actor.RoleAttendant, actor.RoleTechnician}
	technicianUp = []string{actor.RoleOwner, actor.RoleTechnician}
)

// manualMoves are the transitions done through the generic transition operation, with
// the roles that may perform them ("Quem muda" in section 8; owners may do everything).
// Diagnosis is open to every role (permission matrix: "Diagnosticar e montar orçamento").
// Quotes, delivery, cancelation and abandonment have their own operations.
var manualMoves = map[[2]string][]string{
	{StatusReceived, StatusDiagnosing}:       benchRoles,
	{StatusDiagnosing, StatusAwaitingPickup}: benchRoles,
	{StatusApproved, StatusInProgress}:       technicianUp,
	{StatusInProgress, StatusAwaitingPart}:   technicianUp,
	{StatusInProgress, StatusTesting}:        technicianUp,
	{StatusAwaitingPart, StatusInProgress}:   technicianUp,
	{StatusTesting, StatusReady}:             technicianUp,
	{StatusTesting, StatusInProgress}:        technicianUp,
}

// quoteStatuses are the order statuses in which a quote can be drafted and sent.
var quoteStatuses = []string{StatusDiagnosing, StatusAwaitingApproval, StatusApproved, StatusInProgress}
