package registry

import (
	"context"

	"github.com/google/uuid"

	"github.com/luiscadari/bancada-api/internal/api/gen"
	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/paging"
)

// Handler implements the registry part of gen.StrictServerInterface.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func idOrNil(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---- customers --------------------------------------------------------------------

func (h *Handler) ListCustomers(ctx context.Context, req gen.ListCustomersRequestObject) (gen.ListCustomersResponseObject, error) {
	page, err := paging.Parse(req.Params.Limit, req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	items, next, err := h.svc.ListCustomers(ctx, str(req.Params.Q), page)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Customer, len(items))
	for i, c := range items {
		out[i] = toGenCustomer(c)
	}
	return gen.ListCustomers200JSONResponse{Items: out, NextCursor: next}, nil
}

func (h *Handler) CreateCustomer(ctx context.Context, req gen.CreateCustomerRequestObject) (gen.CreateCustomerResponseObject, error) {
	b := req.Body
	in := CustomerCreate{
		ID: idOrNil(b.Id), Type: string(b.Type), Name: b.Name, Document: b.Document, Notes: b.Notes,
		LegalBasis: (*string)(b.LegalBasis), Consents: fromGenConsents(b.Consents),
	}
	if b.Contacts != nil {
		for _, c := range *b.Contacts {
			in.Contacts = append(in.Contacts, fromGenContact(c))
		}
	}
	c, err := h.svc.CreateCustomer(ctx, in)
	if err != nil {
		return nil, err
	}
	return gen.CreateCustomer201JSONResponse(toGenCustomer(c)), nil
}

func (h *Handler) GetCustomer(ctx context.Context, req gen.GetCustomerRequestObject) (gen.GetCustomerResponseObject, error) {
	c, err := h.svc.GetCustomer(ctx, req.CustomerId)
	if err != nil {
		return nil, err
	}
	return gen.GetCustomer200JSONResponse(toGenCustomer(c)), nil
}

func (h *Handler) UpdateCustomer(ctx context.Context, req gen.UpdateCustomerRequestObject) (gen.UpdateCustomerResponseObject, error) {
	b := req.Body
	c, err := h.svc.UpdateCustomer(ctx, req.CustomerId, CustomerUpdate{
		Type: (*string)(b.Type), Name: b.Name, Document: b.Document, Notes: b.Notes,
		LegalBasis: (*string)(b.LegalBasis), Consents: fromGenConsents(b.Consents),
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateCustomer200JSONResponse(toGenCustomer(c)), nil
}

func (h *Handler) CreateContact(ctx context.Context, req gen.CreateContactRequestObject) (gen.CreateContactResponseObject, error) {
	c, err := h.svc.CreateContact(ctx, req.CustomerId, fromGenContact(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateContact201JSONResponse(toGenContact(c)), nil
}

func (h *Handler) UpdateContact(ctx context.Context, req gen.UpdateContactRequestObject) (gen.UpdateContactResponseObject, error) {
	b := req.Body
	c, err := h.svc.UpdateContact(ctx, req.CustomerId, req.ContactId, ContactUpdate{
		Name: b.Name, Phone: b.Phone, Email: b.Email, IsPrimary: b.IsPrimary, IsRequester: b.IsRequester,
		IsApprover: b.IsApprover, ApprovalLimitCents: b.ApprovalLimitCents,
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateContact200JSONResponse(toGenContact(c)), nil
}

func (h *Handler) DeleteContact(ctx context.Context, req gen.DeleteContactRequestObject) (gen.DeleteContactResponseObject, error) {
	if err := h.svc.DeleteContact(ctx, req.CustomerId, req.ContactId); err != nil {
		return nil, err
	}
	return gen.DeleteContact204Response{}, nil
}

func (h *Handler) CreateCostCenter(ctx context.Context, req gen.CreateCostCenterRequestObject) (gen.CreateCostCenterResponseObject, error) {
	b := req.Body
	cc, err := h.svc.CreateCostCenter(ctx, req.CustomerId, CostCenterInput{ID: idOrNil(b.Id), Name: b.Name, Code: b.Code})
	if err != nil {
		return nil, err
	}
	return gen.CreateCostCenter201JSONResponse(toGenCostCenter(cc)), nil
}

func (h *Handler) UpdateCostCenter(ctx context.Context, req gen.UpdateCostCenterRequestObject) (gen.UpdateCostCenterResponseObject, error) {
	b := req.Body
	cc, err := h.svc.UpdateCostCenter(ctx, req.CustomerId, req.CostCenterId,
		CostCenterUpdate{Name: b.Name, Code: b.Code, Active: b.Active})
	if err != nil {
		return nil, err
	}
	return gen.UpdateCostCenter200JSONResponse(toGenCostCenter(cc)), nil
}

// ---- equipment types --------------------------------------------------------------

func fromGenTypeInput(b gen.EquipmentTypeInput) EquipmentTypeInput {
	in := EquipmentTypeInput{ID: idOrNil(b.Id), Name: b.Name, Description: b.Description, Active: b.Active}
	for _, f := range b.Fields {
		fi := FieldInput{Key: f.Key, Label: f.Label, Kind: string(f.Kind), Required: f.Required}
		if f.Options != nil {
			fi.Options = *f.Options
		}
		in.Fields = append(in.Fields, fi)
	}
	for _, c := range b.Checklist {
		in.Checklist = append(in.Checklist, ChecklistInput{Key: c.Key, Label: c.Label, Kind: string(c.Kind), Required: c.Required})
	}
	return in
}

func (h *Handler) ListEquipmentTypes(ctx context.Context, _ gen.ListEquipmentTypesRequestObject) (gen.ListEquipmentTypesResponseObject, error) {
	types, err := h.svc.ListEquipmentTypes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.EquipmentType, len(types))
	for i, t := range types {
		out[i] = toGenType(t)
	}
	return gen.ListEquipmentTypes200JSONResponse{Items: out}, nil
}

func (h *Handler) CreateEquipmentType(ctx context.Context, req gen.CreateEquipmentTypeRequestObject) (gen.CreateEquipmentTypeResponseObject, error) {
	t, err := h.svc.CreateEquipmentType(ctx, fromGenTypeInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateEquipmentType201JSONResponse(toGenType(t)), nil
}

func (h *Handler) GetEquipmentType(ctx context.Context, req gen.GetEquipmentTypeRequestObject) (gen.GetEquipmentTypeResponseObject, error) {
	t, err := h.svc.GetEquipmentType(ctx, req.EquipmentTypeId)
	if err != nil {
		return nil, err
	}
	return gen.GetEquipmentType200JSONResponse(toGenType(t)), nil
}

func (h *Handler) UpdateEquipmentType(ctx context.Context, req gen.UpdateEquipmentTypeRequestObject) (gen.UpdateEquipmentTypeResponseObject, error) {
	t, err := h.svc.UpdateEquipmentType(ctx, req.EquipmentTypeId, fromGenTypeInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateEquipmentType200JSONResponse(toGenType(t)), nil
}

// ---- equipment --------------------------------------------------------------------

func (h *Handler) ListEquipment(ctx context.Context, req gen.ListEquipmentRequestObject) (gen.ListEquipmentResponseObject, error) {
	p := req.Params
	page, err := paging.Parse(p.Limit, p.Cursor)
	if err != nil {
		return nil, err
	}
	items, next, err := h.svc.ListEquipment(ctx, EquipmentFilter{Query: str(p.Q), CustomerID: p.CustomerId, SerialNumber: p.SerialNumber}, page)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Equipment, len(items))
	for i, e := range items {
		out[i] = toGenEquipment(e)
	}
	return gen.ListEquipment200JSONResponse{Items: out, NextCursor: next}, nil
}

func (h *Handler) CreateEquipment(ctx context.Context, req gen.CreateEquipmentRequestObject) (gen.CreateEquipmentResponseObject, error) {
	b := req.Body
	in := EquipmentCreate{ID: idOrNil(b.Id), CustomerID: b.CustomerId, TypeID: b.TypeId, Brand: b.Brand, Model: b.Model,
		SerialNumber: b.SerialNumber, Notes: b.Notes}
	if b.Attributes != nil {
		in.Attributes = *b.Attributes
	}
	e, err := h.svc.CreateEquipment(ctx, in)
	if err != nil {
		return nil, err
	}
	return gen.CreateEquipment201JSONResponse(toGenEquipment(e)), nil
}

func (h *Handler) GetEquipment(ctx context.Context, req gen.GetEquipmentRequestObject) (gen.GetEquipmentResponseObject, error) {
	e, err := h.svc.GetEquipment(ctx, req.EquipmentId)
	if err != nil {
		return nil, err
	}
	return gen.GetEquipment200JSONResponse(toGenEquipment(e)), nil
}

func (h *Handler) UpdateEquipment(ctx context.Context, req gen.UpdateEquipmentRequestObject) (gen.UpdateEquipmentResponseObject, error) {
	b := req.Body
	e, err := h.svc.UpdateEquipment(ctx, req.EquipmentId, EquipmentUpdate{TypeID: b.TypeId, Brand: b.Brand, Model: b.Model,
		SerialNumber: b.SerialNumber, Attributes: b.Attributes, Notes: b.Notes})
	if err != nil {
		return nil, err
	}
	return gen.UpdateEquipment200JSONResponse(toGenEquipment(e)), nil
}

// ---- services ---------------------------------------------------------------------

func catalogParams(q *string, active *bool, limit *int, cursor *string) (CatalogFilter, paging.Page, error) {
	page, err := paging.Parse(limit, cursor)
	return CatalogFilter{Query: str(q), Active: active}, page, err
}

func (h *Handler) ListServices(ctx context.Context, req gen.ListServicesRequestObject) (gen.ListServicesResponseObject, error) {
	p := req.Params
	f, page, err := catalogParams(p.Q, p.Active, p.Limit, p.Cursor)
	if err != nil {
		return nil, err
	}
	items, next, err := h.svc.ListServices(ctx, f, page)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Service, len(items))
	for i, v := range items {
		out[i] = toGenService(v)
	}
	return gen.ListServices200JSONResponse{Items: out, NextCursor: next}, nil
}

func (h *Handler) CreateService(ctx context.Context, req gen.CreateServiceRequestObject) (gen.CreateServiceResponseObject, error) {
	b := req.Body
	v, err := h.svc.CreateService(ctx, ServiceInput{ID: idOrNil(b.Id), Name: b.Name, Description: b.Description,
		PriceCents: b.PriceCents, EstimatedMinutes: b.EstimatedMinutes, WarrantyDays: b.WarrantyDays,
		CommissionPercent: b.CommissionPercent, Active: b.Active})
	if err != nil {
		return nil, err
	}
	return gen.CreateService201JSONResponse(toGenService(v)), nil
}

func (h *Handler) GetService(ctx context.Context, req gen.GetServiceRequestObject) (gen.GetServiceResponseObject, error) {
	v, err := h.svc.GetService(ctx, req.ServiceId)
	if err != nil {
		return nil, err
	}
	return gen.GetService200JSONResponse(toGenService(v)), nil
}

func (h *Handler) UpdateService(ctx context.Context, req gen.UpdateServiceRequestObject) (gen.UpdateServiceResponseObject, error) {
	b := req.Body
	v, err := h.svc.UpdateService(ctx, req.ServiceId, ServiceUpdate{Name: b.Name, Description: b.Description,
		PriceCents: b.PriceCents, EstimatedMinutes: b.EstimatedMinutes, WarrantyDays: b.WarrantyDays,
		CommissionPercent: b.CommissionPercent, Active: b.Active})
	if err != nil {
		return nil, err
	}
	return gen.UpdateService200JSONResponse(toGenService(v)), nil
}

// ---- products ---------------------------------------------------------------------

func (h *Handler) ListProducts(ctx context.Context, req gen.ListProductsRequestObject) (gen.ListProductsResponseObject, error) {
	p := req.Params
	f, page, err := catalogParams(p.Q, p.Active, p.Limit, p.Cursor)
	if err != nil {
		return nil, err
	}
	items, next, err := h.svc.ListProducts(ctx, f, page)
	if err != nil {
		return nil, err
	}
	showCost := canSeeCost(ctx)
	out := make([]gen.Product, len(items))
	for i, v := range items {
		out[i] = toGenProduct(v, showCost)
	}
	return gen.ListProducts200JSONResponse{Items: out, NextCursor: next}, nil
}

func (h *Handler) CreateProduct(ctx context.Context, req gen.CreateProductRequestObject) (gen.CreateProductResponseObject, error) {
	b := req.Body
	v, err := h.svc.CreateProduct(ctx, ProductInput{ID: idOrNil(b.Id), Name: b.Name, Description: b.Description,
		SKU: b.Sku, Barcode: b.Barcode, Unit: b.Unit, CostCents: b.CostCents, PriceCents: b.PriceCents,
		MinStock: b.MinStock, WarrantyDays: b.WarrantyDays, Active: b.Active})
	if err != nil {
		return nil, err
	}
	return gen.CreateProduct201JSONResponse(toGenProduct(v, canSeeCost(ctx))), nil
}

func (h *Handler) GetProduct(ctx context.Context, req gen.GetProductRequestObject) (gen.GetProductResponseObject, error) {
	v, err := h.svc.GetProduct(ctx, req.ProductId)
	if err != nil {
		return nil, err
	}
	return gen.GetProduct200JSONResponse(toGenProduct(v, canSeeCost(ctx))), nil
}

func (h *Handler) UpdateProduct(ctx context.Context, req gen.UpdateProductRequestObject) (gen.UpdateProductResponseObject, error) {
	b := req.Body
	v, err := h.svc.UpdateProduct(ctx, req.ProductId, ProductUpdate{Name: b.Name, Description: b.Description,
		SKU: b.Sku, Barcode: b.Barcode, Unit: b.Unit, CostCents: b.CostCents, PriceCents: b.PriceCents,
		MinStock: b.MinStock, WarrantyDays: b.WarrantyDays, Active: b.Active})
	if err != nil {
		return nil, err
	}
	return gen.UpdateProduct200JSONResponse(toGenProduct(v, canSeeCost(ctx))), nil
}

// canSeeCost implements "Ver custo de peças e margem": owners only.
func canSeeCost(ctx context.Context) bool {
	a, ok := actor.From(ctx)
	return ok && a.IsOwner()
}

// ---- suppliers --------------------------------------------------------------------

func (h *Handler) ListSuppliers(ctx context.Context, req gen.ListSuppliersRequestObject) (gen.ListSuppliersResponseObject, error) {
	p := req.Params
	f, page, err := catalogParams(p.Q, p.Active, p.Limit, p.Cursor)
	if err != nil {
		return nil, err
	}
	items, next, err := h.svc.ListSuppliers(ctx, f, page)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Supplier, len(items))
	for i, v := range items {
		out[i] = toGenSupplier(v)
	}
	return gen.ListSuppliers200JSONResponse{Items: out, NextCursor: next}, nil
}

func (h *Handler) CreateSupplier(ctx context.Context, req gen.CreateSupplierRequestObject) (gen.CreateSupplierResponseObject, error) {
	b := req.Body
	v, err := h.svc.CreateSupplier(ctx, SupplierInput{ID: idOrNil(b.Id), Name: b.Name, Document: b.Document,
		Email: (*string)(b.Email), Phone: b.Phone, ContactName: b.ContactName, Notes: b.Notes, Active: b.Active})
	if err != nil {
		return nil, err
	}
	return gen.CreateSupplier201JSONResponse(toGenSupplier(v)), nil
}

func (h *Handler) GetSupplier(ctx context.Context, req gen.GetSupplierRequestObject) (gen.GetSupplierResponseObject, error) {
	v, err := h.svc.GetSupplier(ctx, req.SupplierId)
	if err != nil {
		return nil, err
	}
	return gen.GetSupplier200JSONResponse(toGenSupplier(v)), nil
}

func (h *Handler) UpdateSupplier(ctx context.Context, req gen.UpdateSupplierRequestObject) (gen.UpdateSupplierResponseObject, error) {
	b := req.Body
	v, err := h.svc.UpdateSupplier(ctx, req.SupplierId, SupplierUpdate{Name: b.Name, Document: b.Document, Email: b.Email,
		Phone: b.Phone, ContactName: b.ContactName, Notes: b.Notes, Active: b.Active})
	if err != nil {
		return nil, err
	}
	return gen.UpdateSupplier200JSONResponse(toGenSupplier(v)), nil
}

// ---- niche packs ------------------------------------------------------------------

func (h *Handler) ApplyNichePack(ctx context.Context, req gen.ApplyNichePackRequestObject) (gen.ApplyNichePackResponseObject, error) {
	res, err := h.svc.ApplyPack(ctx, req.PackCode)
	if err != nil {
		return nil, err
	}
	return gen.ApplyNichePack200JSONResponse{EquipmentTypesAdded: res.EquipmentTypes, ServicesAdded: res.Services}, nil
}

// ---- mapping ----------------------------------------------------------------------

func fromGenConsents(c *gen.ConsentsInput) *ConsentsInput {
	if c == nil {
		return nil
	}
	return &ConsentsInput{WhatsApp: c.Whatsapp, Email: c.Email}
}

func fromGenContact(c gen.ContactInput) ContactInput {
	b := func(p *bool) bool { return p != nil && *p }
	return ContactInput{
		ID: idOrNil(c.Id), Name: c.Name, Phone: c.Phone, Email: (*string)(c.Email),
		IsPrimary: b(c.IsPrimary), IsRequester: b(c.IsRequester), IsApprover: b(c.IsApprover),
		ApprovalLimitCents: c.ApprovalLimitCents,
	}
}

func toGenCustomer(c CustomerView) gen.Customer {
	consents := c.Consents.Data()
	contacts := make([]gen.Contact, len(c.Contacts))
	for i, ct := range c.Contacts {
		contacts[i] = toGenContact(ct)
	}
	centers := make([]gen.CostCenter, len(c.CostCenters))
	for i, cc := range c.CostCenters {
		centers[i] = toGenCostCenter(cc)
	}
	return gen.Customer{
		Id: c.ID, Type: gen.CustomerType(c.Type), Name: c.Name, Document: c.Document, Notes: c.Notes,
		LegalBasis: gen.LegalBasis(c.LegalBasis),
		Consents: gen.Consents{
			Whatsapp: gen.Consent{Granted: consents.WhatsApp.Granted, UpdatedAt: consents.WhatsApp.UpdatedAt},
			Email:    gen.Consent{Granted: consents.Email.Granted, UpdatedAt: consents.Email.UpdatedAt},
		},
		Contacts: contacts, CostCenters: centers,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Version: c.Version,
	}
}

func toGenContact(c Contact) gen.Contact {
	return gen.Contact{
		Id: c.ID, CustomerId: c.CustomerID, Name: c.Name, Phone: c.Phone, Email: c.Email,
		IsPrimary: c.IsPrimary, IsRequester: c.IsRequester, IsApprover: c.IsApprover,
		ApprovalLimitCents: c.ApprovalLimitCents,
	}
}

func toGenCostCenter(c CostCenter) gen.CostCenter {
	return gen.CostCenter{Id: c.ID, CustomerId: c.CustomerID, Name: c.Name, Code: c.Code, Active: c.Active}
}

func toGenType(t EquipmentTypeView) gen.EquipmentType {
	fields := make([]gen.CustomField, len(t.Fields))
	for i, f := range t.Fields {
		fields[i] = gen.CustomField{Key: f.Key, Label: f.Label, Kind: gen.FieldKind(f.Kind), Required: f.Required}
		if f.Kind == FieldSelect {
			opts := f.Options.Data()
			fields[i].Options = &opts
		}
	}
	items := make([]gen.ChecklistItem, len(t.Checklist))
	for i, c := range t.Checklist {
		items[i] = gen.ChecklistItem{Key: c.Key, Label: c.Label, Kind: gen.ChecklistKind(c.Kind), Required: c.Required}
	}
	return gen.EquipmentType{
		Id: t.ID, Name: t.Name, Description: t.Description, Active: t.Active, Fields: fields, Checklist: items,
		FieldSchema: t.FieldSchema.Data(), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Version: t.Version,
	}
}

func toGenEquipment(e Equipment) gen.Equipment {
	attrs := e.Attributes.Data()
	if attrs == nil {
		attrs = map[string]any{}
	}
	return gen.Equipment{
		Id: e.ID, CustomerId: e.CustomerID, TypeId: e.TypeID, Brand: e.Brand, Model: e.Model,
		SerialNumber: e.SerialNumber, Attributes: attrs, Notes: e.Notes,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, Version: e.Version,
	}
}

func toGenService(v CatalogService) gen.Service {
	pct, _ := v.CommissionPercent.Float64()
	return gen.Service{
		Id: v.ID, Name: v.Name, Description: v.Description, PriceCents: v.PriceCents, EstimatedMinutes: v.EstimatedMinutes,
		WarrantyDays: v.WarrantyDays, CommissionPercent: pct, Active: v.Active,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Version: v.Version,
	}
}

func toGenProduct(v Product, showCost bool) gen.Product {
	minStock, _ := v.MinStock.Float64()
	p := gen.Product{
		Id: v.ID, Name: v.Name, Description: v.Description, Sku: v.SKU, Barcode: v.Barcode, Unit: v.Unit,
		PriceCents: v.PriceCents, MinStock: minStock, WarrantyDays: v.WarrantyDays, Active: v.Active,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Version: v.Version,
	}
	if showCost {
		cost := v.CostCents
		p.CostCents = &cost
	}
	return p
}

func toGenSupplier(v Supplier) gen.Supplier {
	return gen.Supplier{
		Id: v.ID, Name: v.Name, Document: v.Document, Email: v.Email, Phone: v.Phone, ContactName: v.ContactName,
		Notes: v.Notes, Active: v.Active, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Version: v.Version,
	}
}
