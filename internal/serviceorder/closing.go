package serviceorder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/apperr"
	"github.com/luiscadari/bancada-api/internal/platform/database"
)

// DeliveryInput hands the equipment back (UC-05).
type DeliveryInput struct {
	PickedUpByName       *string
	PickedUpByDocument   *string
	AuthorizeOpenBalance bool
}

// Deliver hands the equipment back (UC-05). An amount due that was not paid needs the
// owner's authorization (RN-15); the approved items start their warranty (RN-18) and
// the equipment password is erased (RN-31).
func (s *Service) Deliver(ctx context.Context, id uuid.UUID, in DeliveryInput) (Order, error) {
	a := mustActor(ctx)
	o, err := s.lockOrder(ctx, id)
	if err != nil {
		return o, err
	}
	settings, loc, err := s.location(ctx, o.BranchID)
	if err != nil {
		return o, err
	}
	if isTechnicianOnly(a) && !settings.TechnicianCanOpenAndDeliver {
		return o, errTechnicianCannotOpen
	}
	if !CanMove(o.Status, StatusDelivered) {
		return o, invalidMove(o.Status, StatusDelivered)
	}
	now := s.now()
	extra := map[string]any{
		"delivered_at":          now,
		"picked_up_by_name":     trimmed(in.PickedUpByName),
		"picked_up_by_document": trimmed(in.PickedUpByDocument),
	}

	paid, err := s.ledger.Paid(ctx, o.ID)
	if err != nil {
		return o, err
	}
	if balance := o.AmountDueCents() - paid; balance > 0 {
		if !in.AuthorizeOpenBalance {
			return o, apperr.Rule("open_balance", fmt.Sprintf(
				"Há saldo em aberto de %s. Receba o pagamento ou peça ao dono para autorizar a entrega.", brl(balance)))
		}
		if !a.HasAnyRole(actor.RoleOwner) {
			return o, errOpenBalanceOwner
		}
		if err := s.ledger.OpenReceivable(ctx, o.BranchID, o.ID, balance); err != nil {
			return o, err
		}
		extra["open_balance_cents"] = balance
		extra["balance_authorized_by"] = a.UserID
	}

	// Each approved item is covered from the delivery date, in the branch's calendar.
	deliveryDate := now.In(loc).Format(time.DateOnly)
	if err := database.Conn(ctx).Exec(`
		UPDATE quote_items SET warranty_until = ?::date + warranty_days
		WHERE warranty_days > 0 AND deleted_at IS NULL AND quote_id IN
		      (SELECT id FROM quotes WHERE order_id = ? AND status = 'approved' AND deleted_at IS NULL)`,
		deliveryDate, o.ID).Error; err != nil {
		return o, err
	}
	if err := s.inventory.Consume(ctx, o.BranchID, o.ID); err != nil {
		return o, err
	}
	if err := s.move(ctx, &o, StatusDelivered, "", extra); err != nil {
		return o, err
	}
	if o.HasDevicePassword() {
		if err := s.setPassword(ctx, o.ID, nil); err != nil {
			return o, err
		}
	}
	return s.findOrder(ctx, o.ID)
}

// Cancel interrupts the order with a reason (RN-08). The role check (owner or
// attendant) is done by the route policy; the status flow only allows canceling
// before the execution starts, so no part was applied.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID, reason string) (Order, error) {
	o, err := s.lockOrder(ctx, id)
	if err != nil {
		return o, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return o, apperr.Validation("Informe o motivo do cancelamento.", apperr.Field("reason", "Obrigatório."))
	}
	if !CanMove(o.Status, StatusCanceled) {
		return o, invalidMove(o.Status, StatusCanceled)
	}
	if err := s.move(ctx, &o, StatusCanceled, reason, map[string]any{
		"canceled_at": s.now(), "cancel_reason": reason,
	}); err != nil {
		return o, err
	}
	if err := s.inventory.Release(ctx, o.BranchID, o.ID); err != nil {
		return o, err
	}
	if o.HasDevicePassword() {
		if err := s.setPassword(ctx, o.ID, nil); err != nil {
			return o, err
		}
	}
	return s.findOrder(ctx, o.ID)
}

// Abandon marks an equipment not picked up within the branch's term as abandoned
// (RN-16). Only owners reach this operation.
func (s *Service) Abandon(ctx context.Context, id uuid.UUID, reason string) (Order, error) {
	o, err := s.lockOrder(ctx, id)
	if err != nil {
		return o, err
	}
	if !CanMove(o.Status, StatusAbandoned) {
		return o, invalidMove(o.Status, StatusAbandoned)
	}
	settings, loc, err := s.location(ctx, o.BranchID)
	if err != nil {
		return o, err
	}
	since := o.UpdatedAt
	if o.ReadyAt != nil {
		since = *o.ReadyAt
	}
	deadline := since.AddDate(0, 0, settings.AbandonmentDays)
	if s.now().Before(deadline) {
		return o, apperr.Rule("abandonment_term_not_reached", fmt.Sprintf(
			"O prazo de retirada desta filial só termina em %s. Até lá, a OS não pode ser marcada como abandonada.",
			deadline.In(loc).Format("02/01/2006")))
	}
	if err := s.move(ctx, &o, StatusAbandoned, strings.TrimSpace(reason), map[string]any{"abandoned_at": s.now()}); err != nil {
		return o, err
	}
	return s.findOrder(ctx, o.ID)
}

// WarrantyReturnInput opens a warranty return (UC-07).
type WarrantyReturnInput struct {
	ID                 uuid.UUID
	ReservedSequence   *int64
	ReportedDefect     string
	EntryChecklist     map[string]any
	Accessories        *string
	DevicePassword     *string
	ExpectedDeliveryAt *time.Time
	TechnicianID       *uuid.UUID
	// CoveredItemIDs selects the warranty items to cover; nil covers every active one.
	CoveredItemIDs []uuid.UUID
}

// OpenWarrantyReturn opens a new order linked to a delivered one, with a draft quote
// holding the covered items at no charge (RN-19).
func (s *Service) OpenWarrantyReturn(ctx context.Context, originID uuid.UUID, in WarrantyReturnInput) (Opened, error) {
	if in.ID != uuid.Nil {
		var n int64
		if err := database.Conn(ctx).Model(&Order{}).Where("id = ?", in.ID).Count(&n).Error; err != nil {
			return Opened{}, err
		}
		if n > 0 {
			return s.Open(ctx, OpenInput{ID: in.ID}) // replay: returns the existing order
		}
	}
	origin, err := s.findOrder(ctx, originID)
	if err != nil {
		return Opened{}, err
	}
	if origin.Status != StatusDelivered {
		return Opened{}, errNotDelivered
	}
	active, err := s.activeWarranties(ctx, origin.EquipmentID, &origin.ID)
	if err != nil {
		return Opened{}, err
	}
	if len(active) == 0 {
		return Opened{}, errNoWarranty
	}
	coveredIDs := make([]uuid.UUID, 0, len(active))
	if in.CoveredItemIDs == nil {
		for _, w := range active {
			coveredIDs = append(coveredIDs, w.ItemID)
		}
	} else {
		isActive := map[uuid.UUID]bool{}
		for _, w := range active {
			isActive[w.ItemID] = true
		}
		for _, id := range in.CoveredItemIDs {
			if !isActive[id] {
				return Opened{}, errNotCovered
			}
			coveredIDs = append(coveredIDs, id)
		}
	}

	opened, err := s.Open(ctx, OpenInput{
		ID: in.ID, ReservedSequence: in.ReservedSequence,
		CustomerID: origin.CustomerID, EquipmentID: origin.EquipmentID,
		ReportedDefect: in.ReportedDefect, EntryChecklist: in.EntryChecklist,
		Accessories: in.Accessories, DevicePassword: in.DevicePassword,
		ExpectedDeliveryAt: in.ExpectedDeliveryAt, TechnicianID: in.TechnicianID,
		RequesterContactID: origin.RequesterContactID, CostCenterID: origin.CostCenterID,
		originOrderID: &origin.ID,
	})
	if err != nil {
		return opened, err
	}

	var originals []QuoteItem
	if err := database.Conn(ctx).Where("id IN ?", coveredIDs).Order("position").Find(&originals).Error; err != nil {
		return opened, err
	}
	items := make([]QuoteItem, len(originals))
	for i, it := range originals {
		id := it.ID
		items[i] = QuoteItem{
			Position: i + 1, Kind: it.Kind, ServiceID: it.ServiceID, ProductID: it.ProductID,
			Description: it.Description, Quantity: it.Quantity, ListPriceCents: it.UnitPriceCents,
			CoveredItemID: &id,
		}
	}
	if _, err := s.insertQuote(ctx, opened.Order.ID, uuid.Nil, nil, items, nil); err != nil {
		return opened, err
	}
	return opened, nil
}
