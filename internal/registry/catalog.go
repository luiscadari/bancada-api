package registry

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/luiscadari/bancada-api/internal/platform/apperr"
	"github.com/luiscadari/bancada-api/internal/platform/database"
	"github.com/luiscadari/bancada-api/internal/platform/paging"
)

var (
	errServiceNotFound  = apperr.NotFound("service_not_found", "Serviço não encontrado.")
	errProductNotFound  = apperr.NotFound("product_not_found", "Produto não encontrado.")
	errSupplierNotFound = apperr.NotFound("supplier_not_found", "Fornecedor não encontrado.")
	errSKUTaken         = apperr.Conflict("product_sku_taken", "Outro produto já usa este SKU.")
	errBarcodeTaken     = apperr.Conflict("product_barcode_taken", "Outro produto já usa este código de barras.")
)

// DefaultWarrantyDays is the suggested warranty of services and parts (RN-18).
const DefaultWarrantyDays = 90

// CatalogFilter filters services, products and suppliers.
type CatalogFilter struct {
	Query  string
	Active *bool
}

func (f CatalogFilter) apply(db *gorm.DB, searchColumns ...string) *gorm.DB {
	if f.Active != nil {
		db = db.Where("active = ?", *f.Active)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		conds := []string{"search_text(name) LIKE '%' || search_text(?) || '%'"}
		args := []any{paging.Like(q)}
		for _, col := range searchColumns {
			conds = append(conds, "lower("+col+") = lower(?)")
			args = append(args, q)
		}
		db = db.Where("("+strings.Join(conds, " OR ")+")", args...)
	}
	return db
}

// findByID loads a record or returns notFound.
func findByID[T any](ctx context.Context, id uuid.UUID, notFound error) (T, error) {
	var v T
	err := database.Conn(ctx).First(&v, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return v, notFound
	}
	return v, err
}

// existing returns the record with id when it already exists (idempotent creates).
func existing[T any](ctx context.Context, id uuid.UUID) (*T, error) {
	if id == uuid.Nil {
		return nil, nil
	}
	var v []T
	if err := database.Conn(ctx).Where("id = ?", id).Limit(1).Find(&v).Error; err != nil {
		return nil, err
	}
	if len(v) == 0 {
		return nil, nil
	}
	return &v[0], nil
}

func updateAndReload[T any](ctx context.Context, id uuid.UUID, changes map[string]any, notFound error, mapErr func(error) error) (T, error) {
	v, err := findByID[T](ctx, id, notFound)
	if err != nil || len(changes) == 0 {
		return v, err
	}
	if err := database.Conn(ctx).Model(&v).Updates(changes).Error; err != nil {
		if mapErr != nil {
			err = mapErr(err)
		}
		return v, err
	}
	return findByID[T](ctx, id, notFound)
}

// ---- services ---------------------------------------------------------------------

type ServiceInput struct {
	ID                uuid.UUID
	Name              string
	Description       *string
	PriceCents        int64
	EstimatedMinutes  *int
	WarrantyDays      *int
	CommissionPercent *float64
	Active            *bool
}

type ServiceUpdate struct {
	Name              *string
	Description       *string
	PriceCents        *int64
	EstimatedMinutes  *int
	WarrantyDays      *int
	CommissionPercent *float64
	Active            *bool
}

func (s *Service) ListServices(ctx context.Context, f CatalogFilter, page paging.Page) ([]CatalogService, *string, error) {
	var items []CatalogService
	if err := page.Apply(f.apply(database.Conn(ctx).Model(&CatalogService{})), "id").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	items, next := paging.Trim(page, items, func(v CatalogService) uuid.UUID { return v.ID })
	return items, next, nil
}

func (s *Service) GetService(ctx context.Context, id uuid.UUID) (CatalogService, error) {
	return findByID[CatalogService](ctx, id, errServiceNotFound)
}

// CreateService adds a service to the catalog (RF-04). Idempotent by ID.
func (s *Service) CreateService(ctx context.Context, in ServiceInput) (CatalogService, error) {
	if e, err := existing[CatalogService](ctx, in.ID); err != nil || e != nil {
		return deref(e), err
	}
	v := CatalogService{Name: strings.TrimSpace(in.Name), Description: trimmed(in.Description), PriceCents: in.PriceCents,
		EstimatedMinutes: in.EstimatedMinutes, WarrantyDays: DefaultWarrantyDays, Active: true}
	v.ID = in.ID
	if in.WarrantyDays != nil {
		v.WarrantyDays = *in.WarrantyDays
	}
	if in.CommissionPercent != nil {
		v.CommissionPercent = percent(*in.CommissionPercent)
	}
	if in.Active != nil {
		v.Active = *in.Active
	}
	if err := database.Conn(ctx).Create(&v).Error; err != nil {
		return v, err
	}
	return s.GetService(ctx, v.ID)
}

func (s *Service) UpdateService(ctx context.Context, id uuid.UUID, in ServiceUpdate) (CatalogService, error) {
	changes := map[string]any{}
	if in.Name != nil {
		changes["name"] = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		changes["description"] = trimmed(in.Description)
	}
	if in.PriceCents != nil {
		changes["price_cents"] = *in.PriceCents
	}
	if in.EstimatedMinutes != nil {
		changes["estimated_minutes"] = *in.EstimatedMinutes
	}
	if in.WarrantyDays != nil {
		changes["warranty_days"] = *in.WarrantyDays
	}
	if in.CommissionPercent != nil {
		changes["commission_percent"] = percent(*in.CommissionPercent)
	}
	if in.Active != nil {
		changes["active"] = *in.Active
	}
	return updateAndReload[CatalogService](ctx, id, changes, errServiceNotFound, nil)
}

// ---- products ---------------------------------------------------------------------

type ProductInput struct {
	ID           uuid.UUID
	Name         string
	Description  *string
	SKU          *string
	Barcode      *string
	Unit         *string
	CostCents    *int64
	PriceCents   int64
	MinStock     *float64
	WarrantyDays *int
	Active       *bool
}

type ProductUpdate struct {
	Name         *string
	Description  *string
	SKU          *string
	Barcode      *string
	Unit         *string
	CostCents    *int64
	PriceCents   *int64
	MinStock     *float64
	WarrantyDays *int
	Active       *bool
}

func (s *Service) ListProducts(ctx context.Context, f CatalogFilter, page paging.Page) ([]Product, *string, error) {
	var items []Product
	db := f.apply(database.Conn(ctx).Model(&Product{}), "sku", "barcode")
	if err := page.Apply(db, "id").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	items, next := paging.Trim(page, items, func(v Product) uuid.UUID { return v.ID })
	return items, next, nil
}

func (s *Service) GetProduct(ctx context.Context, id uuid.UUID) (Product, error) {
	return findByID[Product](ctx, id, errProductNotFound)
}

// CreateProduct adds a product or part (RF-05). Idempotent by ID.
func (s *Service) CreateProduct(ctx context.Context, in ProductInput) (Product, error) {
	if e, err := existing[Product](ctx, in.ID); err != nil || e != nil {
		return deref(e), err
	}
	v := Product{Name: strings.TrimSpace(in.Name), Description: trimmed(in.Description), SKU: trimmed(in.SKU),
		Barcode: trimmed(in.Barcode), Unit: "un", PriceCents: in.PriceCents, WarrantyDays: DefaultWarrantyDays, Active: true}
	v.ID = in.ID
	if u := trimmed(in.Unit); u != nil {
		v.Unit = *u
	}
	if in.CostCents != nil {
		v.CostCents = *in.CostCents
	}
	if in.MinStock != nil {
		v.MinStock = quantity(*in.MinStock)
	}
	if in.WarrantyDays != nil {
		v.WarrantyDays = *in.WarrantyDays
	}
	if in.Active != nil {
		v.Active = *in.Active
	}
	if err := database.Conn(ctx).Create(&v).Error; err != nil {
		return v, mapProductError(err)
	}
	return s.GetProduct(ctx, v.ID)
}

func (s *Service) UpdateProduct(ctx context.Context, id uuid.UUID, in ProductUpdate) (Product, error) {
	changes := map[string]any{}
	if in.Name != nil {
		changes["name"] = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		changes["description"] = trimmed(in.Description)
	}
	if in.SKU != nil {
		changes["sku"] = trimmed(in.SKU)
	}
	if in.Barcode != nil {
		changes["barcode"] = trimmed(in.Barcode)
	}
	if u := trimmed(in.Unit); u != nil {
		changes["unit"] = *u
	}
	if in.CostCents != nil {
		changes["cost_cents"] = *in.CostCents
	}
	if in.PriceCents != nil {
		changes["price_cents"] = *in.PriceCents
	}
	if in.MinStock != nil {
		changes["min_stock"] = quantity(*in.MinStock)
	}
	if in.WarrantyDays != nil {
		changes["warranty_days"] = *in.WarrantyDays
	}
	if in.Active != nil {
		changes["active"] = *in.Active
	}
	return updateAndReload[Product](ctx, id, changes, errProductNotFound, mapProductError)
}

func mapProductError(err error) error {
	switch {
	case isUnique(err, "products_sku_key"):
		return errSKUTaken
	case isUnique(err, "products_barcode_key"):
		return errBarcodeTaken
	}
	return err
}

// ---- suppliers --------------------------------------------------------------------

type SupplierInput struct {
	ID          uuid.UUID
	Name        string
	Document    *string
	Email       *string
	Phone       *string
	ContactName *string
	Notes       *string
	Active      *bool
}

type SupplierUpdate struct {
	Name        *string
	Document    *string
	Email       *string
	Phone       *string
	ContactName *string
	Notes       *string
	Active      *bool
}

func (s *Service) ListSuppliers(ctx context.Context, f CatalogFilter, page paging.Page) ([]Supplier, *string, error) {
	var items []Supplier
	db := f.apply(database.Conn(ctx).Model(&Supplier{}), "document")
	if err := page.Apply(db, "id").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	items, next := paging.Trim(page, items, func(v Supplier) uuid.UUID { return v.ID })
	return items, next, nil
}

func (s *Service) GetSupplier(ctx context.Context, id uuid.UUID) (Supplier, error) {
	return findByID[Supplier](ctx, id, errSupplierNotFound)
}

// CreateSupplier adds a supplier (RF-06). Idempotent by ID.
func (s *Service) CreateSupplier(ctx context.Context, in SupplierInput) (Supplier, error) {
	if e, err := existing[Supplier](ctx, in.ID); err != nil || e != nil {
		return deref(e), err
	}
	v := Supplier{Name: strings.TrimSpace(in.Name), Document: digitsOrNil(in.Document), Email: lowerTrimmed(in.Email),
		Phone: trimmed(in.Phone), ContactName: trimmed(in.ContactName), Notes: trimmed(in.Notes), Active: true}
	v.ID = in.ID
	if in.Active != nil {
		v.Active = *in.Active
	}
	if err := database.Conn(ctx).Create(&v).Error; err != nil {
		return v, err
	}
	return s.GetSupplier(ctx, v.ID)
}

func (s *Service) UpdateSupplier(ctx context.Context, id uuid.UUID, in SupplierUpdate) (Supplier, error) {
	changes := map[string]any{}
	if in.Name != nil {
		changes["name"] = strings.TrimSpace(*in.Name)
	}
	if in.Document != nil {
		changes["document"] = digitsOrNil(in.Document)
	}
	if in.Email != nil {
		changes["email"] = lowerTrimmed(in.Email)
	}
	if in.Phone != nil {
		changes["phone"] = trimmed(in.Phone)
	}
	if in.ContactName != nil {
		changes["contact_name"] = trimmed(in.ContactName)
	}
	if in.Notes != nil {
		changes["notes"] = trimmed(in.Notes)
	}
	if in.Active != nil {
		changes["active"] = *in.Active
	}
	return updateAndReload[Supplier](ctx, id, changes, errSupplierNotFound, nil)
}

// ---- helpers ----------------------------------------------------------------------

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func percent(v float64) decimal.Decimal  { return decimal.NewFromFloat(v).Round(2) }
func quantity(v float64) decimal.Decimal { return decimal.NewFromFloat(v).Round(3) }

func digitsOrNil(s *string) *string {
	if s == nil {
		return nil
	}
	d := onlyDigits(*s)
	if d == "" {
		return nil
	}
	return &d
}
