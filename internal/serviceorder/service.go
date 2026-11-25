package serviceorder

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/luiscadari/bancada-api/internal/finance"
	"github.com/luiscadari/bancada-api/internal/identity"
	"github.com/luiscadari/bancada-api/internal/inventory"
	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/apperr"
	"github.com/luiscadari/bancada-api/internal/platform/audit"
	"github.com/luiscadari/bancada-api/internal/platform/crypto"
	"github.com/luiscadari/bancada-api/internal/platform/database"
	"github.com/luiscadari/bancada-api/internal/platform/ids"
	"github.com/luiscadari/bancada-api/internal/platform/paging"
	"github.com/luiscadari/bancada-api/internal/registry"
)

// Registry is what service orders need from the registry module (ADR-04).
type Registry interface {
	Customer(ctx context.Context, id uuid.UUID) (registry.Customer, error)
	Contact(ctx context.Context, customerID, contactID uuid.UUID) (registry.Contact, error)
	CostCenter(ctx context.Context, customerID, id uuid.UUID) (registry.CostCenter, error)
	GetEquipment(ctx context.Context, id uuid.UUID) (registry.Equipment, error)
	GetEquipmentType(ctx context.Context, id uuid.UUID) (registry.EquipmentTypeView, error)
	GetService(ctx context.Context, id uuid.UUID) (registry.CatalogService, error)
	GetProduct(ctx context.Context, id uuid.UUID) (registry.Product, error)
}

// Identity is what service orders need from the identity module.
type Identity interface {
	Settings(ctx context.Context, branchID uuid.UUID) (identity.BranchSettings, string, error)
	NextNumber(ctx context.Context, branchID uuid.UUID) (string, int64, error)
	ReservedNumber(ctx context.Context, branchID, deviceID uuid.UUID, n int64) (string, error)
	WorksInBranch(ctx context.Context, userID, branchID uuid.UUID) (bool, error)
}

// Service implements the service order use cases. Every method runs inside the request's
// tenant transaction and acts on the branch of the X-Branch-ID header.
type Service struct {
	registry  Registry
	identity  Identity
	inventory inventory.Reserver
	ledger    finance.Ledger
	sealer    *crypto.Sealer
	now       func() time.Time
}

func NewService(reg Registry, idn Identity, inv inventory.Reserver, ledger finance.Ledger, sealer *crypto.Sealer) *Service {
	return &Service{
		registry: reg, identity: idn, inventory: inv, ledger: ledger, sealer: sealer,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func mustActor(ctx context.Context) *actor.Actor {
	a, ok := actor.From(ctx)
	if !ok {
		panic("serviceorder: no actor in context")
	}
	return a
}

// branchOf returns the branch the request acts on; every operation of this module is
// branch-scoped, so the middleware guarantees it.
func branchOf(a *actor.Actor) uuid.UUID {
	if a.BranchID == nil {
		panic("serviceorder: branch-scoped operation without a branch")
	}
	return *a.BranchID
}

// isTechnicianOnly reports whether the actor works in the branch only as a technician.
func isTechnicianOnly(a *actor.Actor) bool {
	return !a.HasAnyRole(actor.RoleOwner, actor.RoleAttendant)
}

// ensureCanWork enforces that a technician only changes the orders assigned to them.
func ensureCanWork(a *actor.Actor, o *Order) error {
	if !isTechnicianOnly(a) {
		return nil
	}
	if o.TechnicianID == nil || *o.TechnicianID != a.UserID {
		return errNotAssigned
	}
	return nil
}

func (s *Service) location(ctx context.Context, branchID uuid.UUID) (identity.BranchSettings, *time.Location, error) {
	settings, tz, err := s.identity.Settings(ctx, branchID)
	if err != nil {
		return settings, nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return settings, loc, nil
}

// ---- queries ----------------------------------------------------------------------

// orderQuery selects orders with the customer name and the approved quote total.
func orderQuery(ctx context.Context) *gorm.DB {
	return database.Conn(ctx).Model(&Order{}).
		Select(`service_orders.*, c.name AS customer_name,
			COALESCE((SELECT q.total_cents FROM quotes q WHERE q.order_id = service_orders.id
			  AND q.status = 'approved' AND q.deleted_at IS NULL LIMIT 1), 0) AS approved_total_cents`).
		Joins("JOIN customers c ON c.id = service_orders.customer_id")
}

func (s *Service) findOrder(ctx context.Context, id uuid.UUID) (Order, error) {
	branch := branchOf(mustActor(ctx))
	var o Order
	err := orderQuery(ctx).Where("service_orders.id = ? AND service_orders.branch_id = ?", id, branch).Take(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return o, errOrderNotFound
	}
	return o, err
}

// lockOrder loads an order of the branch locking its row until the end of the request,
// so concurrent changes to the same order are serialized.
func (s *Service) lockOrder(ctx context.Context, id uuid.UUID) (Order, error) {
	branch := branchOf(mustActor(ctx))
	var locked []uuid.UUID
	err := database.Conn(ctx).Model(&Order{}).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND branch_id = ?", id, branch).
		Pluck("id", &locked).Error
	if err != nil {
		return Order{}, err
	}
	if len(locked) == 0 {
		return Order{}, errOrderNotFound
	}
	return s.findOrder(ctx, id)
}

// GetOrder returns an order of the branch.
func (s *Service) GetOrder(ctx context.Context, id uuid.UUID) (Order, error) {
	return s.findOrder(ctx, id)
}

// OrderFilter narrows the order queue.
type OrderFilter struct {
	Query        string
	Statuses     []string
	TechnicianID *uuid.UUID
	Unassigned   bool
	CustomerID   *uuid.UUID
	EquipmentID  *uuid.UUID
}

// ListOrders is the branch queue and the search of RF-17: by number, customer name,
// contact phone or equipment serial number.
func (s *Service) ListOrders(ctx context.Context, f OrderFilter, page paging.Page) ([]Order, *string, error) {
	branch := branchOf(mustActor(ctx))
	db := orderQuery(ctx).Where("service_orders.branch_id = ?", branch)
	if len(f.Statuses) > 0 {
		db = db.Where("service_orders.status IN ?", f.Statuses)
	}
	if f.TechnicianID != nil {
		db = db.Where("service_orders.technician_id = ?", *f.TechnicianID)
	}
	if f.Unassigned {
		db = db.Where("service_orders.technician_id IS NULL")
	}
	if f.CustomerID != nil {
		db = db.Where("service_orders.customer_id = ?", *f.CustomerID)
	}
	if f.EquipmentID != nil {
		db = db.Where("service_orders.equipment_id = ?", *f.EquipmentID)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := paging.Like(q)
		conds := []string{
			`lower(service_orders.number) LIKE '%' || lower(?) || '%'`,
			`search_text(c.name) LIKE '%' || search_text(?) || '%'`,
			`EXISTS (SELECT 1 FROM equipment e WHERE e.id = service_orders.equipment_id
			   AND lower(e.serial_number) LIKE '%' || lower(?) || '%')`,
		}
		args := []any{like, like, like}
		if digits := onlyDigits(q); len(digits) >= 4 {
			conds = append(conds, `EXISTS (SELECT 1 FROM customer_contacts cc WHERE cc.customer_id = service_orders.customer_id
				AND cc.deleted_at IS NULL AND cc.phone_digits LIKE ?)`)
			args = append(args, "%"+digits+"%")
		}
		db = db.Where("("+strings.Join(conds, " OR ")+")", args...)
	}
	return listPage(db, page)
}

func listPage(db *gorm.DB, page paging.Page) ([]Order, *string, error) {
	var orders []Order
	if err := page.Apply(db, "service_orders.id").Find(&orders).Error; err != nil {
		return nil, nil, err
	}
	orders, next := paging.Trim(page, orders, func(o Order) uuid.UUID { return o.ID })
	return orders, next, nil
}

// accessibleBranches restricts a query to the branches the actor works in.
func accessibleBranches(db *gorm.DB, a *actor.Actor) *gorm.DB {
	if a.IsOwner() {
		return db
	}
	var branches []uuid.UUID
	for _, g := range a.Grants {
		if g.BranchID != nil {
			branches = append(branches, *g.BranchID)
		}
	}
	return db.Where("service_orders.branch_id IN ?", append(branches, uuid.Nil))
}

// CustomerHistory lists a customer's orders in the branches the actor works in (RF-35).
func (s *Service) CustomerHistory(ctx context.Context, customerID uuid.UUID, page paging.Page) ([]Order, *string, error) {
	if _, err := s.registry.Customer(ctx, customerID); err != nil {
		return nil, nil, err
	}
	db := accessibleBranches(orderQuery(ctx), mustActor(ctx)).Where("service_orders.customer_id = ?", customerID)
	return listPage(db, page)
}

// EquipmentHistory lists an equipment's orders and its active warranties (RF-36).
func (s *Service) EquipmentHistory(ctx context.Context, equipmentID uuid.UUID, page paging.Page) ([]Order, *string, []Warranty, error) {
	if _, err := s.registry.GetEquipment(ctx, equipmentID); err != nil {
		return nil, nil, nil, err
	}
	db := accessibleBranches(orderQuery(ctx), mustActor(ctx)).Where("service_orders.equipment_id = ?", equipmentID)
	orders, next, err := listPage(db, page)
	if err != nil {
		return nil, nil, nil, err
	}
	warranties, err := s.activeWarranties(ctx, equipmentID, nil)
	return orders, next, warranties, err
}

// activeWarranties returns the items of delivered orders of an equipment whose warranty
// has not ended (RN-18). It looks at every branch: the equipment is shared (RN-02).
// With originOrderID, only the items of that order are returned.
func (s *Service) activeWarranties(ctx context.Context, equipmentID uuid.UUID, originOrderID *uuid.UUID) ([]Warranty, error) {
	db := database.Conn(ctx).Table("quote_items qi").
		Select(`qi.id AS item_id, so.id AS order_id, so.number AS order_number, qi.kind, qi.description,
			qi.warranty_until, so.delivered_at`).
		Joins("JOIN quotes q ON q.id = qi.quote_id AND q.status = 'approved' AND q.deleted_at IS NULL").
		Joins("JOIN service_orders so ON so.id = q.order_id AND so.deleted_at IS NULL").
		Where("qi.deleted_at IS NULL AND so.status = ? AND so.equipment_id = ?", StatusDelivered, equipmentID).
		Where("qi.warranty_until >= ?", s.now().Format(time.DateOnly)).
		Order("qi.warranty_until, qi.position")
	if originOrderID != nil {
		db = db.Where("so.id = ?", *originOrderID)
	}
	var out []Warranty
	return out, db.Scan(&out).Error
}

// History returns the status changes of an order, oldest first.
func (s *Service) History(ctx context.Context, orderID uuid.UUID) ([]StatusChange, error) {
	if _, err := s.findOrder(ctx, orderID); err != nil {
		return nil, err
	}
	var out []StatusChange
	err := database.Conn(ctx).Where("order_id = ?", orderID).Order("created_at, id").Find(&out).Error
	return out, err
}

// ---- opening ----------------------------------------------------------------------

// OpenInput opens an order (UC-01).
type OpenInput struct {
	ID                 uuid.UUID
	ReservedSequence   *int64
	CustomerID         uuid.UUID
	EquipmentID        uuid.UUID
	ReportedDefect     string
	EntryChecklist     map[string]any
	Accessories        *string
	DevicePassword     *string
	PreAuthorizedCents *int64
	ExpectedDeliveryAt *time.Time
	RequesterContactID *uuid.UUID
	CostCenterID       *uuid.UUID
	TechnicianID       *uuid.UUID
	Priority           *string

	originOrderID *uuid.UUID
}

// Opened is an opened order with the equipment's active warranties (UC-01 2b).
type Opened struct {
	Order      Order
	Warranties []Warranty
}

// Open opens a service order (UC-01, RN-04, RN-05). A repeated open with the same id
// returns the existing order.
func (s *Service) Open(ctx context.Context, in OpenInput) (Opened, error) {
	a := mustActor(ctx)
	branch := branchOf(a)
	if in.ID != uuid.Nil {
		var found []Order
		if err := database.Conn(ctx).Where("id = ?", in.ID).Limit(1).Find(&found).Error; err != nil {
			return Opened{}, err
		}
		if len(found) > 0 {
			if found[0].BranchID != branch {
				return Opened{}, apperr.Conflict("order_id_taken", "Este id já pertence a outra OS.")
			}
			return s.opened(ctx, in.ID)
		}
	}

	settings, _, err := s.location(ctx, branch)
	if err != nil {
		return Opened{}, err
	}
	if isTechnicianOnly(a) && !settings.TechnicianCanOpenAndDeliver {
		return Opened{}, errTechnicianCannotOpen
	}
	if err := s.validateOpen(ctx, &in); err != nil {
		return Opened{}, err
	}

	number, sequence, err := s.number(ctx, a, branch, in.ReservedSequence)
	if err != nil {
		return Opened{}, err
	}
	priority := "normal"
	if in.Priority != nil {
		priority = *in.Priority
	}
	o := Order{
		BranchID: branch, Sequence: sequence, Number: number,
		CustomerID: in.CustomerID, EquipmentID: in.EquipmentID, TechnicianID: in.TechnicianID,
		Priority: priority, OriginOrderID: in.originOrderID, RequesterContactID: in.RequesterContactID,
		CostCenterID: in.CostCenterID, Status: StatusReceived,
		ReportedDefect: strings.TrimSpace(in.ReportedDefect),
		EntryChecklist: database.NewJSON(in.EntryChecklist), Accessories: trimmed(in.Accessories),
		PreAuthorizedCents: in.PreAuthorizedCents, ExpectedDeliveryAt: in.ExpectedDeliveryAt,
	}
	o.ID = in.ID
	if o.ID == uuid.Nil {
		o.ID = ids.New()
	}
	if pw := trimmed(in.DevicePassword); pw != nil {
		if o.DevicePasswordEncrypted, err = s.sealer.Seal([]byte(*pw), o.ID[:]); err != nil {
			return Opened{}, err
		}
	}
	if err := database.Conn(ctx).Create(&o).Error; err != nil {
		if isUnique(err, "service_orders_sequence_key") {
			return Opened{}, errNumberTaken
		}
		return Opened{}, err
	}
	if err := s.record(ctx, o.ID, nil, StatusReceived, nil); err != nil {
		return Opened{}, err
	}
	return s.opened(ctx, o.ID)
}

func (s *Service) opened(ctx context.Context, id uuid.UUID) (Opened, error) {
	o, err := s.findOrder(ctx, id)
	if err != nil {
		return Opened{}, err
	}
	w, err := s.activeWarranties(ctx, o.EquipmentID, nil)
	return Opened{Order: o, Warranties: w}, err
}

func (s *Service) validateOpen(ctx context.Context, in *OpenInput) error {
	a := mustActor(ctx)
	if strings.TrimSpace(in.ReportedDefect) == "" {
		return apperr.Validation("Descreva o defeito relatado pelo cliente.",
			apperr.Field("reported_defect", "Obrigatório."))
	}
	customer, err := s.registry.Customer(ctx, in.CustomerID)
	if err != nil {
		return err
	}
	equipment, err := s.registry.GetEquipment(ctx, in.EquipmentID)
	if err != nil {
		return err
	}
	if equipment.CustomerID != customer.ID {
		return errEquipmentOfOther
	}
	eqType, err := s.registry.GetEquipmentType(ctx, equipment.TypeID)
	if err != nil {
		return err
	}
	if in.EntryChecklist == nil {
		in.EntryChecklist = map[string]any{}
	}
	if err := validateChecklist(eqType.Checklist, in.EntryChecklist); err != nil {
		return err
	}
	if err := s.checkContacts(ctx, customer, in.RequesterContactID, in.CostCenterID); err != nil {
		return err
	}
	if in.TechnicianID != nil {
		if err := s.checkTechnician(ctx, branchOf(a), *in.TechnicianID); err != nil {
			return err
		}
	}
	return nil
}

// checkContacts validates the requester and the cost center of an order (RN-34).
func (s *Service) checkContacts(ctx context.Context, customer registry.Customer, requesterID, costCenterID *uuid.UUID) error {
	if requesterID == nil && customer.Type == registry.CustomerCompany {
		return errRequesterRequired
	}
	if requesterID != nil {
		c, err := s.registry.Contact(ctx, customer.ID, *requesterID)
		if err != nil {
			return err
		}
		if customer.Type == registry.CustomerCompany && !c.IsRequester {
			return errNotRequester
		}
	}
	if costCenterID != nil {
		if _, err := s.registry.CostCenter(ctx, customer.ID, *costCenterID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) checkTechnician(ctx context.Context, branch, userID uuid.UUID) error {
	ok, err := s.identity.WorksInBranch(ctx, userID, branch)
	if err != nil {
		return err
	}
	if !ok {
		return errTechnicianNotInBranch
	}
	return nil
}

// number takes the next number of the branch, or validates a number reserved by the
// calling device (RN-04).
func (s *Service) number(ctx context.Context, a *actor.Actor, branch uuid.UUID, reserved *int64) (string, int64, error) {
	if reserved == nil {
		return s.identity.NextNumber(ctx, branch)
	}
	if a.DeviceID == nil {
		return "", 0, errNumberNeedsDevice
	}
	number, err := s.identity.ReservedNumber(ctx, branch, *a.DeviceID, *reserved)
	return number, *reserved, err
}

// validateChecklist checks the answers of the entry checklist against the equipment
// type: required items answered, booleans for checks, text for text items (RN-05).
func validateChecklist(items []registry.ChecklistItem, answers map[string]any) error {
	var fields []apperr.FieldError
	known := map[string]bool{}
	for _, item := range items {
		known[item.Key] = true
		field := "entry_checklist." + item.Key
		v, ok := answers[item.Key]
		if !ok || v == nil {
			if item.Required {
				fields = append(fields, apperr.Field(field, "Responda este item do checklist."))
			}
			continue
		}
		switch item.Kind {
		case "check":
			if _, isBool := v.(bool); !isBool {
				fields = append(fields, apperr.Field(field, "Use verdadeiro ou falso."))
			}
		default:
			text, isText := v.(string)
			if !isText {
				fields = append(fields, apperr.Field(field, "Use um texto."))
			} else if item.Required && strings.TrimSpace(text) == "" {
				fields = append(fields, apperr.Field(field, "Responda este item do checklist."))
			}
		}
	}
	keys := make([]string, 0, len(answers))
	for k := range answers {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !known[k] {
			fields = append(fields, apperr.Field("entry_checklist."+k, "Item fora do checklist deste tipo de equipamento."))
		}
	}
	if len(fields) > 0 {
		return apperr.Validation("Confira o checklist de entrada.", fields...)
	}
	return nil
}

// ---- updates ----------------------------------------------------------------------

// OrderUpdate changes details of an open order. Empty strings clear text fields.
type OrderUpdate struct {
	Diagnosis          *string
	TechnicalReport    *string
	Accessories        *string
	DevicePassword     *string
	ExpectedDeliveryAt *time.Time
	PreAuthorizedCents *int64
	RequesterContactID *uuid.UUID
	CostCenterID       *uuid.UUID
}

// Update records diagnosis, technical report and other details (RF-12, RF-14).
func (s *Service) Update(ctx context.Context, id uuid.UUID, in OrderUpdate) (Order, error) {
	a := mustActor(ctx)
	o, err := s.lockOrder(ctx, id)
	if err != nil {
		return o, err
	}
	if IsFinal(o.Status) {
		return o, errOrderClosed
	}
	if err := ensureCanWork(a, &o); err != nil {
		return o, err
	}
	changes := map[string]any{}
	setText := func(col string, v *string) {
		if v != nil {
			changes[col] = trimmed(v)
		}
	}
	setText("diagnosis", in.Diagnosis)
	setText("technical_report", in.TechnicalReport)
	setText("accessories", in.Accessories)
	if in.ExpectedDeliveryAt != nil {
		changes["expected_delivery_at"] = *in.ExpectedDeliveryAt
	}
	if in.PreAuthorizedCents != nil {
		changes["pre_authorized_cents"] = *in.PreAuthorizedCents
	}
	if in.RequesterContactID != nil || in.CostCenterID != nil {
		customer, err := s.registry.Customer(ctx, o.CustomerID)
		if err != nil {
			return o, err
		}
		requester := o.RequesterContactID
		if in.RequesterContactID != nil {
			requester = in.RequesterContactID
			changes["requester_contact_id"] = *in.RequesterContactID
		}
		if in.CostCenterID != nil {
			changes["cost_center_id"] = *in.CostCenterID
		}
		if err := s.checkContacts(ctx, customer, requester, in.CostCenterID); err != nil {
			return o, err
		}
	}
	if len(changes) > 0 {
		if err := database.Conn(ctx).Model(&Order{Base: database.Base{ID: o.ID}}).Updates(changes).Error; err != nil {
			return o, err
		}
	}
	if in.DevicePassword != nil {
		if err := s.setPassword(ctx, o.ID, trimmed(in.DevicePassword)); err != nil {
			return o, err
		}
	}
	return s.findOrder(ctx, o.ID)
}

// setPassword stores (or erases, with nil) the equipment password. The ciphertext is
// written outside the audited update so it never reaches the audit log.
func (s *Service) setPassword(ctx context.Context, orderID uuid.UUID, password *string) error {
	var sealed []byte
	if password != nil {
		var err error
		if sealed, err = s.sealer.Seal([]byte(*password), orderID[:]); err != nil {
			return err
		}
	}
	db := database.Conn(ctx)
	if err := db.Exec(`UPDATE service_orders SET device_password_encrypted = ? WHERE id = ?`, sealed, orderID).Error; err != nil {
		return err
	}
	action := "device_password_set"
	if password == nil {
		action = "device_password_erased"
	}
	return audit.Record(ctx, "service_orders", orderID, action, nil)
}

// RevealPassword decrypts the equipment password and audits the access (RN-31).
func (s *Service) RevealPassword(ctx context.Context, id uuid.UUID) (string, error) {
	a := mustActor(ctx)
	o, err := s.findOrder(ctx, id)
	if err != nil {
		return "", err
	}
	if err := ensureCanWork(a, &o); err != nil {
		return "", err
	}
	if !o.HasDevicePassword() {
		return "", errNoPassword
	}
	plain, err := s.sealer.Open(o.DevicePasswordEncrypted, o.ID[:])
	if err != nil {
		return "", err
	}
	if err := audit.Record(ctx, "service_orders", o.ID, "device_password_revealed", nil); err != nil {
		return "", err
	}
	return string(plain), nil
}

// AssignInput sets the technician and the priority (RF-11).
type AssignInput struct {
	TechnicianID *uuid.UUID
	Unassign     bool
	Priority     *string
}

// Assign sets the technician and the priority of an order (RF-11).
func (s *Service) Assign(ctx context.Context, id uuid.UUID, in AssignInput) (Order, error) {
	o, err := s.lockOrder(ctx, id)
	if err != nil {
		return o, err
	}
	if IsFinal(o.Status) {
		return o, errOrderClosed
	}
	changes := map[string]any{}
	switch {
	case in.Unassign:
		changes["technician_id"] = nil
	case in.TechnicianID != nil:
		if err := s.checkTechnician(ctx, o.BranchID, *in.TechnicianID); err != nil {
			return o, err
		}
		changes["technician_id"] = *in.TechnicianID
	}
	if in.Priority != nil {
		changes["priority"] = *in.Priority
	}
	if len(changes) == 0 {
		return o, nil
	}
	if err := database.Conn(ctx).Model(&Order{Base: database.Base{ID: o.ID}}).Updates(changes).Error; err != nil {
		return o, err
	}
	return s.findOrder(ctx, o.ID)
}

// ---- status flow ------------------------------------------------------------------

// Transition performs a bench step of the status flow (section 8).
func (s *Service) Transition(ctx context.Context, id uuid.UUID, to, reason string) (Order, error) {
	a := mustActor(ctx)
	o, err := s.lockOrder(ctx, id)
	if err != nil {
		return o, err
	}
	roles, ok := manualMoves[[2]string{o.Status, to}]
	if !ok {
		return o, invalidMove(o.Status, to)
	}
	if !a.HasAnyRole(roles...) {
		return o, apperr.Forbidden("transition_forbidden", "Seu perfil não pode fazer esta mudança de status.")
	}
	extra := map[string]any{}
	// A technician starting the diagnosis of an unassigned order takes it.
	if to == StatusDiagnosing && isTechnicianOnly(a) && o.TechnicianID == nil {
		o.TechnicianID = &a.UserID
		extra["technician_id"] = a.UserID
	}
	if err := ensureCanWork(a, &o); err != nil {
		return o, err
	}
	reason = strings.TrimSpace(reason)
	switch to {
	case StatusAwaitingPickup:
		if reason == "" {
			return o, apperr.Validation("Explique por que o equipamento não tem conserto.",
				apperr.Field("reason", "Obrigatório."))
		}
		extra["ready_at"] = s.now()
	case StatusReady:
		extra["ready_at"] = s.now()
	}
	if err := s.move(ctx, &o, to, reason, extra); err != nil {
		return o, err
	}
	return s.findOrder(ctx, o.ID)
}

func invalidMove(from, to string) error {
	return apperr.Conflict("invalid_transition",
		"A OS está em \""+statusLabel(from)+"\" e não pode ir para \""+statusLabel(to)+"\".")
}

// move changes the status of o along the flow, with extra column changes, and records
// the change in the history (RN-07).
func (s *Service) move(ctx context.Context, o *Order, to, reason string, extra map[string]any) error {
	if !CanMove(o.Status, to) {
		return invalidMove(o.Status, to)
	}
	changes := map[string]any{"status": to}
	for k, v := range extra {
		changes[k] = v
	}
	if err := database.Conn(ctx).Model(&Order{Base: database.Base{ID: o.ID}}).Updates(changes).Error; err != nil {
		return err
	}
	from := o.Status
	o.Status = to
	var r *string
	if reason != "" {
		r = &reason
	}
	return s.record(ctx, o.ID, &from, to, r)
}

func (s *Service) record(ctx context.Context, orderID uuid.UUID, from *string, to string, reason *string) error {
	a := mustActor(ctx)
	return database.Conn(ctx).Create(&StatusChange{
		OrderID: orderID, FromStatus: from, ToStatus: to, Reason: reason,
		UserID: &a.UserID, DeviceID: a.DeviceID, CreatedAt: s.now(),
	}).Error
}

var statusLabels = map[string]string{
	StatusReceived: "Recebida", StatusDiagnosing: "Em diagnóstico", StatusAwaitingApproval: "Aguardando aprovação",
	StatusApproved: "Aprovada", StatusInProgress: "Em execução", StatusAwaitingPart: "Aguardando peça",
	StatusTesting: "Em testes", StatusReady: "Pronta", StatusAwaitingPickup: "Aguardando retirada",
	StatusDelivered: "Entregue", StatusAbandoned: "Abandonada", StatusCanceled: "Cancelada",
}

func statusLabel(status string) string {
	if l, ok := statusLabels[status]; ok {
		return l
	}
	return status
}

// ---- helpers ----------------------------------------------------------------------

func trimmed(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil
	}
	return &v
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
