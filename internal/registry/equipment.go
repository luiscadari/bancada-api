package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gorm.io/gorm"

	"github.com/luiscadari/bancada-api/internal/platform/apperr"
	"github.com/luiscadari/bancada-api/internal/platform/database"
	"github.com/luiscadari/bancada-api/internal/platform/paging"
)

var (
	errTypeNotFound      = apperr.NotFound("equipment_type_not_found", "Tipo de equipamento não encontrado.")
	errTypeInactive      = apperr.Rule("equipment_type_inactive", "Este tipo de equipamento está inativo. Escolha outro tipo.")
	errTypeNameTaken     = apperr.Conflict("equipment_type_name_taken", "Já existe um tipo de equipamento com este nome.")
	errEquipmentNotFound = apperr.NotFound("equipment_not_found", "Equipamento não encontrado.")
)

// ---- equipment types --------------------------------------------------------------

type FieldInput struct {
	Key      string
	Label    string
	Kind     string
	Required bool
	Options  []string
}

type ChecklistInput struct {
	Key      string
	Label    string
	Kind     string
	Required bool
}

type EquipmentTypeInput struct {
	ID          uuid.UUID
	Name        string
	Description *string
	Active      *bool
	Fields      []FieldInput
	Checklist   []ChecklistInput
}

func validateTypeInput(in EquipmentTypeInput) error {
	seen := map[string]bool{}
	for i, f := range in.Fields {
		if seen[f.Key] {
			return apperr.Validation("Cada campo precisa de uma chave diferente.",
				apperr.Field(fmt.Sprintf("fields[%d].key", i), "chave repetida"))
		}
		seen[f.Key] = true
		if f.Kind == FieldSelect && len(f.Options) == 0 {
			return apperr.Validation("Informe as opções do campo de seleção.",
				apperr.Field(fmt.Sprintf("fields[%d].options", i), "obrigatório para kind=select"))
		}
	}
	seen = map[string]bool{}
	for i, c := range in.Checklist {
		if seen[c.Key] {
			return apperr.Validation("Cada item do checklist precisa de uma chave diferente.",
				apperr.Field(fmt.Sprintf("checklist[%d].key", i), "chave repetida"))
		}
		seen[c.Key] = true
	}
	return nil
}

// BuildFieldSchema returns the JSON Schema (draft 2020-12) that validates the
// attributes of equipment of a type (ADR-05).
func BuildFieldSchema(fields []FieldInput) map[string]any {
	props := map[string]any{}
	required := []any{}
	for _, f := range fields {
		var p map[string]any
		switch f.Kind {
		case FieldText:
			p = map[string]any{"type": "string", "maxLength": 1000}
		case FieldNumber:
			p = map[string]any{"type": "number"}
		case FieldInteger:
			p = map[string]any{"type": "integer"}
		case FieldBoolean:
			p = map[string]any{"type": "boolean"}
		case FieldDate:
			p = map[string]any{"type": "string", "format": "date"}
		case FieldSelect:
			enum := make([]any, len(f.Options))
			for i, o := range f.Options {
				enum[i] = o
			}
			p = map[string]any{"type": "string", "enum": enum}
		}
		p["title"] = f.Label
		props[f.Key] = p
		if f.Required {
			required = append(required, f.Key)
		}
	}
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// validateAttributes checks equipment attributes against the type's schema.
func validateAttributes(schema map[string]any, attrs map[string]any) error {
	toJSON := func(v any) (any, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return jsonschema.UnmarshalJSON(bytes.NewReader(b))
	}
	doc, err := toJSON(schema)
	if err != nil {
		return err
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource("bancada://equipment-type.json", doc); err != nil {
		return err
	}
	sch, err := c.Compile("bancada://equipment-type.json")
	if err != nil {
		return fmt.Errorf("compile field schema: %w", err)
	}
	inst, err := toJSON(attrs)
	if err != nil {
		return err
	}
	verr := sch.Validate(inst)
	if verr == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(verr, &ve) {
		return verr
	}
	var fields []apperr.FieldError
	for _, u := range ve.BasicOutput().Errors {
		if u.Error == nil {
			continue
		}
		field := "attributes" + strings.ReplaceAll(u.InstanceLocation, "/", ".")
		fields = append(fields, apperr.Field(field, u.Error.String()))
	}
	return apperr.Validation("Confira os campos do equipamento.", fields...)
}

func (s *Service) ListEquipmentTypes(ctx context.Context) ([]EquipmentTypeView, error) {
	var types []EquipmentType
	if err := database.Conn(ctx).Order("name").Find(&types).Error; err != nil {
		return nil, err
	}
	return s.typeViews(ctx, types)
}

func (s *Service) typeViews(ctx context.Context, types []EquipmentType) ([]EquipmentTypeView, error) {
	views := make([]EquipmentTypeView, len(types))
	if len(types) == 0 {
		return views, nil
	}
	ids := make([]uuid.UUID, len(types))
	for i, t := range types {
		ids[i] = t.ID
	}
	db := database.Conn(ctx)
	var fields []CustomField
	if err := db.Where("type_id IN ?", ids).Order("position").Find(&fields).Error; err != nil {
		return nil, err
	}
	var items []ChecklistItem
	if err := db.Where("type_id IN ?", ids).Order("position").Find(&items).Error; err != nil {
		return nil, err
	}
	fieldsBy := map[uuid.UUID][]CustomField{}
	for _, f := range fields {
		fieldsBy[f.TypeID] = append(fieldsBy[f.TypeID], f)
	}
	itemsBy := map[uuid.UUID][]ChecklistItem{}
	for _, it := range items {
		itemsBy[it.TypeID] = append(itemsBy[it.TypeID], it)
	}
	for i, t := range types {
		views[i] = EquipmentTypeView{EquipmentType: t, Fields: fieldsBy[t.ID], Checklist: itemsBy[t.ID]}
	}
	return views, nil
}

func (s *Service) findType(ctx context.Context, id uuid.UUID) (EquipmentType, error) {
	var t EquipmentType
	err := database.Conn(ctx).First(&t, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return t, errTypeNotFound
	}
	return t, err
}

// GetEquipmentType returns a type with its fields and checklist.
func (s *Service) GetEquipmentType(ctx context.Context, id uuid.UUID) (EquipmentTypeView, error) {
	t, err := s.findType(ctx, id)
	if err != nil {
		return EquipmentTypeView{}, err
	}
	views, err := s.typeViews(ctx, []EquipmentType{t})
	if err != nil {
		return EquipmentTypeView{}, err
	}
	return views[0], nil
}

// CreateEquipmentType adds a configurable type (RF-03). Idempotent by ID.
func (s *Service) CreateEquipmentType(ctx context.Context, in EquipmentTypeInput) (EquipmentTypeView, error) {
	if in.ID != uuid.Nil {
		if v, err := s.GetEquipmentType(ctx, in.ID); err == nil {
			return v, nil
		} else if !errors.Is(err, errTypeNotFound) {
			return EquipmentTypeView{}, err
		}
	}
	if err := validateTypeInput(in); err != nil {
		return EquipmentTypeView{}, err
	}
	t := EquipmentType{Name: strings.TrimSpace(in.Name), Description: trimmed(in.Description), Active: true,
		FieldSchema: database.NewJSON(BuildFieldSchema(in.Fields))}
	if in.Active != nil {
		t.Active = *in.Active
	}
	t.ID = in.ID
	if err := database.Conn(ctx).Create(&t).Error; err != nil {
		return EquipmentTypeView{}, mapTypeError(err)
	}
	if err := s.replaceTypeParts(ctx, t.ID, in); err != nil {
		return EquipmentTypeView{}, err
	}
	return s.GetEquipmentType(ctx, t.ID)
}

// UpdateEquipmentType replaces the definition of a type, including its fields and checklist.
func (s *Service) UpdateEquipmentType(ctx context.Context, id uuid.UUID, in EquipmentTypeInput) (EquipmentTypeView, error) {
	t, err := s.findType(ctx, id)
	if err != nil {
		return EquipmentTypeView{}, err
	}
	if err := validateTypeInput(in); err != nil {
		return EquipmentTypeView{}, err
	}
	changes := map[string]any{
		"name":         strings.TrimSpace(in.Name),
		"description":  trimmed(in.Description),
		"field_schema": database.NewJSON(BuildFieldSchema(in.Fields)),
	}
	if in.Active != nil {
		changes["active"] = *in.Active
	}
	db := database.Conn(ctx)
	if err := db.Model(&t).Updates(changes).Error; err != nil {
		return EquipmentTypeView{}, mapTypeError(err)
	}
	if err := db.Where("type_id = ?", id).Delete(&CustomField{}).Error; err != nil {
		return EquipmentTypeView{}, err
	}
	if err := db.Where("type_id = ?", id).Delete(&ChecklistItem{}).Error; err != nil {
		return EquipmentTypeView{}, err
	}
	if err := s.replaceTypeParts(ctx, id, in); err != nil {
		return EquipmentTypeView{}, err
	}
	return s.GetEquipmentType(ctx, id)
}

func (s *Service) replaceTypeParts(ctx context.Context, typeID uuid.UUID, in EquipmentTypeInput) error {
	db := database.Conn(ctx)
	for i, f := range in.Fields {
		opts := f.Options
		if f.Kind != FieldSelect || opts == nil {
			opts = []string{}
		}
		cf := CustomField{TypeID: typeID, Key: f.Key, Label: strings.TrimSpace(f.Label), Kind: f.Kind,
			Required: f.Required, Options: database.NewJSON(opts), Position: i}
		if err := db.Create(&cf).Error; err != nil {
			return err
		}
	}
	for i, c := range in.Checklist {
		item := ChecklistItem{TypeID: typeID, Key: c.Key, Label: strings.TrimSpace(c.Label), Kind: c.Kind,
			Required: c.Required, Position: i}
		if err := db.Create(&item).Error; err != nil {
			return err
		}
	}
	return nil
}

func mapTypeError(err error) error {
	if isUnique(err, "equipment_types_name_key") {
		return errTypeNameTaken
	}
	return err
}

func isUnique(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

// ---- equipment --------------------------------------------------------------------

type EquipmentCreate struct {
	ID           uuid.UUID
	CustomerID   uuid.UUID
	TypeID       uuid.UUID
	Brand        *string
	Model        *string
	SerialNumber *string
	Attributes   map[string]any
	Notes        *string
}

type EquipmentUpdate struct {
	TypeID       *uuid.UUID
	Brand        *string
	Model        *string
	SerialNumber *string
	Attributes   *map[string]any
	Notes        *string
}

type EquipmentFilter struct {
	Query        string
	CustomerID   *uuid.UUID
	SerialNumber *string
}

func (s *Service) ListEquipment(ctx context.Context, f EquipmentFilter, page paging.Page) ([]Equipment, *string, error) {
	db := database.Conn(ctx).Model(&Equipment{})
	if f.CustomerID != nil {
		db = db.Where("customer_id = ?", *f.CustomerID)
	}
	if f.SerialNumber != nil && strings.TrimSpace(*f.SerialNumber) != "" {
		db = db.Where("lower(serial_number) = lower(?)", strings.TrimSpace(*f.SerialNumber))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + paging.Like(q) + "%"
		db = db.Where(`(lower(serial_number) LIKE lower(?) OR search_text(brand) LIKE search_text(?)
			OR search_text(model) LIKE search_text(?))`, like, like, like)
	}
	var items []Equipment
	if err := page.Apply(db, "id").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	items, next := paging.Trim(page, items, func(e Equipment) uuid.UUID { return e.ID })
	return items, next, nil
}

func (s *Service) GetEquipment(ctx context.Context, id uuid.UUID) (Equipment, error) {
	var e Equipment
	err := database.Conn(ctx).First(&e, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return e, errEquipmentNotFound
	}
	return e, err
}

func (s *Service) activeType(ctx context.Context, id uuid.UUID) (EquipmentType, error) {
	t, err := s.findType(ctx, id)
	if err != nil {
		return t, err
	}
	if !t.Active {
		return t, errTypeInactive
	}
	return t, nil
}

// CreateEquipment registers a customer's equipment (RF-02). Idempotent by ID.
func (s *Service) CreateEquipment(ctx context.Context, in EquipmentCreate) (Equipment, error) {
	if in.ID != uuid.Nil {
		if e, err := s.GetEquipment(ctx, in.ID); err == nil {
			return e, nil
		} else if !errors.Is(err, errEquipmentNotFound) {
			return Equipment{}, err
		}
	}
	if _, err := s.findCustomer(ctx, in.CustomerID); err != nil {
		return Equipment{}, err
	}
	t, err := s.activeType(ctx, in.TypeID)
	if err != nil {
		return Equipment{}, err
	}
	attrs := in.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}
	if err := validateAttributes(t.FieldSchema.Data(), attrs); err != nil {
		return Equipment{}, err
	}
	e := Equipment{CustomerID: in.CustomerID, TypeID: in.TypeID, Brand: trimmed(in.Brand), Model: trimmed(in.Model),
		SerialNumber: trimmed(in.SerialNumber), Attributes: database.NewJSON(attrs), Notes: trimmed(in.Notes)}
	e.ID = in.ID
	if err := database.Conn(ctx).Create(&e).Error; err != nil {
		return Equipment{}, err
	}
	return s.GetEquipment(ctx, e.ID)
}

func (s *Service) UpdateEquipment(ctx context.Context, id uuid.UUID, in EquipmentUpdate) (Equipment, error) {
	e, err := s.GetEquipment(ctx, id)
	if err != nil {
		return e, err
	}
	changes := map[string]any{}
	typeID := e.TypeID
	if in.TypeID != nil {
		typeID = *in.TypeID
		changes["type_id"] = typeID
	}
	if in.Attributes != nil || in.TypeID != nil {
		t, err := s.activeType(ctx, typeID)
		if err != nil {
			return e, err
		}
		attrs := e.Attributes.Data()
		if in.Attributes != nil {
			attrs = *in.Attributes
		}
		if attrs == nil {
			attrs = map[string]any{}
		}
		if err := validateAttributes(t.FieldSchema.Data(), attrs); err != nil {
			return e, err
		}
		changes["attributes"] = database.NewJSON(attrs)
	}
	if in.Brand != nil {
		changes["brand"] = trimmed(in.Brand)
	}
	if in.Model != nil {
		changes["model"] = trimmed(in.Model)
	}
	if in.SerialNumber != nil {
		changes["serial_number"] = trimmed(in.SerialNumber)
	}
	if in.Notes != nil {
		changes["notes"] = trimmed(in.Notes)
	}
	if len(changes) > 0 {
		if err := database.Conn(ctx).Model(&e).Updates(changes).Error; err != nil {
			return e, err
		}
	}
	return s.GetEquipment(ctx, id)
}
