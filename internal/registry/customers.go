package registry

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/luiscadari/bancada-api/internal/platform/apperr"
	"github.com/luiscadari/bancada-api/internal/platform/database"
	"github.com/luiscadari/bancada-api/internal/platform/paging"
)

var (
	errCustomerNotFound   = apperr.NotFound("customer_not_found", "Cliente não encontrado.")
	errContactNotFound    = apperr.NotFound("contact_not_found", "Contato não encontrado.")
	errCostCenterNotFound = apperr.NotFound("cost_center_not_found", "Centro de custo não encontrado.")
	errDocumentTaken      = apperr.Conflict("customer_document_taken",
		"Já existe um cliente com este CPF/CNPJ. Busque pelo documento para usar o cadastro existente.")
	errCostCenterIndividual = apperr.Rule("cost_center_requires_company",
		"Centros de custo são só para clientes pessoa jurídica.")
)

// Service implements the registry use cases. Every method runs inside the request's
// tenant transaction (database.Conn).
type Service struct {
	now func() time.Time
}

func NewService() *Service {
	return &Service{now: func() time.Time { return time.Now().UTC() }}
}

// ---- input types ------------------------------------------------------------------

type ConsentsInput struct {
	WhatsApp *bool
	Email    *bool
}

type ContactInput struct {
	ID                 uuid.UUID
	Name               string
	Phone              *string
	Email              *string
	IsPrimary          bool
	IsRequester        bool
	IsApprover         bool
	ApprovalLimitCents *int64
}

type CustomerCreate struct {
	ID         uuid.UUID
	Type       string
	Name       string
	Document   *string
	Notes      *string
	LegalBasis *string
	Consents   *ConsentsInput
	Contacts   []ContactInput
}

type CustomerUpdate struct {
	Type       *string
	Name       *string
	Document   *string // "" clears
	Notes      *string // "" clears
	LegalBasis *string
	Consents   *ConsentsInput
}

// ---- queries ----------------------------------------------------------------------

// ListCustomers searches by name (accent-insensitive), document, phone or email.
func (s *Service) ListCustomers(ctx context.Context, q string, page paging.Page) ([]CustomerView, *string, error) {
	db := database.Conn(ctx).Model(&Customer{})
	if q = strings.TrimSpace(q); q != "" {
		conds := []string{`search_text(customers.name) LIKE '%' || search_text(?) || '%'`}
		args := []any{paging.Like(q)}
		if digits := onlyDigits(q); len(digits) >= 3 {
			conds = append(conds,
				`customers.document LIKE ?`,
				`EXISTS (SELECT 1 FROM customer_contacts cc WHERE cc.customer_id = customers.id
				   AND cc.deleted_at IS NULL AND cc.phone_digits LIKE ?)`)
			args = append(args, digits+"%", "%"+digits+"%")
		}
		if strings.Contains(q, "@") {
			conds = append(conds, `EXISTS (SELECT 1 FROM customer_contacts cc WHERE cc.customer_id = customers.id
				AND cc.deleted_at IS NULL AND lower(cc.email) = lower(?))`)
			args = append(args, q)
		}
		db = db.Where("("+strings.Join(conds, " OR ")+")", args...)
	}
	var customers []Customer
	if err := page.Apply(db, "customers.id").Find(&customers).Error; err != nil {
		return nil, nil, err
	}
	customers, next := paging.Trim(page, customers, func(c Customer) uuid.UUID { return c.ID })
	views, err := s.customerViews(ctx, customers)
	return views, next, err
}

func (s *Service) customerViews(ctx context.Context, customers []Customer) ([]CustomerView, error) {
	views := make([]CustomerView, len(customers))
	if len(customers) == 0 {
		return views, nil
	}
	ids := make([]uuid.UUID, len(customers))
	for i, c := range customers {
		ids[i] = c.ID
	}
	db := database.Conn(ctx)
	var contacts []Contact
	if err := db.Where("customer_id IN ?", ids).Order("is_primary DESC, name").Find(&contacts).Error; err != nil {
		return nil, err
	}
	var centers []CostCenter
	if err := db.Where("customer_id IN ?", ids).Order("name").Find(&centers).Error; err != nil {
		return nil, err
	}
	byContacts := map[uuid.UUID][]Contact{}
	for _, c := range contacts {
		byContacts[c.CustomerID] = append(byContacts[c.CustomerID], c)
	}
	byCenters := map[uuid.UUID][]CostCenter{}
	for _, c := range centers {
		byCenters[c.CustomerID] = append(byCenters[c.CustomerID], c)
	}
	for i, c := range customers {
		views[i] = CustomerView{Customer: c, Contacts: byContacts[c.ID], CostCenters: byCenters[c.ID]}
	}
	return views, nil
}

func (s *Service) findCustomer(ctx context.Context, id uuid.UUID) (Customer, error) {
	var c Customer
	err := database.Conn(ctx).First(&c, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c, errCustomerNotFound
	}
	return c, err
}

// GetCustomer returns a customer with contacts and cost centers.
func (s *Service) GetCustomer(ctx context.Context, id uuid.UUID) (CustomerView, error) {
	c, err := s.findCustomer(ctx, id)
	if err != nil {
		return CustomerView{}, err
	}
	views, err := s.customerViews(ctx, []Customer{c})
	if err != nil {
		return CustomerView{}, err
	}
	return views[0], nil
}

// ---- customers --------------------------------------------------------------------

// CreateCustomer registers a customer with its contacts (RF-01, UC-01 2a). Idempotent by ID.
func (s *Service) CreateCustomer(ctx context.Context, in CustomerCreate) (CustomerView, error) {
	db := database.Conn(ctx)
	if in.ID != uuid.Nil {
		if existing, err := s.GetCustomer(ctx, in.ID); err == nil {
			return existing, nil
		} else if !errors.Is(err, errCustomerNotFound) {
			return CustomerView{}, err
		}
	}
	doc, err := normalizeDocument(in.Type, in.Document)
	if err != nil {
		return CustomerView{}, err
	}
	c := Customer{
		Type: in.Type, Name: strings.TrimSpace(in.Name), Document: doc, Notes: trimmed(in.Notes),
		LegalBasis: "contract", Consents: database.NewJSON(applyConsents(Consents{}, in.Consents, s.now())),
	}
	if in.LegalBasis != nil {
		c.LegalBasis = *in.LegalBasis
	}
	c.ID = in.ID
	if err := db.Create(&c).Error; err != nil {
		return CustomerView{}, mapCustomerError(err)
	}
	// Exactly one primary contact: the first one marked as primary, or the first one.
	primary := 0
	for i, ci := range in.Contacts {
		if ci.IsPrimary {
			primary = i
			break
		}
	}
	for i, ci := range in.Contacts {
		ci.IsPrimary = i == primary
		if _, err := s.insertContact(ctx, c.ID, ci); err != nil {
			return CustomerView{}, err
		}
	}
	return s.GetCustomer(ctx, c.ID)
}

func (s *Service) UpdateCustomer(ctx context.Context, id uuid.UUID, in CustomerUpdate) (CustomerView, error) {
	c, err := s.findCustomer(ctx, id)
	if err != nil {
		return CustomerView{}, err
	}
	changes := map[string]any{}
	typ := c.Type
	if in.Type != nil {
		typ = *in.Type
		changes["type"] = typ
	}
	if in.Name != nil {
		changes["name"] = strings.TrimSpace(*in.Name)
	}
	if in.Document != nil || in.Type != nil {
		docIn := c.Document
		if in.Document != nil {
			docIn = in.Document
		}
		doc, err := normalizeDocument(typ, docIn)
		if err != nil {
			return CustomerView{}, err
		}
		changes["document"] = doc
	}
	if in.Notes != nil {
		changes["notes"] = trimmed(in.Notes)
	}
	if in.LegalBasis != nil {
		changes["legal_basis"] = *in.LegalBasis
	}
	if in.Consents != nil {
		changes["consents"] = database.NewJSON(applyConsents(c.Consents.Data(), in.Consents, s.now()))
	}
	if len(changes) > 0 {
		if err := database.Conn(ctx).Model(&c).Updates(changes).Error; err != nil {
			return CustomerView{}, mapCustomerError(err)
		}
	}
	return s.GetCustomer(ctx, id)
}

func applyConsents(current Consents, in *ConsentsInput, now time.Time) Consents {
	if in == nil {
		return current
	}
	if in.WhatsApp != nil && (*in.WhatsApp != current.WhatsApp.Granted || current.WhatsApp.UpdatedAt == nil) {
		current.WhatsApp = Consent{Granted: *in.WhatsApp, UpdatedAt: &now}
	}
	if in.Email != nil && (*in.Email != current.Email.Granted || current.Email.UpdatedAt == nil) {
		current.Email = Consent{Granted: *in.Email, UpdatedAt: &now}
	}
	return current
}

// ---- contacts ---------------------------------------------------------------------

func (s *Service) insertContact(ctx context.Context, customerID uuid.UUID, in ContactInput) (Contact, error) {
	db := database.Conn(ctx)
	if in.ID != uuid.Nil {
		var existing Contact
		if err := db.Where("id = ? AND customer_id = ?", in.ID, customerID).Limit(1).Find(&existing).Error; err != nil {
			return existing, err
		}
		if existing.ID != uuid.Nil {
			return existing, nil
		}
	}
	if in.IsPrimary {
		if err := s.clearPrimary(ctx, customerID); err != nil {
			return Contact{}, err
		}
	}
	c := Contact{
		CustomerID: customerID, Name: strings.TrimSpace(in.Name), Phone: trimmed(in.Phone), Email: lowerTrimmed(in.Email),
		IsPrimary: in.IsPrimary, IsRequester: in.IsRequester, IsApprover: in.IsApprover,
		ApprovalLimitCents: in.ApprovalLimitCents,
	}
	c.ID = in.ID
	return c, db.Create(&c).Error
}

func (s *Service) clearPrimary(ctx context.Context, customerID uuid.UUID) error {
	return database.Conn(ctx).Model(&Contact{}).Where("customer_id = ? AND is_primary", customerID).
		Update("is_primary", false).Error
}

func (s *Service) CreateContact(ctx context.Context, customerID uuid.UUID, in ContactInput) (Contact, error) {
	if _, err := s.findCustomer(ctx, customerID); err != nil {
		return Contact{}, err
	}
	var count int64
	if err := database.Conn(ctx).Model(&Contact{}).Where("customer_id = ?", customerID).Count(&count).Error; err != nil {
		return Contact{}, err
	}
	if count == 0 {
		in.IsPrimary = true
	}
	return s.insertContact(ctx, customerID, in)
}

type ContactUpdate struct {
	Name               *string
	Phone              *string // "" clears
	Email              *string // "" clears
	IsPrimary          *bool
	IsRequester        *bool
	IsApprover         *bool
	ApprovalLimitCents *int64 // -1 clears
}

func (s *Service) findContact(ctx context.Context, customerID, contactID uuid.UUID) (Contact, error) {
	var c Contact
	err := database.Conn(ctx).First(&c, "id = ? AND customer_id = ?", contactID, customerID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c, errContactNotFound
	}
	return c, err
}

func (s *Service) UpdateContact(ctx context.Context, customerID, contactID uuid.UUID, in ContactUpdate) (Contact, error) {
	c, err := s.findContact(ctx, customerID, contactID)
	if err != nil {
		return c, err
	}
	changes := map[string]any{}
	if in.Name != nil {
		changes["name"] = strings.TrimSpace(*in.Name)
	}
	if in.Phone != nil {
		changes["phone"] = trimmed(in.Phone)
	}
	if in.Email != nil {
		changes["email"] = lowerTrimmed(in.Email)
	}
	if in.IsPrimary != nil {
		if *in.IsPrimary {
			if err := s.clearPrimary(ctx, customerID); err != nil {
				return c, err
			}
		}
		changes["is_primary"] = *in.IsPrimary
	}
	if in.IsRequester != nil {
		changes["is_requester"] = *in.IsRequester
	}
	if in.IsApprover != nil {
		changes["is_approver"] = *in.IsApprover
	}
	if in.ApprovalLimitCents != nil {
		if *in.ApprovalLimitCents < 0 {
			changes["approval_limit_cents"] = nil
		} else {
			changes["approval_limit_cents"] = *in.ApprovalLimitCents
		}
	}
	if len(changes) == 0 {
		return c, nil
	}
	err = database.Conn(ctx).Model(&c).Clauses(clause.Returning{}).Updates(changes).Error
	return c, err
}

func (s *Service) DeleteContact(ctx context.Context, customerID, contactID uuid.UUID) error {
	c, err := s.findContact(ctx, customerID, contactID)
	if err != nil {
		return err
	}
	return database.Conn(ctx).Delete(&c).Error
}

// ---- cost centers -----------------------------------------------------------------

type CostCenterInput struct {
	ID   uuid.UUID
	Name string
	Code *string
}

func (s *Service) CreateCostCenter(ctx context.Context, customerID uuid.UUID, in CostCenterInput) (CostCenter, error) {
	cust, err := s.findCustomer(ctx, customerID)
	if err != nil {
		return CostCenter{}, err
	}
	if cust.Type != CustomerCompany {
		return CostCenter{}, errCostCenterIndividual
	}
	db := database.Conn(ctx)
	if in.ID != uuid.Nil {
		var existing CostCenter
		if err := db.Where("id = ? AND customer_id = ?", in.ID, customerID).Limit(1).Find(&existing).Error; err != nil {
			return existing, err
		}
		if existing.ID != uuid.Nil {
			return existing, nil
		}
	}
	cc := CostCenter{CustomerID: customerID, Name: strings.TrimSpace(in.Name), Code: trimmed(in.Code), Active: true}
	cc.ID = in.ID
	return cc, db.Create(&cc).Error
}

type CostCenterUpdate struct {
	Name   *string
	Code   *string
	Active *bool
}

func (s *Service) UpdateCostCenter(ctx context.Context, customerID, id uuid.UUID, in CostCenterUpdate) (CostCenter, error) {
	var cc CostCenter
	db := database.Conn(ctx)
	if err := db.First(&cc, "id = ? AND customer_id = ?", id, customerID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return cc, errCostCenterNotFound
		}
		return cc, err
	}
	changes := map[string]any{}
	if in.Name != nil {
		changes["name"] = strings.TrimSpace(*in.Name)
	}
	if in.Code != nil {
		changes["code"] = trimmed(in.Code)
	}
	if in.Active != nil {
		changes["active"] = *in.Active
	}
	if len(changes) == 0 {
		return cc, nil
	}
	return cc, db.Model(&cc).Clauses(clause.Returning{}).Updates(changes).Error
}

// ---- lookups used by other modules ------------------------------------------------

// Contact returns a contact of a customer.
func (s *Service) Contact(ctx context.Context, customerID, contactID uuid.UUID) (Contact, error) {
	return s.findContact(ctx, customerID, contactID)
}

// CostCenter returns an active cost center of a customer.
func (s *Service) CostCenter(ctx context.Context, customerID, id uuid.UUID) (CostCenter, error) {
	var cc CostCenter
	err := database.Conn(ctx).First(&cc, "id = ? AND customer_id = ? AND active", id, customerID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cc, errCostCenterNotFound
	}
	return cc, err
}

// Customer returns a customer without its related records.
func (s *Service) Customer(ctx context.Context, id uuid.UUID) (Customer, error) {
	return s.findCustomer(ctx, id)
}

// ---- helpers ----------------------------------------------------------------------

func mapCustomerError(err error) error {
	if isUnique(err, "customers_document_key") {
		return errDocumentTaken
	}
	return err
}

func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}

func lowerTrimmed(s *string) *string {
	v := trimmed(s)
	if v == nil {
		return nil
	}
	l := strings.ToLower(*v)
	return &l
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

// normalizeDocument keeps the digits of a CPF (individual) or CNPJ (company) and checks
// its verification digits. Empty means no document.
func normalizeDocument(customerType string, doc *string) (*string, error) {
	if doc == nil {
		return nil, nil
	}
	d := onlyDigits(*doc)
	if d == "" {
		return nil, nil
	}
	switch customerType {
	case CustomerIndividual:
		if !validCPF(d) {
			return nil, apperr.Validation("CPF inválido. Confira os 11 dígitos.", apperr.Field("document", "CPF inválido"))
		}
	case CustomerCompany:
		if !validCNPJ(d) {
			return nil, apperr.Validation("CNPJ inválido. Confira os 14 dígitos.", apperr.Field("document", "CNPJ inválido"))
		}
	}
	return &d, nil
}

func allSame(d string) bool {
	return strings.Count(d, d[:1]) == len(d)
}

func validCPF(d string) bool {
	if len(d) != 11 || allSame(d) {
		return false
	}
	check := func(n int) int {
		sum := 0
		for i := 0; i < n; i++ {
			sum += int(d[i]-'0') * (n + 1 - i)
		}
		r := sum * 10 % 11
		if r == 10 {
			r = 0
		}
		return r
	}
	return check(9) == int(d[9]-'0') && check(10) == int(d[10]-'0')
}

func validCNPJ(d string) bool {
	if len(d) != 14 || allSame(d) {
		return false
	}
	check := func(n int) int {
		weights := []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}[13-n:]
		sum := 0
		for i := 0; i < n; i++ {
			sum += int(d[i]-'0') * weights[i]
		}
		r := sum % 11
		if r < 2 {
			return 0
		}
		return 11 - r
	}
	return check(12) == int(d[12]-'0') && check(13) == int(d[13]-'0')
}
