package serviceorder_test

import (
	"database/sql"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luiscadari/bancada-api/internal/apptest"
	"github.com/luiscadari/bancada-api/internal/platform/actor"
)

var env *apptest.Env

func TestMain(m *testing.M) { apptest.Main(m, &env) }

func newID() string { return uuid.Must(uuid.NewV7()).String() }

// fixture is an account with a customer, an equipment and a small catalog.
type fixture struct {
	tn                      apptest.Tenant
	customerID, equipmentID string
	serviceID, productID    string
}

var checklist = map[string]any{"charger": true, "state": "Riscos na tampa"}

func setup(t *testing.T) fixture {
	t.Helper()
	tn := apptest.SignupWith(t, env, false)
	res := tn.Do(t, http.MethodPost, "/v1/equipment-types", map[string]any{
		"name": "Celular", "fields": []map[string]any{},
		"checklist": []map[string]any{
			{"key": "charger", "label": "Carregador", "kind": "check", "required": true},
			{"key": "state", "label": "Estado físico", "kind": "text", "required": true},
			{"key": "case", "label": "Capinha", "kind": "check", "required": false},
		},
	})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	typeID := res.JSON(t)["id"].(string)
	res = tn.Do(t, http.MethodPost, "/v1/customers", map[string]any{
		"type": "individual", "name": "Antônio Araújo",
		"contacts": []map[string]any{{"name": "Antônio", "phone": "(19) 98888-7777"}},
	})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	customerID := res.JSON(t)["id"].(string)
	res = tn.Do(t, http.MethodPost, "/v1/equipment", map[string]any{
		"customer_id": customerID, "type_id": typeID, "brand": "Moto", "model": "G9", "serial_number": "SN-XYZ-987",
	})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	equipmentID := res.JSON(t)["id"].(string)
	res = tn.Do(t, http.MethodPost, "/v1/services", map[string]any{"name": "Troca de tela", "price_cents": 30000})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	serviceID := res.JSON(t)["id"].(string)
	res = tn.Do(t, http.MethodPost, "/v1/products", map[string]any{
		"name": "Película", "cost_cents": 1000, "price_cents": 5000, "warranty_days": 30,
	})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	return fixture{tn: tn, customerID: customerID, equipmentID: equipmentID, serviceID: serviceID, productID: res.JSON(t)["id"].(string)}
}

func (f fixture) openBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"customer_id": f.customerID, "equipment_id": f.equipmentID,
		"reported_defect": "Tela quebrada, não liga", "entry_checklist": checklist,
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// open opens an order and returns it.
func (f fixture) open(t *testing.T, c apptest.Client, extra map[string]any) map[string]any {
	t.Helper()
	res := c.Do(t, http.MethodPost, "/v1/service-orders", f.openBody(extra))
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	return res.JSON(t)["order"].(map[string]any)
}

func path(orderID string, parts ...string) string {
	p := "/v1/service-orders/" + orderID
	for _, s := range parts {
		p += "/" + s
	}
	return p
}

func move(t *testing.T, c apptest.Client, orderID, to string) map[string]any {
	t.Helper()
	res := c.Do(t, http.MethodPost, path(orderID, "transitions"), map[string]any{"to": to})
	require.Equal(t, http.StatusOK, res.Status, "→ %s: %s", to, res.Body)
	return res.JSON(t)
}

// quote creates and sends a quote with the given items and returns {order, quote}.
func quote(t *testing.T, c apptest.Client, orderID string, items []map[string]any) map[string]any {
	t.Helper()
	res := c.Do(t, http.MethodPost, path(orderID, "quotes"), map[string]any{"items": items})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	quoteID := res.JSON(t)["id"].(string)
	res = c.Do(t, http.MethodPost, path(orderID, "quotes", quoteID, "send"), nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	return res.JSON(t)
}

func approveAll(t *testing.T, c apptest.Client, orderID, quoteID string) map[string]any {
	t.Helper()
	res := c.Do(t, http.MethodPost, path(orderID, "quotes", quoteID, "decision"), map[string]any{
		"decision": "approved", "channel": "counter", "approver_name": "Antônio",
	})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	return res.JSON(t)
}

// system runs SQL bypassing RLS, to move clocks backwards in tests.
func system(t *testing.T, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("pgx", env.DB.SystemDSN)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(query, args...)
	require.NoError(t, err)
}

func querySystem(t *testing.T, query string, args ...any) int {
	t.Helper()
	db, err := sql.Open("pgx", env.DB.SystemDSN)
	require.NoError(t, err)
	defer db.Close()
	var n int
	require.NoError(t, db.QueryRow(query, args...).Scan(&n))
	return n
}

func statuses(t *testing.T, c apptest.Client, orderID string) []string {
	t.Helper()
	var out []string
	for _, h := range c.Do(t, http.MethodGet, path(orderID, "history"), nil).Items(t) {
		out = append(out, h["to_status"].(string))
	}
	return out
}

func TestOpenOrder(t *testing.T) {
	f := setup(t)
	tn := f.tn
	id := newID()
	res := tn.Do(t, http.MethodPost, "/v1/service-orders", f.openBody(map[string]any{
		"id": id, "device_password": "1234", "accessories": "Capinha azul", "priority": "high",
	}))
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	order := res.JSON(t)["order"].(map[string]any)
	assert.Equal(t, "MAT-000001", order["number"])
	assert.Equal(t, "received", order["status"])
	assert.Equal(t, "Antônio Araújo", order["customer_name"])
	assert.Equal(t, true, order["has_device_password"])
	assert.NotContains(t, string(res.Body), "1234", "the password never leaves in clear")
	assert.Empty(t, res.JSON(t)["active_warranties"])

	// Replaying the same open returns the same order without taking a new number.
	res = tn.Do(t, http.MethodPost, "/v1/service-orders", f.openBody(map[string]any{"id": id}))
	require.Equal(t, http.StatusCreated, res.Status)
	assert.Equal(t, "MAT-000001", res.JSON(t)["order"].(map[string]any)["number"])
	assert.Equal(t, "MAT-000002", f.open(t, tn.Client, nil)["number"])

	assert.Equal(t, []string{"received"}, statuses(t, tn.Client, id))

	// RN-05: the entry checklist follows the equipment type.
	for name, answers := range map[string]map[string]any{
		"missing required": {"charger": true},
		"wrong type":       {"charger": "sim", "state": "ok"},
		"unknown item":     {"charger": true, "state": "ok", "voltage": "110"},
		"blank text":       {"charger": true, "state": "  "},
	} {
		res := tn.Do(t, http.MethodPost, "/v1/service-orders", f.openBody(map[string]any{"entry_checklist": answers}))
		assert.Equal(t, http.StatusBadRequest, res.Status, name)
		assert.NotEmpty(t, res.JSON(t)["errors"], name)
	}
	other := tn.Do(t, http.MethodPost, "/v1/customers", map[string]any{"type": "individual", "name": "Outra"}).JSON(t)
	res = tn.Do(t, http.MethodPost, "/v1/service-orders", f.openBody(map[string]any{"customer_id": other["id"]}))
	assert.Equal(t, http.StatusUnprocessableEntity, res.Status)
	assert.Equal(t, "equipment_not_of_customer", res.Code(t))

	// The password is revealed on request, and the access is audited (RN-31).
	res = tn.Do(t, http.MethodPost, path(id, "device-password", "reveal"), nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, "1234", res.JSON(t)["password"])
	assert.Equal(t, 1, querySystem(t, `SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = 'device_password_revealed'`, id))
	assert.Equal(t, 0, querySystem(t, `SELECT count(*) FROM audit_log WHERE changes::text LIKE '%device_password_encrypted%'`))

	// Search (RF-17): number, customer name without accents, phone, serial.
	for _, q := range []string{"000001", "antonio araujo", "98888", "xyz-98"} {
		items := tn.Do(t, http.MethodGet, "/v1/service-orders?q="+url.QueryEscape(q), nil).Items(t)
		assert.NotEmpty(t, items, q)
	}
	assert.Empty(t, tn.Do(t, http.MethodGet, "/v1/service-orders?q=nada-disso", nil).Items(t))
	assert.Len(t, tn.Do(t, http.MethodGet, "/v1/service-orders?status=received&status=diagnosing", nil).Items(t), 2)
	assert.Empty(t, tn.Do(t, http.MethodGet, "/v1/service-orders?status=ready", nil).Items(t))
	assert.Len(t, tn.Do(t, http.MethodGet, "/v1/service-orders?unassigned=true&limit=1", nil).Items(t), 1)
}

func TestOpenWithReservedNumber(t *testing.T) {
	f := setup(t)
	deviceID := newID()
	res := apptest.Client{Env: env}.Do(t, http.MethodPost, "/v1/auth/login", map[string]any{
		"email": f.tn.Email, "password": f.tn.Password,
		"device": map[string]any{"id": deviceID, "branch_id": f.tn.BranchID, "name": "Balcão", "platform": "windows"},
	})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	device := apptest.Client{Env: env, Token: res.JSON(t)["access_token"].(string), BranchID: f.tn.BranchID}
	block := device.Do(t, http.MethodPost, "/v1/devices/"+deviceID+"/number-blocks", map[string]any{"size": 10})
	require.Equal(t, http.StatusCreated, block.Status, string(block.Body))

	order := f.open(t, device, map[string]any{"reserved_sequence": 3})
	assert.Equal(t, "MAT-000003", order["number"])

	res = device.Do(t, http.MethodPost, "/v1/service-orders", f.openBody(map[string]any{"reserved_sequence": 3}))
	assert.Equal(t, http.StatusConflict, res.Status)
	assert.Equal(t, "order_number_taken", res.Code(t))
	res = device.Do(t, http.MethodPost, "/v1/service-orders", f.openBody(map[string]any{"reserved_sequence": 99}))
	assert.Equal(t, "number_not_reserved", res.Code(t))
	res = f.tn.Do(t, http.MethodPost, "/v1/service-orders", f.openBody(map[string]any{"reserved_sequence": 4}))
	assert.Equal(t, "number_requires_device", res.Code(t))

	// The counter skips the reserved block.
	assert.Equal(t, "MAT-000011", f.open(t, f.tn.Client, nil)["number"])
}

func TestFullFlow(t *testing.T) {
	f := setup(t)
	tn := f.tn
	attendant, _ := tn.Invite(t, actor.RoleAttendant)
	tech, techID := tn.Invite(t, actor.RoleTechnician)

	order := f.open(t, attendant, map[string]any{"device_password": "senha-do-cliente", "technician_id": techID})
	id := order["id"].(string)
	assert.Equal(t, techID, order["technician_id"])

	move(t, tech, id, "diagnosing")
	res := tech.Do(t, http.MethodPatch, path(id), map[string]any{"diagnosis": "Tela trincada e conector ok"})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, "Tela trincada e conector ok", res.JSON(t)["diagnosis"])

	sent := quote(t, tech, id, []map[string]any{
		{"kind": "service", "service_id": f.serviceID},
		{"kind": "part", "product_id": f.productID, "quantity": 2, "optional": true},
	})
	q1 := sent["quote"].(map[string]any)
	assert.Equal(t, "awaiting_approval", sent["order"].(map[string]any)["status"])
	assert.Equal(t, "sent", q1["status"])
	assert.EqualValues(t, 40000, q1["total_cents"])
	assert.NotNil(t, q1["valid_until"])
	items := q1["items"].([]any)
	assert.Equal(t, "Troca de tela", items[0].(map[string]any)["description"])
	assert.EqualValues(t, 90, items[0].(map[string]any)["warranty_days"])

	// The technician cannot record the customer's decision (section 3).
	res = tech.Do(t, http.MethodPost, path(id, "quotes", q1["id"].(string), "decision"),
		map[string]any{"decision": "approved", "channel": "phone", "approver_name": "Antônio"})
	assert.Equal(t, http.StatusForbidden, res.Status)

	// Partial approval: the customer declines the optional film → new version (RN-11).
	res = attendant.Do(t, http.MethodPost, path(id, "quotes", q1["id"].(string), "decision"), map[string]any{
		"decision": "approved", "channel": "phone", "approver_name": "Antônio",
		"accepted_item_ids": []string{items[0].(map[string]any)["id"].(string)},
	})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	q2 := res.JSON(t)["quote"].(map[string]any)
	ord := res.JSON(t)["order"].(map[string]any)
	assert.EqualValues(t, 2, q2["number"])
	assert.Equal(t, "approved", q2["status"])
	assert.Equal(t, q1["id"], q2["previous_quote_id"])
	assert.EqualValues(t, 30000, q2["total_cents"])
	assert.Equal(t, "phone", q2["decision_channel"])
	assert.Equal(t, "approved", ord["status"])
	assert.EqualValues(t, 30000, ord["approved_total_cents"])
	versions := tn.Do(t, http.MethodGet, path(id, "quotes"), nil).Items(t)
	require.Len(t, versions, 2)
	assert.Equal(t, "superseded", versions[0]["status"])

	move(t, tech, id, "in_progress")
	move(t, tech, id, "awaiting_part")
	move(t, tech, id, "in_progress")
	move(t, tech, id, "testing")
	move(t, tech, id, "in_progress") // test failed
	move(t, tech, id, "testing")
	ready := move(t, tech, id, "ready")
	assert.NotNil(t, ready["ready_at"])
	res = tech.Do(t, http.MethodPatch, path(id), map[string]any{"technical_report": "Tela substituída e testada."})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))

	// RN-15: the balance is open (no payments yet), so delivery needs the owner.
	res = attendant.Do(t, http.MethodPost, path(id, "deliver"), map[string]any{})
	assert.Equal(t, http.StatusUnprocessableEntity, res.Status)
	assert.Equal(t, "open_balance", res.Code(t))
	assert.Contains(t, res.JSON(t)["detail"], "R$ 300,00")
	res = attendant.Do(t, http.MethodPost, path(id, "deliver"), map[string]any{"authorize_open_balance": true})
	assert.Equal(t, http.StatusForbidden, res.Status)
	assert.Equal(t, "open_balance_requires_owner", res.Code(t))
	res = tech.Do(t, http.MethodPost, path(id, "deliver"), map[string]any{})
	assert.Equal(t, "technician_cannot_open_or_deliver", res.Code(t))

	res = tn.Do(t, http.MethodPost, path(id, "deliver"), map[string]any{
		"authorize_open_balance": true, "picked_up_by_name": "Maria Araújo", "picked_up_by_document": "123",
	})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	done := res.JSON(t)
	assert.Equal(t, "delivered", done["status"])
	assert.EqualValues(t, 30000, done["open_balance_cents"])
	assert.Equal(t, tn.UserID, done["balance_authorized_by"])
	assert.Equal(t, "Maria Araújo", done["picked_up_by_name"])
	assert.Equal(t, false, done["has_device_password"], "RN-31: erased at delivery")
	assert.Equal(t, http.StatusNotFound, tn.Do(t, http.MethodPost, path(id, "device-password", "reveal"), nil).Status)

	// RN-18: warranty per item from the delivery date.
	versions = tn.Do(t, http.MethodGet, path(id, "quotes"), nil).Items(t)
	item := versions[1]["items"].([]any)[0].(map[string]any)
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	assert.Equal(t, time.Now().In(loc).AddDate(0, 0, 90).Format(time.DateOnly), item["warranty_until"])

	// Final status: nothing else changes.
	res = tn.Do(t, http.MethodPatch, path(id), map[string]any{"diagnosis": "x"})
	assert.Equal(t, "order_closed", res.Code(t))

	assert.Equal(t, []string{
		"received", "diagnosing", "awaiting_approval", "approved", "in_progress", "awaiting_part", "in_progress",
		"testing", "in_progress", "testing", "ready", "delivered",
	}, statuses(t, tn.Client, id))

	// RF-36: equipment history with the active warranty; RF-35: customer history.
	res = attendant.Do(t, http.MethodGet, "/v1/equipment/"+f.equipmentID+"/history", nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	warranties := res.JSON(t)["active_warranties"].([]any)
	require.Len(t, warranties, 1)
	assert.Equal(t, "Troca de tela", warranties[0].(map[string]any)["description"])
	assert.Len(t, res.Items(t), 1)
	assert.Len(t, attendant.Do(t, http.MethodGet, "/v1/customers/"+f.customerID+"/history", nil).Items(t), 1)

	// UC-01 2b: a new order of the same equipment warns about the active warranty.
	res = attendant.Do(t, http.MethodPost, "/v1/service-orders", f.openBody(nil))
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	assert.Len(t, res.JSON(t)["active_warranties"], 1)
}

func TestStatusRules(t *testing.T) {
	f := setup(t)
	tn := f.tn
	attendant, _ := tn.Invite(t, actor.RoleAttendant)
	tech, techID := tn.Invite(t, actor.RoleTechnician)
	other, otherID := tn.Invite(t, actor.RoleTechnician)

	id := f.open(t, attendant, map[string]any{"device_password": "0000"})["id"].(string)

	res := tn.Do(t, http.MethodPost, path(id, "transitions"), map[string]any{"to": "in_progress"})
	assert.Equal(t, http.StatusConflict, res.Status)
	assert.Equal(t, "invalid_transition", res.Code(t))
	res = tn.Do(t, http.MethodPost, path(id, "transitions"), map[string]any{"to": "delivered"})
	assert.Equal(t, http.StatusBadRequest, res.Status, "only bench steps go through transitions")

	// A technician takes an unassigned order by starting the diagnosis; others cannot touch it.
	assert.Equal(t, techID, move(t, tech, id, "diagnosing")["technician_id"])
	res = other.Do(t, http.MethodPatch, path(id), map[string]any{"diagnosis": "x"})
	assert.Equal(t, http.StatusForbidden, res.Status)
	assert.Equal(t, "order_not_assigned", res.Code(t))
	assert.Equal(t, http.StatusForbidden, other.Do(t, http.MethodPost, path(id, "device-password", "reveal"), nil).Status)
	assert.Len(t, other.Do(t, http.MethodGet, "/v1/service-orders", nil).Items(t), 1, "technicians see the whole branch")
	assert.Equal(t, http.StatusForbidden, tech.Do(t, http.MethodPatch, path(id, "assignment"),
		map[string]any{"technician_id": otherID}).Status)
	res = attendant.Do(t, http.MethodPatch, path(id, "assignment"), map[string]any{"technician_id": otherID, "priority": "urgent"})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, "urgent", res.JSON(t)["priority"])
	res = attendant.Do(t, http.MethodPatch, path(id, "assignment"), map[string]any{"technician_id": newID()})
	assert.Equal(t, "technician_not_in_branch", res.Code(t))

	// No repair: reason required, equipment waits for pickup.
	res = other.Do(t, http.MethodPost, path(id, "transitions"), map[string]any{"to": "awaiting_pickup"})
	assert.Equal(t, http.StatusBadRequest, res.Status)
	res = other.Do(t, http.MethodPost, path(id, "transitions"), map[string]any{"to": "awaiting_pickup", "reason": "Placa sem conserto"})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))

	// RN-16: abandonment only by the owner, after the branch term.
	res = attendant.Do(t, http.MethodPost, path(id, "abandon"), map[string]any{})
	assert.Equal(t, http.StatusForbidden, res.Status)
	res = tn.Do(t, http.MethodPost, path(id, "abandon"), map[string]any{})
	assert.Equal(t, "abandonment_term_not_reached", res.Code(t))
	system(t, `UPDATE service_orders SET ready_at = now() - interval '1096 days' WHERE id = $1`, id)
	res = tn.Do(t, http.MethodPost, path(id, "abandon"), map[string]any{"reason": "Cliente não respondeu"})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, "abandoned", res.JSON(t)["status"])
	// The customer showed up: an abandoned equipment can still be delivered (nothing due).
	res = attendant.Do(t, http.MethodPost, path(id, "deliver"), map[string]any{})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, false, res.JSON(t)["has_device_password"])

	// RN-08: cancel only by owner/attendant, with a reason, before execution.
	id2 := f.open(t, attendant, map[string]any{"device_password": "1111"})["id"].(string)
	assert.Equal(t, http.StatusForbidden, tech.Do(t, http.MethodPost, path(id2, "cancel"), map[string]any{"reason": "Desistiu"}).Status)
	assert.Equal(t, http.StatusBadRequest, attendant.Do(t, http.MethodPost, path(id2, "cancel"), map[string]any{}).Status)
	assert.Equal(t, http.StatusBadRequest, attendant.Do(t, http.MethodPost, path(id2, "cancel"), map[string]any{"reason": "   "}).Status)
	res = attendant.Do(t, http.MethodPost, path(id2, "cancel"), map[string]any{"reason": "Cliente desistiu"})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, "canceled", res.JSON(t)["status"])
	assert.Equal(t, "Cliente desistiu", res.JSON(t)["cancel_reason"])
	assert.Equal(t, false, res.JSON(t)["has_device_password"])

	// Execution cannot be canceled; the attendant does not run the bench.
	id3 := f.open(t, attendant, map[string]any{"technician_id": techID})["id"].(string)
	move(t, tech, id3, "diagnosing")
	q := quote(t, tech, id3, []map[string]any{{"kind": "service", "service_id": f.serviceID}})
	approveAll(t, attendant, id3, q["quote"].(map[string]any)["id"].(string))
	res = attendant.Do(t, http.MethodPost, path(id3, "transitions"), map[string]any{"to": "in_progress"})
	assert.Equal(t, "transition_forbidden", res.Code(t))
	move(t, tech, id3, "in_progress")
	res = attendant.Do(t, http.MethodPost, path(id3, "cancel"), map[string]any{"reason": "Desistiu"})
	assert.Equal(t, "invalid_transition", res.Code(t))

	// Technicians open and deliver only when the branch allows it.
	res = tech.Do(t, http.MethodPost, "/v1/service-orders", f.openBody(nil))
	assert.Equal(t, "technician_cannot_open_or_deliver", res.Code(t))
	settings := tn.Do(t, http.MethodGet, "/v1/branches/"+tn.BranchID, nil).JSON(t)["settings"].(map[string]any)
	settings["technician_can_open_and_deliver"] = true
	require.Equal(t, http.StatusOK, tn.Do(t, http.MethodPut, "/v1/branches/"+tn.BranchID+"/settings", settings).Status)
	f.open(t, tech, nil)
}

func TestQuoteRules(t *testing.T) {
	f := setup(t)
	tn := f.tn
	attendant, _ := tn.Invite(t, actor.RoleAttendant)
	tech, techID := tn.Invite(t, actor.RoleTechnician)

	id := f.open(t, attendant, map[string]any{"technician_id": techID})["id"].(string)
	res := tech.Do(t, http.MethodPost, path(id, "quotes"), map[string]any{})
	assert.Equal(t, "quote_not_allowed", res.Code(t), "diagnosis first")
	move(t, tech, id, "diagnosing")

	svc := func(extra map[string]any) []map[string]any {
		item := map[string]any{"kind": "service", "service_id": f.serviceID}
		for k, v := range extra {
			item[k] = v
		}
		return []map[string]any{item}
	}
	// RN-12 and section 3: technicians give no discount, attendants up to 10%.
	res = tech.Do(t, http.MethodPost, path(id, "quotes"), map[string]any{"items": svc(map[string]any{"discount_cents": 100})})
	assert.Equal(t, "discount_not_allowed", res.Code(t))
	res = attendant.Do(t, http.MethodPost, path(id, "quotes"), map[string]any{"items": svc(map[string]any{"discount_cents": 6000})})
	assert.Equal(t, http.StatusForbidden, res.Status)
	assert.Equal(t, "discount_requires_owner", res.Code(t))
	res = attendant.Do(t, http.MethodPost, path(id, "quotes"), map[string]any{"items": svc(map[string]any{"unit_price_cents": 20000})})
	assert.Equal(t, "discount_requires_owner", res.Code(t), "a lower price is a discount")
	res = attendant.Do(t, http.MethodPost, path(id, "quotes"), map[string]any{"items": svc(map[string]any{"discount_cents": 50000})})
	assert.Equal(t, "discount_exceeds_price", res.Code(t))
	res = tech.Do(t, http.MethodPost, path(id, "quotes"), map[string]any{"items": []map[string]any{{"kind": "service", "product_id": f.productID}}})
	assert.Equal(t, "item_reference_required", res.Code(t))

	res = attendant.Do(t, http.MethodPost, path(id, "quotes"), map[string]any{"items": svc(map[string]any{"discount_cents": 3000})})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	draft := res.JSON(t)
	assert.EqualValues(t, 30000, draft["subtotal_cents"])
	assert.EqualValues(t, 3000, draft["discount_cents"])
	assert.EqualValues(t, 27000, draft["total_cents"])
	res = tech.Do(t, http.MethodPost, path(id, "quotes"), map[string]any{})
	assert.Equal(t, "quote_draft_exists", res.Code(t))

	// The owner has no limit.
	draftID := draft["id"].(string)
	res = tn.Do(t, http.MethodPut, path(id, "quotes", draftID), map[string]any{"items": svc(map[string]any{"discount_cents": 15000})})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.EqualValues(t, 15000, res.JSON(t)["total_cents"])
	res = tn.Do(t, http.MethodPut, path(id, "quotes", draftID), map[string]any{"items": []map[string]any{}})
	require.Equal(t, http.StatusOK, res.Status)
	res = tech.Do(t, http.MethodPost, path(id, "quotes", draftID, "send"), nil)
	assert.Equal(t, "quote_empty", res.Code(t))
	tn.Do(t, http.MethodPut, path(id, "quotes", draftID), map[string]any{"items": svc(nil)})
	res = tech.Do(t, http.MethodPost, path(id, "quotes", draftID, "send"), nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	res = tn.Do(t, http.MethodPut, path(id, "quotes", draftID), map[string]any{"items": svc(nil)})
	assert.Equal(t, "quote_not_draft", res.Code(t))

	// RN-09: an expired quote cannot be decided.
	system(t, `UPDATE quotes SET valid_until = now() - interval '1 minute' WHERE id = $1`, draftID)
	res = attendant.Do(t, http.MethodPost, path(id, "quotes", draftID, "decision"),
		map[string]any{"decision": "approved", "channel": "phone", "approver_name": "Antônio"})
	assert.Equal(t, "quote_expired", res.Code(t))

	// A new version replaces it; rejection charges the diagnostic fee (RN-13, RN-14).
	settings := tn.Do(t, http.MethodGet, "/v1/branches/"+tn.BranchID, nil).JSON(t)["settings"].(map[string]any)
	settings["diagnostic_fee"] = map[string]any{"enabled": true, "amount_cents": 5000, "deduct_on_approval": false}
	require.Equal(t, http.StatusOK, tn.Do(t, http.MethodPut, "/v1/branches/"+tn.BranchID+"/settings", settings).Status)
	q2 := quote(t, tech, id, svc(nil))["quote"].(map[string]any)
	assert.EqualValues(t, 2, q2["number"])
	res = attendant.Do(t, http.MethodPost, path(id, "quotes", q2["id"].(string), "decision"),
		map[string]any{"decision": "rejected", "channel": "whatsapp"})
	assert.Equal(t, "approver_required", res.Code(t))
	res = attendant.Do(t, http.MethodPost, path(id, "quotes", q2["id"].(string), "decision"),
		map[string]any{"decision": "rejected", "channel": "whatsapp", "approver_name": "Antônio", "rejection_reason": "Caro"})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	ord := res.JSON(t)["order"].(map[string]any)
	assert.Equal(t, "awaiting_pickup", ord["status"])
	assert.EqualValues(t, 5000, ord["diagnostic_fee_cents"])
	assert.EqualValues(t, 5000, ord["amount_due_cents"])
	assert.Equal(t, "Caro", res.JSON(t)["quote"].(map[string]any)["rejection_reason"])

	// RN-10: a total within the pre-authorized amount is approved at once; without the
	// deduction the fee is added to what is due (RN-13).
	id2 := f.open(t, attendant, map[string]any{"technician_id": techID, "pre_authorized_cents": 35000})["id"].(string)
	move(t, tech, id2, "diagnosing")
	res2 := quote(t, tech, id2, svc(nil))
	assert.Equal(t, "approved", res2["order"].(map[string]any)["status"])
	assert.Equal(t, "pre_authorized", res2["quote"].(map[string]any)["decision_channel"])
	assert.EqualValues(t, 35000, res2["order"].(map[string]any)["amount_due_cents"])

	// RN-11: a new quote during execution goes back to approval and supersedes the approved one.
	move(t, tech, id2, "in_progress")
	res2 = quote(t, tech, id2, []map[string]any{
		{"kind": "service", "service_id": f.serviceID}, {"kind": "part", "product_id": f.productID, "quantity": 2},
	}) // 40000 > pre-authorized 35000
	assert.Equal(t, "awaiting_approval", res2["order"].(map[string]any)["status"])
	assert.EqualValues(t, 0, res2["order"].(map[string]any)["approved_total_cents"])
	versions := tn.Do(t, http.MethodGet, path(id2, "quotes"), nil).Items(t)
	assert.Equal(t, "superseded", versions[0]["status"])
	assert.Equal(t, "sent", versions[1]["status"])
	approveAll(t, attendant, id2, versions[1]["id"].(string))
	assert.Equal(t, []string{"received", "diagnosing", "awaiting_approval", "approved", "in_progress", "awaiting_approval", "approved"},
		statuses(t, tn.Client, id2))
}

func TestCompanyApprovers(t *testing.T) {
	f := setup(t)
	tn := f.tn
	res := tn.Do(t, http.MethodPost, "/v1/customers", map[string]any{
		"type": "company", "name": "Escritório Contábil Ltda",
		"contacts": []map[string]any{
			{"name": "Recepção", "is_requester": true},
			{"name": "Gerente", "is_approver": true, "approval_limit_cents": 20000},
			{"name": "Diretora", "is_approver": true},
		},
	})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	company := res.JSON(t)
	contacts := map[string]string{}
	for _, c := range company["contacts"].([]any) {
		contacts[c.(map[string]any)["name"].(string)] = c.(map[string]any)["id"].(string)
	}
	cc := tn.Do(t, http.MethodPost, "/v1/customers/"+company["id"].(string)+"/cost-centers", map[string]any{"name": "Fiscal"}).JSON(t)
	typeID := tn.Do(t, http.MethodGet, "/v1/equipment/"+f.equipmentID, nil).JSON(t)["type_id"]
	eq := tn.Do(t, http.MethodPost, "/v1/equipment", map[string]any{"customer_id": company["id"], "type_id": typeID}).JSON(t)
	body := func(extra map[string]any) map[string]any {
		b := map[string]any{"customer_id": company["id"], "equipment_id": eq["id"], "reported_defect": "Não liga", "entry_checklist": checklist}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}

	// RN-34: the order registers who brought the equipment and the sector to charge.
	res = tn.Do(t, http.MethodPost, "/v1/service-orders", body(nil))
	assert.Equal(t, "requester_required", res.Code(t))
	res = tn.Do(t, http.MethodPost, "/v1/service-orders", body(map[string]any{"requester_contact_id": contacts["Gerente"]}))
	assert.Equal(t, "contact_not_requester", res.Code(t))
	res = tn.Do(t, http.MethodPost, "/v1/service-orders", body(map[string]any{
		"requester_contact_id": contacts["Recepção"], "cost_center_id": cc["id"],
	}))
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	order := res.JSON(t)["order"].(map[string]any)
	id := order["id"].(string)
	assert.Equal(t, cc["id"], order["cost_center_id"])

	move(t, tn.Client, id, "diagnosing")
	q := quote(t, tn.Client, id, []map[string]any{{"kind": "service", "service_id": f.serviceID}})["quote"].(map[string]any)
	decide := func(extra map[string]any) apptest.Response {
		b := map[string]any{"decision": "approved", "channel": "email"}
		for k, v := range extra {
			b[k] = v
		}
		return tn.Do(t, http.MethodPost, path(id, "quotes", q["id"].(string), "decision"), b)
	}
	assert.Equal(t, "approver_required", decide(map[string]any{"approver_name": "Alguém"}).Code(t))
	assert.Equal(t, "contact_not_approver", decide(map[string]any{"approver_contact_id": contacts["Recepção"]}).Code(t))
	assert.Equal(t, "approval_limit_exceeded", decide(map[string]any{"approver_contact_id": contacts["Gerente"]}).Code(t))
	res = decide(map[string]any{"approver_contact_id": contacts["Diretora"]})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, contacts["Diretora"], res.JSON(t)["order"].(map[string]any)["approver_contact_id"])
	assert.Equal(t, "Diretora", res.JSON(t)["quote"].(map[string]any)["decided_by_name"])
}

func TestWarrantyReturn(t *testing.T) {
	f := setup(t)
	tn := f.tn
	id := f.open(t, tn.Client, nil)["id"].(string)
	res := tn.Do(t, http.MethodPost, path(id, "warranty-return"), map[string]any{"reported_defect": "x", "entry_checklist": checklist})
	assert.Equal(t, "order_not_delivered", res.Code(t))

	move(t, tn.Client, id, "diagnosing")
	q := quote(t, tn.Client, id, []map[string]any{
		{"kind": "service", "service_id": f.serviceID}, {"kind": "part", "product_id": f.productID},
	})["quote"].(map[string]any)
	approveAll(t, tn.Client, id, q["id"].(string))
	for _, to := range []string{"in_progress", "testing", "ready"} {
		move(t, tn.Client, id, to)
	}
	res = tn.Do(t, http.MethodPost, path(id, "deliver"), map[string]any{"authorize_open_balance": true})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))

	// The part's 30-day warranty is over; only the service is still covered.
	system(t, `UPDATE quote_items SET warranty_until = current_date - 1 WHERE product_id = $1`, f.productID)
	returnID := newID()
	res = tn.Do(t, http.MethodPost, path(id, "warranty-return"), map[string]any{
		"id": returnID, "reported_defect": "Tela voltou a falhar", "entry_checklist": checklist,
	})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	ret := res.JSON(t)["order"].(map[string]any)
	assert.Equal(t, id, ret["origin_order_id"])
	assert.Equal(t, "MAT-000002", ret["number"])
	assert.Len(t, res.JSON(t)["active_warranties"], 1)
	res = tn.Do(t, http.MethodPost, path(id, "warranty-return"), map[string]any{
		"id": returnID, "reported_defect": "Tela voltou a falhar", "entry_checklist": checklist,
	})
	require.Equal(t, http.StatusCreated, res.Status, "replay")
	assert.Equal(t, "MAT-000002", res.JSON(t)["order"].(map[string]any)["number"])

	quotes := tn.Do(t, http.MethodGet, path(returnID, "quotes"), nil).Items(t)
	require.Len(t, quotes, 1)
	assert.Equal(t, "draft", quotes[0]["status"])
	assert.EqualValues(t, 0, quotes[0]["total_cents"])
	items := quotes[0]["items"].([]any)
	require.Len(t, items, 1)
	covered := items[0].(map[string]any)
	assert.Equal(t, "Troca de tela", covered["description"])
	assert.NotNil(t, covered["covered_item_id"])

	// RN-19: the covered item stays free; an expired item cannot be covered.
	move(t, tn.Client, returnID, "diagnosing")
	draftID := quotes[0]["id"].(string)
	res = tn.Do(t, http.MethodPut, path(returnID, "quotes", draftID), map[string]any{"items": []map[string]any{
		{"kind": "service", "service_id": f.serviceID, "covered_item_id": covered["covered_item_id"], "unit_price_cents": 99999},
	}})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.EqualValues(t, 0, res.JSON(t)["total_cents"])
	res = tn.Do(t, http.MethodPost, path(returnID, "quotes", draftID, "send"), nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	assert.Equal(t, "approved", res.JSON(t)["order"].(map[string]any)["status"])
	assert.Equal(t, "no_charge", res.JSON(t)["quote"].(map[string]any)["decision_channel"])

	res = tn.Do(t, http.MethodPost, path(id, "warranty-return"), map[string]any{
		"reported_defect": "Película", "entry_checklist": checklist, "covered_item_ids": []string{newID()},
	})
	assert.Equal(t, "item_not_under_warranty", res.Code(t))

	assert.Len(t, tn.Do(t, http.MethodGet, "/v1/customers/"+f.customerID+"/history", nil).Items(t), 2)
}

func TestOrderIsolation(t *testing.T) {
	f := setup(t)
	id := f.open(t, f.tn.Client, nil)["id"].(string)

	// Another account sees nothing (RN-01).
	intruder := apptest.Signup(t, env)
	assert.Equal(t, http.StatusNotFound, intruder.Do(t, http.MethodGet, path(id), nil).Status)
	assert.Empty(t, intruder.Do(t, http.MethodGet, "/v1/service-orders", nil).Items(t))
	assert.Equal(t, http.StatusNotFound, intruder.Do(t, http.MethodPost, path(id, "cancel"), map[string]any{"reason": "xx x"}).Status)
	assert.Equal(t, http.StatusNotFound, intruder.Do(t, http.MethodGet, "/v1/customers/"+f.customerID+"/history", nil).Status)

	// Another branch of the same account does not see the order (RN-02).
	res := f.tn.Do(t, http.MethodPost, "/v1/branches", map[string]any{"name": "Centro", "os_prefix": "CEN"})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	branch2 := res.JSON(t)["id"].(string)
	owner2 := f.tn.Client
	owner2.BranchID = branch2
	assert.Equal(t, http.StatusNotFound, owner2.Do(t, http.MethodGet, path(id), nil).Status)
	assert.Equal(t, "CEN-000001", f.open(t, owner2, nil)["number"], "numbering is per branch")

	// An attendant of the first branch cannot act on the second one, nor see its history.
	attendant, _ := f.tn.Invite(t, actor.RoleAttendant)
	other := attendant
	other.BranchID = branch2
	assert.Equal(t, http.StatusForbidden, other.Do(t, http.MethodGet, "/v1/service-orders", nil).Status)
	assert.Len(t, attendant.Do(t, http.MethodGet, "/v1/customers/"+f.customerID+"/history", nil).Items(t), 1)
	assert.Len(t, f.tn.Do(t, http.MethodGet, "/v1/customers/"+f.customerID+"/history", nil).Items(t), 2)

	// Branch-scoped operations need the header.
	noBranch := f.tn.Client
	noBranch.BranchID = ""
	assert.Equal(t, http.StatusBadRequest, noBranch.Do(t, http.MethodGet, "/v1/service-orders", nil).Status)
}
