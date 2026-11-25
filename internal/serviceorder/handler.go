package serviceorder

import (
	"context"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/luiscadari/bancada-api/internal/api/gen"
	"github.com/luiscadari/bancada-api/internal/platform/paging"
)

// Handler implements the service order part of gen.StrictServerInterface.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// ---- orders -----------------------------------------------------------------------

func (h *Handler) ListServiceOrders(ctx context.Context, req gen.ListServiceOrdersRequestObject) (gen.ListServiceOrdersResponseObject, error) {
	p := req.Params
	page, err := paging.Parse(p.Limit, p.Cursor)
	if err != nil {
		return nil, err
	}
	f := OrderFilter{Query: str(p.Q), TechnicianID: p.TechnicianId, CustomerID: p.CustomerId,
		EquipmentID: p.EquipmentId, Unassigned: p.Unassigned != nil && *p.Unassigned}
	if p.Status != nil {
		for _, st := range *p.Status {
			f.Statuses = append(f.Statuses, string(st))
		}
	}
	orders, next, err := h.svc.ListOrders(ctx, f, page)
	if err != nil {
		return nil, err
	}
	return gen.ListServiceOrders200JSONResponse(orderList(orders, next)), nil
}

func (h *Handler) OpenServiceOrder(ctx context.Context, req gen.OpenServiceOrderRequestObject) (gen.OpenServiceOrderResponseObject, error) {
	b := req.Body
	opened, err := h.svc.Open(ctx, OpenInput{
		ID: idOrNil(b.Id), ReservedSequence: b.ReservedSequence, CustomerID: b.CustomerId,
		EquipmentID: b.EquipmentId, ReportedDefect: b.ReportedDefect, EntryChecklist: b.EntryChecklist,
		Accessories: b.Accessories, DevicePassword: b.DevicePassword, PreAuthorizedCents: b.PreAuthorizedCents,
		ExpectedDeliveryAt: b.ExpectedDeliveryAt, RequesterContactID: b.RequesterContactId,
		CostCenterID: b.CostCenterId, TechnicianID: b.TechnicianId, Priority: (*string)(b.Priority),
	})
	if err != nil {
		return nil, err
	}
	return gen.OpenServiceOrder201JSONResponse(toGenOpened(opened)), nil
}

func (h *Handler) GetServiceOrder(ctx context.Context, req gen.GetServiceOrderRequestObject) (gen.GetServiceOrderResponseObject, error) {
	o, err := h.svc.GetOrder(ctx, req.OrderId)
	if err != nil {
		return nil, err
	}
	return gen.GetServiceOrder200JSONResponse(toGenOrder(o)), nil
}

func (h *Handler) UpdateServiceOrder(ctx context.Context, req gen.UpdateServiceOrderRequestObject) (gen.UpdateServiceOrderResponseObject, error) {
	b := req.Body
	o, err := h.svc.Update(ctx, req.OrderId, OrderUpdate{
		Diagnosis: b.Diagnosis, TechnicalReport: b.TechnicalReport, Accessories: b.Accessories,
		DevicePassword: b.DevicePassword, ExpectedDeliveryAt: b.ExpectedDeliveryAt,
		PreAuthorizedCents: b.PreAuthorizedCents, RequesterContactID: b.RequesterContactId,
		CostCenterID: b.CostCenterId,
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateServiceOrder200JSONResponse(toGenOrder(o)), nil
}

func (h *Handler) AssignServiceOrder(ctx context.Context, req gen.AssignServiceOrderRequestObject) (gen.AssignServiceOrderResponseObject, error) {
	b := req.Body
	o, err := h.svc.Assign(ctx, req.OrderId, AssignInput{
		TechnicianID: b.TechnicianId, Unassign: b.Unassign != nil && *b.Unassign, Priority: (*string)(b.Priority),
	})
	if err != nil {
		return nil, err
	}
	return gen.AssignServiceOrder200JSONResponse(toGenOrder(o)), nil
}

func (h *Handler) TransitionServiceOrder(ctx context.Context, req gen.TransitionServiceOrderRequestObject) (gen.TransitionServiceOrderResponseObject, error) {
	o, err := h.svc.Transition(ctx, req.OrderId, string(req.Body.To), str(req.Body.Reason))
	if err != nil {
		return nil, err
	}
	return gen.TransitionServiceOrder200JSONResponse(toGenOrder(o)), nil
}

func (h *Handler) ListOrderStatusHistory(ctx context.Context, req gen.ListOrderStatusHistoryRequestObject) (gen.ListOrderStatusHistoryResponseObject, error) {
	changes, err := h.svc.History(ctx, req.OrderId)
	if err != nil {
		return nil, err
	}
	out := make([]gen.StatusChange, len(changes))
	for i, c := range changes {
		out[i] = gen.StatusChange{
			Id: c.ID, FromStatus: (*gen.OrderStatus)(c.FromStatus), ToStatus: gen.OrderStatus(c.ToStatus),
			Reason: c.Reason, UserId: c.UserID, DeviceId: c.DeviceID, CreatedAt: c.CreatedAt,
		}
	}
	return gen.ListOrderStatusHistory200JSONResponse{Items: out}, nil
}

func (h *Handler) RevealDevicePassword(ctx context.Context, req gen.RevealDevicePasswordRequestObject) (gen.RevealDevicePasswordResponseObject, error) {
	pw, err := h.svc.RevealPassword(ctx, req.OrderId)
	if err != nil {
		return nil, err
	}
	return gen.RevealDevicePassword200JSONResponse{Password: pw}, nil
}

func (h *Handler) DeliverServiceOrder(ctx context.Context, req gen.DeliverServiceOrderRequestObject) (gen.DeliverServiceOrderResponseObject, error) {
	b := req.Body
	o, err := h.svc.Deliver(ctx, req.OrderId, DeliveryInput{
		PickedUpByName: b.PickedUpByName, PickedUpByDocument: b.PickedUpByDocument,
		AuthorizeOpenBalance: b.AuthorizeOpenBalance != nil && *b.AuthorizeOpenBalance,
	})
	if err != nil {
		return nil, err
	}
	return gen.DeliverServiceOrder200JSONResponse(toGenOrder(o)), nil
}

func (h *Handler) CancelServiceOrder(ctx context.Context, req gen.CancelServiceOrderRequestObject) (gen.CancelServiceOrderResponseObject, error) {
	o, err := h.svc.Cancel(ctx, req.OrderId, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return gen.CancelServiceOrder200JSONResponse(toGenOrder(o)), nil
}

func (h *Handler) AbandonServiceOrder(ctx context.Context, req gen.AbandonServiceOrderRequestObject) (gen.AbandonServiceOrderResponseObject, error) {
	var reason string
	if req.Body != nil {
		reason = str(req.Body.Reason)
	}
	o, err := h.svc.Abandon(ctx, req.OrderId, reason)
	if err != nil {
		return nil, err
	}
	return gen.AbandonServiceOrder200JSONResponse(toGenOrder(o)), nil
}

func (h *Handler) OpenWarrantyReturn(ctx context.Context, req gen.OpenWarrantyReturnRequestObject) (gen.OpenWarrantyReturnResponseObject, error) {
	b := req.Body
	in := WarrantyReturnInput{
		ID: idOrNil(b.Id), ReservedSequence: b.ReservedSequence, ReportedDefect: b.ReportedDefect,
		EntryChecklist: b.EntryChecklist, Accessories: b.Accessories, DevicePassword: b.DevicePassword,
		ExpectedDeliveryAt: b.ExpectedDeliveryAt, TechnicianID: b.TechnicianId,
	}
	if b.CoveredItemIds != nil {
		in.CoveredItemIDs = append([]uuid.UUID{}, *b.CoveredItemIds...)
	}
	opened, err := h.svc.OpenWarrantyReturn(ctx, req.OrderId, in)
	if err != nil {
		return nil, err
	}
	return gen.OpenWarrantyReturn201JSONResponse(toGenOpened(opened)), nil
}

// ---- histories --------------------------------------------------------------------

func (h *Handler) GetCustomerHistory(ctx context.Context, req gen.GetCustomerHistoryRequestObject) (gen.GetCustomerHistoryResponseObject, error) {
	page, err := paging.Parse(req.Params.Limit, req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	orders, next, err := h.svc.CustomerHistory(ctx, req.CustomerId, page)
	if err != nil {
		return nil, err
	}
	return gen.GetCustomerHistory200JSONResponse(orderList(orders, next)), nil
}

func (h *Handler) GetEquipmentHistory(ctx context.Context, req gen.GetEquipmentHistoryRequestObject) (gen.GetEquipmentHistoryResponseObject, error) {
	page, err := paging.Parse(req.Params.Limit, req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	orders, next, warranties, err := h.svc.EquipmentHistory(ctx, req.EquipmentId, page)
	if err != nil {
		return nil, err
	}
	list := orderList(orders, next)
	return gen.GetEquipmentHistory200JSONResponse{
		Items: list.Items, NextCursor: next, ActiveWarranties: toGenWarranties(warranties),
	}, nil
}

// ---- quotes -----------------------------------------------------------------------

func (h *Handler) ListQuotes(ctx context.Context, req gen.ListQuotesRequestObject) (gen.ListQuotesResponseObject, error) {
	quotes, err := h.svc.ListQuotes(ctx, req.OrderId)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Quote, len(quotes))
	for i, q := range quotes {
		out[i] = toGenQuote(q)
	}
	return gen.ListQuotes200JSONResponse{Items: out}, nil
}

func (h *Handler) CreateQuote(ctx context.Context, req gen.CreateQuoteRequestObject) (gen.CreateQuoteResponseObject, error) {
	b := req.Body
	in := QuoteInput{ID: idOrNil(b.Id), Notes: b.Notes}
	if b.Items != nil {
		in.Items = fromGenItems(*b.Items)
	}
	q, err := h.svc.CreateQuote(ctx, req.OrderId, in)
	if err != nil {
		return nil, err
	}
	return gen.CreateQuote201JSONResponse(toGenQuote(q)), nil
}

func (h *Handler) UpdateQuote(ctx context.Context, req gen.UpdateQuoteRequestObject) (gen.UpdateQuoteResponseObject, error) {
	q, err := h.svc.UpdateQuote(ctx, req.OrderId, req.QuoteId, req.Body.Notes, fromGenItems(req.Body.Items))
	if err != nil {
		return nil, err
	}
	return gen.UpdateQuote200JSONResponse(toGenQuote(q)), nil
}

func (h *Handler) SendQuote(ctx context.Context, req gen.SendQuoteRequestObject) (gen.SendQuoteResponseObject, error) {
	o, q, err := h.svc.SendQuote(ctx, req.OrderId, req.QuoteId)
	if err != nil {
		return nil, err
	}
	return gen.SendQuote200JSONResponse{Order: toGenOrder(o), Quote: toGenQuote(q)}, nil
}

func (h *Handler) DecideQuote(ctx context.Context, req gen.DecideQuoteRequestObject) (gen.DecideQuoteResponseObject, error) {
	b := req.Body
	in := DecisionInput{
		Approve: b.Decision == gen.QuoteDecisionDecisionApproved, Channel: string(b.Channel), ApproverName: b.ApproverName,
		ApproverContactID: b.ApproverContactId, RejectionReason: b.RejectionReason,
	}
	if b.AcceptedItemIds != nil {
		in.AcceptedItemIDs = append([]uuid.UUID{}, *b.AcceptedItemIds...)
	}
	o, q, err := h.svc.DecideQuote(ctx, req.OrderId, req.QuoteId, in)
	if err != nil {
		return nil, err
	}
	return gen.DecideQuote200JSONResponse{Order: toGenOrder(o), Quote: toGenQuote(q)}, nil
}

// ---- mapping ----------------------------------------------------------------------

func orderList(orders []Order, next *string) gen.ServiceOrderList {
	out := make([]gen.ServiceOrder, len(orders))
	for i, o := range orders {
		out[i] = toGenOrder(o)
	}
	return gen.ServiceOrderList{Items: out, NextCursor: next}
}

func toGenOrder(o Order) gen.ServiceOrder {
	checklist := o.EntryChecklist.Data()
	if checklist == nil {
		checklist = map[string]any{}
	}
	return gen.ServiceOrder{
		Id: o.ID, BranchId: o.BranchID, Number: o.Number, Sequence: o.Sequence,
		CustomerId: o.CustomerID, CustomerName: o.CustomerName, EquipmentId: o.EquipmentID,
		TechnicianId: o.TechnicianID, Priority: gen.Priority(o.Priority), OriginOrderId: o.OriginOrderID,
		RequesterContactId: o.RequesterContactID, ApproverContactId: o.ApproverContactID,
		CostCenterId: o.CostCenterID, Status: gen.OrderStatus(o.Status), ReportedDefect: o.ReportedDefect,
		Diagnosis: o.Diagnosis, TechnicalReport: o.TechnicalReport, EntryChecklist: checklist,
		Accessories: o.Accessories, HasDevicePassword: o.HasDevicePassword(),
		PreAuthorizedCents: o.PreAuthorizedCents, DiagnosticFeeCents: o.DiagnosticFeeCents,
		ApprovedTotalCents: o.ApprovedTotalCents, AmountDueCents: o.AmountDueCents(),
		OpenBalanceCents: o.OpenBalanceCents, BalanceAuthorizedBy: o.BalanceAuthorizedBy,
		ExpectedDeliveryAt: o.ExpectedDeliveryAt, ReadyAt: o.ReadyAt, DeliveredAt: o.DeliveredAt,
		CanceledAt: o.CanceledAt, AbandonedAt: o.AbandonedAt, CancelReason: o.CancelReason,
		PickedUpByName: o.PickedUpByName, PickedUpByDocument: o.PickedUpByDocument,
		CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt, Version: o.Version,
	}
}

func toGenOpened(o Opened) gen.ServiceOrderOpened {
	return gen.ServiceOrderOpened{Order: toGenOrder(o.Order), ActiveWarranties: toGenWarranties(o.Warranties)}
}

func toGenWarranties(ws []Warranty) []gen.WarrantyItem {
	out := make([]gen.WarrantyItem, len(ws))
	for i, w := range ws {
		out[i] = gen.WarrantyItem{
			ItemId: w.ItemID, OrderId: w.OrderID, OrderNumber: w.OrderNumber, Kind: gen.QuoteItemKind(w.Kind),
			Description: w.Description, WarrantyUntil: openapi_types.Date{Time: w.WarrantyUntil},
			DeliveredAt: w.DeliveredAt,
		}
	}
	return out
}

func toGenQuote(q Quote) gen.Quote {
	items := make([]gen.QuoteItem, len(q.Items))
	for i, it := range q.Items {
		qty, _ := it.Quantity.Float64()
		var until *openapi_types.Date
		if it.WarrantyUntil != nil {
			until = &openapi_types.Date{Time: *it.WarrantyUntil}
		}
		items[i] = gen.QuoteItem{
			Id: it.ID, Position: it.Position, Kind: gen.QuoteItemKind(it.Kind), ServiceId: it.ServiceID,
			ProductId: it.ProductID, Description: it.Description, Quantity: qty,
			ListPriceCents: it.ListPriceCents, UnitPriceCents: it.UnitPriceCents, DiscountCents: it.DiscountCents,
			TotalCents: it.TotalCents, Optional: it.Optional, WarrantyDays: it.WarrantyDays,
			WarrantyUntil: until, CoveredItemId: it.CoveredItemID,
		}
	}
	return gen.Quote{
		Id: q.ID, OrderId: q.OrderID, Number: q.Number, Status: gen.QuoteStatus(q.Status), Notes: q.Notes,
		SubtotalCents: q.SubtotalCents, DiscountCents: q.DiscountCents, TotalCents: q.TotalCents,
		SentAt: q.SentAt, ValidUntil: q.ValidUntil, DecisionChannel: (*gen.DecisionChannel)(q.DecisionChannel),
		DecidedByName: q.DecidedByName, DecidedByContactId: q.DecidedByContactID,
		DecidedByUserId: q.DecidedByUserID, DecidedAt: q.DecidedAt, RejectionReason: q.RejectionReason,
		PreviousQuoteId: q.PreviousQuoteID, Items: items,
		CreatedAt: q.CreatedAt, UpdatedAt: q.UpdatedAt, Version: q.Version,
	}
}

func fromGenItems(in []gen.QuoteItemInput) []ItemInput {
	out := make([]ItemInput, len(in))
	for i, it := range in {
		out[i] = ItemInput{
			ID: idOrNil(it.Id), Kind: string(it.Kind), ServiceID: it.ServiceId, ProductID: it.ProductId,
			Description: it.Description, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents,
			DiscountCents: it.DiscountCents, Optional: it.Optional != nil && *it.Optional,
			WarrantyDays: it.WarrantyDays, CoveredItemID: it.CoveredItemId,
		}
	}
	return out
}

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
