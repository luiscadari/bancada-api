package api

import (
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/httpx"
)

const (
	owner      = actor.RoleOwner
	attendant  = actor.RoleAttendant
	technician = actor.RoleTechnician
)

var (
	everyone       = []string{owner, attendant, technician}
	ownerOnly      = []string{owner}
	ownerAttendant = []string{owner, attendant}
)

// operationRoles is the permission matrix of the requirements (section 3), per API
// operation. Finer rules that depend on data (assigned technician, discount limit,
// cancel reason) are enforced by the services. Every operation that is not public must
// be listed here; BuildPolicies fails at startup otherwise.
var operationRoles = map[string][]string{
	// identity
	"getMe":                everyone,
	"getAccount":           everyone,
	"updateAccount":        ownerOnly,
	"listBranches":         everyone,
	"getBranch":            everyone,
	"createBranch":         ownerOnly,
	"updateBranch":         ownerOnly,
	"updateBranchSettings": ownerOnly,
	"listUsers":            ownerOnly,
	"getUser":              ownerOnly,
	"inviteUser":           ownerOnly,
	"updateUserGrants":     ownerOnly,
	"disableUser":          ownerOnly,
	"enableUser":           ownerOnly,
	"listDevices":          ownerOnly,
	"revokeDevice":         ownerOnly,
	"reserveNumberBlock":   everyone,

	// registry: shared by every branch (RN-02). The counter opens orders, so it registers
	// customers and equipment; configuration of the catalog belongs to the owner (UC-13).
	"listCustomers":       everyone,
	"getCustomer":         everyone,
	"createCustomer":      ownerAttendant,
	"updateCustomer":      ownerAttendant,
	"createContact":       ownerAttendant,
	"updateContact":       ownerAttendant,
	"deleteContact":       ownerAttendant,
	"createCostCenter":    ownerAttendant,
	"updateCostCenter":    ownerAttendant,
	"listEquipmentTypes":  everyone,
	"getEquipmentType":    everyone,
	"createEquipmentType": ownerOnly,
	"updateEquipmentType": ownerOnly,
	"listEquipment":       everyone,
	"getEquipment":        everyone,
	"createEquipment":     ownerAttendant,
	"updateEquipment":     ownerAttendant,
	"listServices":        everyone,
	"getService":          everyone,
	"createService":       ownerOnly,
	"updateService":       ownerOnly,
	"listProducts":        everyone,
	"getProduct":          everyone,
	"createProduct":       ownerOnly,
	"updateProduct":       ownerOnly,
	"listSuppliers":       ownerOnly,
	"getSupplier":         ownerOnly,
	"createSupplier":      ownerOnly,
	"updateSupplier":      ownerOnly,
	"applyNichePack":      ownerOnly,
}

// BuildPolicies derives the protection of every operation from the contract: operations
// with `security: []` are public and operations declaring the X-Branch-ID header are
// branch-scoped. Roles come from operationRoles.
func BuildPolicies(spec *openapi3.T) (httpx.Policies, error) {
	roleTable := make(map[string][]string, len(operationRoles))
	for id, roles := range operationRoles {
		roleTable[strings.ToLower(id)] = roles
	}
	policies := httpx.Policies{}
	var missing []string
	for _, path := range spec.Paths.InMatchingOrder() {
		for _, op := range spec.Paths.Value(path).Operations() {
			id := op.OperationID
			public := op.Security != nil && len(*op.Security) == 0
			p := httpx.Operation{Public: public, Branch: hasBranchHeader(op)}
			if !public {
				roles, ok := roleTable[strings.ToLower(id)]
				if !ok {
					missing = append(missing, id)
				}
				p.Roles = roles
			}
			policies[strings.ToLower(id)] = p
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("operations without a role policy: %s", strings.Join(missing, ", "))
	}
	return policies, nil
}

func hasBranchHeader(op *openapi3.Operation) bool {
	for _, p := range op.Parameters {
		if p.Value != nil && p.Value.In == openapi3.ParameterInHeader &&
			strings.EqualFold(p.Value.Name, "X-Branch-ID") && p.Value.Required {
			return true
		}
	}
	return false
}
