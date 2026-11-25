package registry_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luiscadari/bancada-api/internal/apptest"
	"github.com/luiscadari/bancada-api/internal/platform/actor"
)

var env *apptest.Env

func TestMain(m *testing.M) { apptest.Main(m, &env) }

const (
	validCPF  = "529.982.247-25"
	validCNPJ = "11.222.333/0001-81"
)

func newID() string { return uuid.Must(uuid.NewV7()).String() }

func TestSignupAppliesNichePack(t *testing.T) {
	tn := apptest.Signup(t, env)
	types := tn.Do(t, http.MethodGet, "/v1/equipment-types", nil).Items(t)
	names := map[string]map[string]any{}
	for _, ty := range types {
		names[ty["name"].(string)] = ty
	}
	require.Contains(t, names, "Impressora")
	require.Contains(t, names, "Notebook")
	require.Contains(t, names, "Desktop")
	printer := names["Impressora"]
	assert.NotEmpty(t, printer["checklist"])
	schema := printer["field_schema"].(map[string]any)
	assert.Contains(t, schema["properties"].(map[string]any), "page_counter")

	services := tn.Do(t, http.MethodGet, "/v1/services?limit=200", nil).Items(t)
	assert.GreaterOrEqual(t, len(services), 5)

	// Applying again adds nothing.
	res := tn.Do(t, http.MethodPost, "/v1/niche-packs/it_repair/apply", nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.EqualValues(t, 0, res.JSON(t)["equipment_types_added"])
	assert.EqualValues(t, 0, res.JSON(t)["services_added"])

	empty := apptest.SignupWith(t, env, false)
	assert.Empty(t, empty.Do(t, http.MethodGet, "/v1/equipment-types", nil).Items(t))
	res = empty.Do(t, http.MethodPost, "/v1/niche-packs/it_repair/apply", nil)
	assert.EqualValues(t, 3, res.JSON(t)["equipment_types_added"])
}

func TestCustomerRegistrationAndSearch(t *testing.T) {
	tn := apptest.Signup(t, env)
	id := newID()
	body := map[string]any{
		"id": id, "type": "individual", "name": "João da Silva", "document": validCPF,
		"consents": map[string]any{"whatsapp": true},
		"contacts": []map[string]any{
			{"name": "João", "phone": "(11) 99876-5432", "email": "Joao@Example.com"},
			{"name": "Maria (esposa)", "phone": "11 3333-4444"},
		},
	}
	res := tn.Do(t, http.MethodPost, "/v1/customers", body)
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	c := res.JSON(t)
	assert.Equal(t, "52998224725", c["document"])
	assert.Equal(t, "contract", c["legal_basis"])
	consents := c["consents"].(map[string]any)
	assert.Equal(t, true, consents["whatsapp"].(map[string]any)["granted"])
	assert.NotNil(t, consents["whatsapp"].(map[string]any)["updated_at"])
	assert.Equal(t, false, consents["email"].(map[string]any)["granted"])
	contacts := c["contacts"].([]any)
	require.Len(t, contacts, 2)
	assert.Equal(t, true, contacts[0].(map[string]any)["is_primary"], "first contact becomes primary")
	assert.Equal(t, "joao@example.com", contacts[0].(map[string]any)["email"])

	// Retrying the same create (offline resend) returns the same customer.
	res = tn.Do(t, http.MethodPost, "/v1/customers", body)
	assert.Equal(t, http.StatusCreated, res.Status)
	assert.Equal(t, id, res.JSON(t)["id"])

	for _, q := range []string{"joao", "SILVA", "98765", "529982", "joao@example.com", "33334444"} {
		items := tn.Do(t, http.MethodGet, "/v1/customers?q="+url.QueryEscape(q), nil).Items(t)
		require.Len(t, items, 1, "search %q", q)
		assert.Equal(t, id, items[0]["id"])
	}
	assert.Empty(t, tn.Do(t, http.MethodGet, "/v1/customers?q=pedro", nil).Items(t))

	// Document rules.
	res = tn.Do(t, http.MethodPost, "/v1/customers", map[string]any{"type": "individual", "name": "Outro", "document": validCPF})
	assert.Equal(t, http.StatusConflict, res.Status)
	assert.Equal(t, "customer_document_taken", res.Code(t))
	res = tn.Do(t, http.MethodPost, "/v1/customers", map[string]any{"type": "individual", "name": "X", "document": "123.456.789-00"})
	assert.Equal(t, http.StatusBadRequest, res.Status)
	res = tn.Do(t, http.MethodPost, "/v1/customers", map[string]any{"type": "company", "name": "X", "document": validCPF})
	assert.Equal(t, http.StatusBadRequest, res.Status, "a CPF is not a valid CNPJ")

	// Update: clear the document with an empty string, revoke the WhatsApp consent.
	res = tn.Do(t, http.MethodPatch, "/v1/customers/"+id, map[string]any{"document": "", "consents": map[string]any{"whatsapp": false}})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Nil(t, res.JSON(t)["document"])
	assert.Equal(t, false, res.JSON(t)["consents"].(map[string]any)["whatsapp"].(map[string]any)["granted"])
}

func TestCompanyContactsAndCostCenters(t *testing.T) {
	tn := apptest.Signup(t, env)
	res := tn.Do(t, http.MethodPost, "/v1/customers", map[string]any{
		"type": "company", "name": "Escritório Contábil Ltda", "document": validCNPJ,
		"contacts": []map[string]any{
			{"name": "Recepção", "phone": "1130000000", "is_requester": true},
			{"name": "Diretora", "email": "diretora@empresa.com", "is_approver": true, "approval_limit_cents": 50000, "is_primary": true},
		},
	})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	company := res.JSON(t)
	companyID := company["id"].(string)
	contacts := company["contacts"].([]any)
	assert.Equal(t, "Diretora", contacts[0].(map[string]any)["name"], "primary first")
	approver := contacts[0].(map[string]any)
	assert.EqualValues(t, 50000, approver["approval_limit_cents"])

	res = tn.Do(t, http.MethodPost, "/v1/customers/"+companyID+"/cost-centers", map[string]any{"name": "Financeiro", "code": "FIN"})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	ccID := res.JSON(t)["id"].(string)
	res = tn.Do(t, http.MethodPatch, "/v1/customers/"+companyID+"/cost-centers/"+ccID, map[string]any{"active": false})
	assert.Equal(t, false, res.JSON(t)["active"])

	// Switching the primary contact; removing the approval limit.
	reception := contacts[1].(map[string]any)["id"].(string)
	res = tn.Do(t, http.MethodPatch, "/v1/customers/"+companyID+"/contacts/"+reception, map[string]any{"is_primary": true})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	res = tn.Do(t, http.MethodPatch, "/v1/customers/"+companyID+"/contacts/"+approver["id"].(string), map[string]any{"approval_limit_cents": -1})
	assert.Nil(t, res.JSON(t)["approval_limit_cents"])
	got := tn.Do(t, http.MethodGet, "/v1/customers/"+companyID, nil).JSON(t)
	primaries := 0
	for _, c := range got["contacts"].([]any) {
		if c.(map[string]any)["is_primary"] == true {
			primaries++
			assert.Equal(t, reception, c.(map[string]any)["id"])
		}
	}
	assert.Equal(t, 1, primaries)

	res = tn.Do(t, http.MethodDelete, "/v1/customers/"+companyID+"/contacts/"+reception, nil)
	assert.Equal(t, http.StatusNoContent, res.Status)
	assert.Len(t, tn.Do(t, http.MethodGet, "/v1/customers/"+companyID, nil).JSON(t)["contacts"], 1)

	// Cost centers are for companies only.
	person := tn.Do(t, http.MethodPost, "/v1/customers", map[string]any{"type": "individual", "name": "Pessoa"}).JSON(t)
	res = tn.Do(t, http.MethodPost, "/v1/customers/"+person["id"].(string)+"/cost-centers", map[string]any{"name": "X"})
	assert.Equal(t, http.StatusUnprocessableEntity, res.Status)
	assert.Equal(t, "cost_center_requires_company", res.Code(t))
}

func TestEquipmentTypesValidateAttributes(t *testing.T) {
	tn := apptest.SignupWith(t, env, false)
	res := tn.Do(t, http.MethodPost, "/v1/equipment-types", map[string]any{
		"name": "Celular",
		"fields": []map[string]any{
			{"key": "imei", "label": "IMEI", "kind": "text", "required": true},
			{"key": "storage_gb", "label": "Armazenamento", "kind": "integer", "required": false},
			{"key": "color", "label": "Cor", "kind": "select", "required": false, "options": []string{"Preto", "Branco"}},
			{"key": "purchased_on", "label": "Comprado em", "kind": "date", "required": false},
		},
		"checklist": []map[string]any{{"key": "sim_tray", "label": "Gaveta do chip", "kind": "check", "required": true}},
	})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	typeID := res.JSON(t)["id"].(string)
	customer := tn.Do(t, http.MethodPost, "/v1/customers", map[string]any{"type": "individual", "name": "Ana"}).JSON(t)
	customerID := customer["id"].(string)

	create := func(attrs map[string]any) apptest.Response {
		return tn.Do(t, http.MethodPost, "/v1/equipment", map[string]any{
			"customer_id": customerID, "type_id": typeID, "brand": "Moto", "model": "G", "serial_number": "SN-ABC-1",
			"attributes": attrs,
		})
	}
	for name, attrs := range map[string]map[string]any{
		"missing required": {"storage_gb": 64},
		"wrong type":       {"imei": "1", "storage_gb": "64"},
		"not an option":    {"imei": "1", "color": "Roxo"},
		"bad date":         {"imei": "1", "purchased_on": "31/12/2025"},
		"unknown field":    {"imei": "1", "voltage": 110},
	} {
		res := create(attrs)
		assert.Equal(t, http.StatusBadRequest, res.Status, name)
		assert.NotEmpty(t, res.JSON(t)["errors"], name)
	}
	res = create(map[string]any{"imei": "356938035643809", "storage_gb": 128, "color": "Preto", "purchased_on": "2025-12-31"})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	equipmentID := res.JSON(t)["id"].(string)

	items := tn.Do(t, http.MethodGet, "/v1/equipment?serial_number=sn-abc-1", nil).Items(t)
	require.Len(t, items, 1)
	assert.Equal(t, equipmentID, items[0]["id"])
	assert.Len(t, tn.Do(t, http.MethodGet, "/v1/equipment?customer_id="+customerID, nil).Items(t), 1)
	assert.Len(t, tn.Do(t, http.MethodGet, "/v1/equipment?q=abc", nil).Items(t), 1)

	res = tn.Do(t, http.MethodPatch, "/v1/equipment/"+equipmentID, map[string]any{"attributes": map[string]any{"storage_gb": 1}})
	assert.Equal(t, http.StatusBadRequest, res.Status, "replacing attributes revalidates them")
	res = tn.Do(t, http.MethodPatch, "/v1/equipment/"+equipmentID, map[string]any{"serial_number": ""})
	require.Equal(t, http.StatusOK, res.Status)
	assert.Nil(t, res.JSON(t)["serial_number"])

	// Type definition rules.
	res = tn.Do(t, http.MethodPost, "/v1/equipment-types", map[string]any{"name": "celular", "fields": []any{}, "checklist": []any{}})
	assert.Equal(t, http.StatusConflict, res.Status, "names are unique ignoring case")
	res = tn.Do(t, http.MethodPost, "/v1/equipment-types", map[string]any{"name": "X", "checklist": []any{},
		"fields": []map[string]any{{"key": "a", "label": "A", "kind": "text", "required": false}, {"key": "a", "label": "B", "kind": "text", "required": false}}})
	assert.Equal(t, http.StatusBadRequest, res.Status)
	res = tn.Do(t, http.MethodPost, "/v1/equipment-types", map[string]any{"name": "Y", "checklist": []any{},
		"fields": []map[string]any{{"key": "c", "label": "C", "kind": "select", "required": false}}})
	assert.Equal(t, http.StatusBadRequest, res.Status, "select needs options")

	// Deactivated types cannot receive new equipment.
	res = tn.Do(t, http.MethodPut, "/v1/equipment-types/"+typeID, map[string]any{"name": "Celular", "active": false, "fields": []any{}, "checklist": []any{}})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Empty(t, res.JSON(t)["fields"])
	res = create(map[string]any{})
	assert.Equal(t, http.StatusUnprocessableEntity, res.Status)
	assert.Equal(t, "equipment_type_inactive", res.Code(t))
}

func TestCatalogAndCostVisibility(t *testing.T) {
	tn := apptest.SignupWith(t, env, false)
	res := tn.Do(t, http.MethodPost, "/v1/services", map[string]any{"name": "Troca de fusor", "price_cents": 25000, "commission_percent": 12.5})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	svc := res.JSON(t)
	assert.EqualValues(t, 90, svc["warranty_days"], "RN-18 default")
	assert.EqualValues(t, 12.5, svc["commission_percent"])

	res = tn.Do(t, http.MethodPost, "/v1/products", map[string]any{
		"name": "Fusor HP M402", "sku": "FUS-402", "barcode": "7890000000017", "cost_cents": 18000, "price_cents": 32000, "min_stock": 2,
	})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	productID := res.JSON(t)["id"].(string)
	assert.EqualValues(t, 18000, res.JSON(t)["cost_cents"])
	assert.Equal(t, "un", res.JSON(t)["unit"])

	res = tn.Do(t, http.MethodPost, "/v1/products", map[string]any{"name": "Outro", "sku": "fus-402", "price_cents": 1})
	assert.Equal(t, http.StatusConflict, res.Status)
	assert.Equal(t, "product_sku_taken", res.Code(t))

	for _, q := range []string{"fusor", "FUS-402", "7890000000017"} {
		assert.Len(t, tn.Do(t, http.MethodGet, "/v1/products?q="+q, nil).Items(t), 1, q)
	}

	// Attendants see prices but never costs; technicians cannot change the catalog.
	attendant, _ := tn.Invite(t, actor.RoleAttendant)
	p := attendant.Do(t, http.MethodGet, "/v1/products/"+productID, nil).JSON(t)
	assert.Nil(t, p["cost_cents"])
	assert.EqualValues(t, 32000, p["price_cents"])
	assert.Nil(t, attendant.Do(t, http.MethodGet, "/v1/products", nil).Items(t)[0]["cost_cents"])
	assert.Equal(t, http.StatusForbidden, attendant.Do(t, http.MethodPost, "/v1/services", map[string]any{"name": "X", "price_cents": 1}).Status)
	assert.Equal(t, http.StatusForbidden, attendant.Do(t, http.MethodGet, "/v1/suppliers", nil).Status)

	tech, _ := tn.Invite(t, actor.RoleTechnician)
	assert.Equal(t, http.StatusOK, tech.Do(t, http.MethodGet, "/v1/customers", nil).Status)
	assert.Equal(t, http.StatusForbidden, tech.Do(t, http.MethodPost, "/v1/customers", map[string]any{"type": "individual", "name": "X"}).Status)

	// Deactivating instead of deleting; filtering by active.
	res = tn.Do(t, http.MethodPatch, "/v1/products/"+productID, map[string]any{"active": false, "sku": ""})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Nil(t, res.JSON(t)["sku"])
	assert.Empty(t, tn.Do(t, http.MethodGet, "/v1/products?active=true", nil).Items(t))
	assert.Len(t, tn.Do(t, http.MethodGet, "/v1/products?active=false", nil).Items(t), 1)

	res = tn.Do(t, http.MethodPost, "/v1/suppliers", map[string]any{"name": "Distribuidora Peças", "document": "11.222.333/0001-81", "email": "Vendas@Dist.com"})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	assert.Equal(t, "11222333000181", res.JSON(t)["document"])
	assert.Equal(t, "vendas@dist.com", res.JSON(t)["email"])
}

func TestPagination(t *testing.T) {
	tn := apptest.SignupWith(t, env, false)
	for i := 0; i < 5; i++ {
		res := tn.Do(t, http.MethodPost, "/v1/services", map[string]any{"name": "Serviço " + apptest.Unique(), "price_cents": 100})
		require.Equal(t, http.StatusCreated, res.Status)
	}
	seen := map[string]bool{}
	path := "/v1/services?limit=2"
	pages := 0
	for {
		res := tn.Do(t, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		pages++
		for _, it := range res.Items(t) {
			seen[it["id"].(string)] = true
		}
		next, _ := res.JSON(t)["next_cursor"].(string)
		if next == "" {
			break
		}
		path = "/v1/services?limit=2&cursor=" + next
	}
	assert.Equal(t, 3, pages)
	assert.Len(t, seen, 5)
	assert.Equal(t, http.StatusBadRequest, tn.Do(t, http.MethodGet, "/v1/services?cursor=%21%21", nil).Status)
}

// ADR-01: registry data never crosses accounts.
func TestRegistryTenantIsolation(t *testing.T) {
	a := apptest.Signup(t, env)
	b := apptest.Signup(t, env)
	customer := a.Do(t, http.MethodPost, "/v1/customers", map[string]any{"type": "individual", "name": "Cliente Exclusivo da Loja A",
		"contacts": []map[string]any{{"name": "C", "phone": "11912345678"}}}).JSON(t)
	customerID := customer["id"].(string)
	types := a.Do(t, http.MethodGet, "/v1/equipment-types", nil).Items(t)
	typeID := types[0]["id"].(string)

	assert.Equal(t, http.StatusNotFound, b.Do(t, http.MethodGet, "/v1/customers/"+customerID, nil).Status)
	assert.Empty(t, b.Do(t, http.MethodGet, "/v1/customers?q=Exclusivo", nil).Items(t))
	assert.Empty(t, b.Do(t, http.MethodGet, "/v1/customers?q=912345678", nil).Items(t))
	assert.Equal(t, http.StatusNotFound, b.Do(t, http.MethodPatch, "/v1/customers/"+customerID, map[string]any{"name": "X"}).Status)
	assert.Equal(t, http.StatusNotFound, b.Do(t, http.MethodGet, "/v1/equipment-types/"+typeID, nil).Status)

	// B cannot attach equipment to A's customer or use A's equipment type.
	bTypes := b.Do(t, http.MethodGet, "/v1/equipment-types", nil).Items(t)
	res := b.Do(t, http.MethodPost, "/v1/equipment", map[string]any{"customer_id": customerID, "type_id": bTypes[0]["id"]})
	assert.Equal(t, http.StatusNotFound, res.Status)
	bCustomer := b.Do(t, http.MethodPost, "/v1/customers", map[string]any{"type": "individual", "name": "B"}).JSON(t)
	res = b.Do(t, http.MethodPost, "/v1/equipment", map[string]any{"customer_id": bCustomer["id"], "type_id": typeID})
	assert.Equal(t, http.StatusNotFound, res.Status)

	// Reusing a client id that exists in another account does not leak or overwrite it.
	res = b.Do(t, http.MethodPost, "/v1/customers", map[string]any{"id": customerID, "type": "individual", "name": "Sequestro"})
	assert.Equal(t, http.StatusConflict, res.Status)
	assert.Equal(t, "Cliente Exclusivo da Loja A", a.Do(t, http.MethodGet, "/v1/customers/"+customerID, nil).JSON(t)["name"])
}

func TestCatalogUpdatesAndSuppliers(t *testing.T) {
	tn := apptest.SignupWith(t, env, false)
	svc := tn.Do(t, http.MethodPost, "/v1/services", map[string]any{"name": "Limpeza", "price_cents": 8000}).JSON(t)
	svcID := svc["id"].(string)
	res := tn.Do(t, http.MethodPatch, "/v1/services/"+svcID, map[string]any{
		"price_cents": 9000, "warranty_days": 30, "commission_percent": 5, "active": false, "description": "Interna",
	})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	got := tn.Do(t, http.MethodGet, "/v1/services/"+svcID, nil).JSON(t)
	assert.EqualValues(t, 9000, got["price_cents"])
	assert.EqualValues(t, 30, got["warranty_days"])
	assert.Equal(t, false, got["active"])
	assert.Equal(t, "Interna", got["description"])
	assert.Len(t, tn.Do(t, http.MethodGet, "/v1/services?active=true", nil).Items(t), 0)
	assert.Equal(t, http.StatusNotFound, tn.Do(t, http.MethodGet, "/v1/services/"+newID(), nil).Status)

	res = tn.Do(t, http.MethodPost, "/v1/suppliers", map[string]any{
		"name": "Distribuidora Peças", "document": validCNPJ, "email": "vendas@pecas.com", "phone": "(11) 4000-0000",
	})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	supID := res.JSON(t)["id"].(string)
	res = tn.Do(t, http.MethodPatch, "/v1/suppliers/"+supID, map[string]any{"contact_name": "Carlos", "notes": ""})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, "Carlos", res.JSON(t)["contact_name"])
	assert.Equal(t, "Distribuidora Peças", tn.Do(t, http.MethodGet, "/v1/suppliers/"+supID, nil).JSON(t)["name"])
	assert.Len(t, tn.Do(t, http.MethodGet, "/v1/suppliers?q=distribuidora", nil).Items(t), 1)

	customer := tn.Do(t, http.MethodPost, "/v1/customers", map[string]any{"type": "individual", "name": "Bia"}).JSON(t)
	customerID := customer["id"].(string)
	res = tn.Do(t, http.MethodPost, "/v1/customers/"+customerID+"/contacts", map[string]any{"name": "Bia", "phone": "11 91234-5678", "is_primary": true})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	assert.Equal(t, true, res.JSON(t)["is_primary"])
	types := tn.Do(t, http.MethodPost, "/v1/equipment-types", map[string]any{"name": "Tablet", "fields": []any{}, "checklist": []any{}}).JSON(t)
	eq := tn.Do(t, http.MethodPost, "/v1/equipment", map[string]any{"customer_id": customerID, "type_id": types["id"]}).JSON(t)
	res = tn.Do(t, http.MethodGet, "/v1/equipment/"+eq["id"].(string), nil)
	require.Equal(t, http.StatusOK, res.Status)
	assert.Equal(t, customerID, res.JSON(t)["customer_id"])
}
