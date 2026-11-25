package registry

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/luiscadari/bancada-api/internal/platform/apperr"
	"github.com/luiscadari/bancada-api/internal/platform/database"
)

//go:embed packs/*.json
var packFS embed.FS

// NichePack is the initial configuration of a business niche (RF-08, ADR-05).
type NichePack struct {
	Code           string `json:"code"`
	EquipmentTypes []struct {
		Name        string           `json:"name"`
		Description string           `json:"description"`
		Fields      []packField      `json:"fields"`
		Checklist   []ChecklistInput `json:"checklist"`
	} `json:"equipment_types"`
	Services []struct {
		Name             string `json:"name"`
		Description      string `json:"description"`
		PriceCents       int64  `json:"price_cents"`
		EstimatedMinutes int    `json:"estimated_minutes"`
		WarrantyDays     int    `json:"warranty_days"`
	} `json:"services"`
}

type packField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required"`
	Options  []string `json:"options"`
}

// LoadPack reads an embedded niche pack.
func LoadPack(code string) (*NichePack, error) {
	b, err := packFS.ReadFile("packs/" + code + ".json")
	if err != nil {
		return nil, apperr.NotFound("niche_pack_not_found", "Pacote de nicho não encontrado.")
	}
	var p NichePack
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("niche pack %s: %w", code, err)
	}
	return &p, nil
}

// PackResult counts what a pack added.
type PackResult struct {
	EquipmentTypes int
	Services       int
}

// ApplyPack adds the pack's equipment types and services, skipping names that already
// exist so it can run more than once.
func (s *Service) ApplyPack(ctx context.Context, code string) (PackResult, error) {
	var res PackResult
	pack, err := LoadPack(code)
	if err != nil {
		return res, err
	}
	db := database.Conn(ctx)
	exists := func(model any, name string) (bool, error) {
		var n int64
		err := db.Model(model).Where("search_text(name) = search_text(?)", name).Count(&n).Error
		return n > 0, err
	}
	for _, t := range pack.EquipmentTypes {
		found, err := exists(&EquipmentType{}, t.Name)
		if err != nil {
			return res, err
		}
		if found {
			continue
		}
		in := EquipmentTypeInput{Name: t.Name, Checklist: t.Checklist}
		if t.Description != "" {
			in.Description = &t.Description
		}
		for _, f := range t.Fields {
			in.Fields = append(in.Fields, FieldInput(f))
		}
		if _, err := s.CreateEquipmentType(ctx, in); err != nil {
			return res, err
		}
		res.EquipmentTypes++
	}
	for _, sv := range pack.Services {
		found, err := exists(&CatalogService{}, sv.Name)
		if err != nil {
			return res, err
		}
		if found {
			continue
		}
		in := ServiceInput{Name: sv.Name, PriceCents: sv.PriceCents, WarrantyDays: &sv.WarrantyDays}
		if sv.Description != "" {
			in.Description = &sv.Description
		}
		if sv.EstimatedMinutes > 0 {
			in.EstimatedMinutes = &sv.EstimatedMinutes
		}
		if _, err := s.CreateService(ctx, in); err != nil {
			return res, err
		}
		res.Services++
	}
	return res, nil
}

// OnAccountCreated applies the account's niche pack at signup when requested.
func (s *Service) OnAccountCreated(ctx context.Context, accountID, branchID uuid.UUID, niche string, applyPack bool) error {
	if !applyPack {
		return nil
	}
	_, err := s.ApplyPack(ctx, niche)
	return err
}
