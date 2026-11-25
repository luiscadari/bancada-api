package serviceorder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/luiscadari/bancada-api/internal/identity"
	"github.com/luiscadari/bancada-api/internal/inventory"
	"github.com/luiscadari/bancada-api/internal/platform/actor"
	"github.com/luiscadari/bancada-api/internal/platform/apperr"
	"github.com/luiscadari/bancada-api/internal/platform/database"
	"github.com/luiscadari/bancada-api/internal/registry"
)

// ItemInput is a line of a quote draft.
type ItemInput struct {
	ID             uuid.UUID
	Kind           string
	ServiceID      *uuid.UUID
	ProductID      *uuid.UUID
	Description    *string
	Quantity       *float64
	UnitPriceCents *int64
	DiscountCents  *int64
	Optional       bool
	WarrantyDays   *int
	CoveredItemID  *uuid.UUID
}

// QuoteInput is the content of a quote draft.
type QuoteInput struct {
	ID    uuid.UUID
	Notes *string
	Items []ItemInput
}

// ---- queries ----------------------------------------------------------------------

func loadItems(ctx context.Context, quotes []Quote) error {
	if len(quotes) == 0 {
		return nil
	}
	idx := make(map[uuid.UUID]int, len(quotes))
	quoteIDs := make([]uuid.UUID, len(quotes))
	for i, q := range quotes {
		idx[q.ID] = i
		quoteIDs[i] = q.ID
		quotes[i].Items = []QuoteItem{}
	}
	var items []QuoteItem
	if err := database.Conn(ctx).Where("quote_id IN ?", quoteIDs).Order("position").Find(&items).Error; err != nil {
		return err
	}
	for _, it := range items {
		q := &quotes[idx[it.QuoteID]]
		q.Items = append(q.Items, it)
	}
	return nil
}

func (s *Service) loadQuote(ctx context.Context, orderID, quoteID uuid.UUID) (Quote, error) {
	var q Quote
	err := database.Conn(ctx).Where("id = ? AND order_id = ?", quoteID, orderID).Take(&q).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return q, errQuoteNotFound
	}
	if err != nil {
		return q, err
	}
	quotes := []Quote{q}
	err = loadItems(ctx, quotes)
	return quotes[0], err
}

// ListQuotes returns every version of the order's quote, oldest first (RN-11).
func (s *Service) ListQuotes(ctx context.Context, orderID uuid.UUID) ([]Quote, error) {
	if _, err := s.findOrder(ctx, orderID); err != nil {
		return nil, err
	}
	var quotes []Quote
	if err := database.Conn(ctx).Where("order_id = ?", orderID).Order("number").Find(&quotes).Error; err != nil {
		return nil, err
	}
	return quotes, loadItems(ctx, quotes)
}

func (s *Service) nextQuoteNumber(ctx context.Context, orderID uuid.UUID) (int, error) {
	var n int
	err := database.Conn(ctx).Model(&Quote{}).Unscoped().Where("order_id = ?", orderID).
		Select("COALESCE(MAX(number), 0) + 1").Scan(&n).Error
	return n, err
}

// ---- drafts -----------------------------------------------------------------------

// CreateQuote starts the order's quote draft (RF-12). A repeated create with the same
// id returns the existing quote.
func (s *Service) CreateQuote(ctx context.Context, orderID uuid.UUID, in QuoteInput) (Quote, error) {
	a := mustActor(ctx)
	o, err := s.lockOrder(ctx, orderID)
	if err != nil {
		return Quote{}, err
	}
	if in.ID != uuid.Nil {
		if q, err := s.loadQuote(ctx, o.ID, in.ID); err == nil {
			return q, nil
		}
	}
	if err := ensureCanWork(a, &o); err != nil {
		return Quote{}, err
	}
	if !slices.Contains(quoteStatuses, o.Status) {
		return Quote{}, apperr.Conflict("quote_not_allowed",
			"Inicie o diagnóstico antes de montar o orçamento. A OS está em \""+statusLabel(o.Status)+"\".")
	}
	var drafts int64
	if err := database.Conn(ctx).Model(&Quote{}).Where("order_id = ? AND status = ?", o.ID, QuoteDraft).Count(&drafts).Error; err != nil {
		return Quote{}, err
	}
	if drafts > 0 {
		return Quote{}, errDraftExists
	}
	items, err := s.buildItems(ctx, o, in.Items)
	if err != nil {
		return Quote{}, err
	}
	return s.insertQuote(ctx, o.ID, in.ID, trimmed(in.Notes), items, nil)
}

func (s *Service) insertQuote(ctx context.Context, orderID, id uuid.UUID, notes *string, items []QuoteItem, previous *uuid.UUID) (Quote, error) {
	number, err := s.nextQuoteNumber(ctx, orderID)
	if err != nil {
		return Quote{}, err
	}
	q := Quote{OrderID: orderID, Number: number, Status: QuoteDraft, Notes: notes, PreviousQuoteID: previous}
	q.ID = id
	q.SubtotalCents, q.DiscountCents, q.TotalCents = totals(items)
	db := database.Conn(ctx)
	if err := db.Create(&q).Error; err != nil {
		if isUnique(err, "quotes_one_draft_key") {
			return Quote{}, errDraftExists
		}
		return Quote{}, err
	}
	if err := s.insertItems(ctx, q.ID, items); err != nil {
		return Quote{}, err
	}
	return s.loadQuote(ctx, orderID, q.ID)
}

func (s *Service) insertItems(ctx context.Context, quoteID uuid.UUID, items []QuoteItem) error {
	if len(items) == 0 {
		return nil
	}
	for i := range items {
		items[i].QuoteID = quoteID
	}
	return database.Conn(ctx).Create(&items).Error
}

// UpdateQuote replaces the notes and the items of a draft.
func (s *Service) UpdateQuote(ctx context.Context, orderID, quoteID uuid.UUID, notes *string, in []ItemInput) (Quote, error) {
	a := mustActor(ctx)
	o, err := s.lockOrder(ctx, orderID)
	if err != nil {
		return Quote{}, err
	}
	if err := ensureCanWork(a, &o); err != nil {
		return Quote{}, err
	}
	q, err := s.loadQuote(ctx, o.ID, quoteID)
	if err != nil {
		return q, err
	}
	if q.Status != QuoteDraft {
		return q, errQuoteNotDraft
	}
	items, err := s.buildItems(ctx, o, in)
	if err != nil {
		return q, err
	}
	db := database.Conn(ctx)
	// Draft lines are working copies: they are replaced, not versioned.
	if err := db.Unscoped().Where("quote_id = ?", q.ID).Delete(&QuoteItem{}).Error; err != nil {
		return q, err
	}
	if err := s.insertItems(ctx, q.ID, items); err != nil {
		return q, err
	}
	sub, disc, total := totals(items)
	if err := db.Model(&Quote{Base: database.Base{ID: q.ID}}).Updates(map[string]any{
		"notes": trimmed(notes), "subtotal_cents": sub, "discount_cents": disc, "total_cents": total,
	}).Error; err != nil {
		return q, err
	}
	return s.loadQuote(ctx, o.ID, q.ID)
}

// buildItems prices the lines of a draft from the catalog and enforces the discount
// limits of RN-12. Items covered by the warranty of the original order cost nothing
// (RN-19).
func (s *Service) buildItems(ctx context.Context, o Order, in []ItemInput) ([]QuoteItem, error) {
	a := mustActor(ctx)
	settings, _, err := s.identity.Settings(ctx, o.BranchID)
	if err != nil {
		return nil, err
	}
	covered := map[uuid.UUID]bool{}
	if o.OriginOrderID != nil {
		active, err := s.activeWarranties(ctx, o.EquipmentID, o.OriginOrderID)
		if err != nil {
			return nil, err
		}
		for _, w := range active {
			covered[w.ItemID] = true
		}
	}
	items := make([]QuoteItem, 0, len(in))
	for i, line := range in {
		item, err := s.priceItem(ctx, line)
		if err != nil {
			return nil, prefixFields(err, i)
		}
		item.Position = i + 1
		if line.CoveredItemID != nil {
			if !covered[*line.CoveredItemID] {
				return nil, errNotCovered
			}
			item.CoveredItemID = line.CoveredItemID
			item.UnitPriceCents, item.DiscountCents, item.TotalCents, item.WarrantyDays = 0, 0, 0, 0
		} else if err := checkDiscount(a, settings, item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) priceItem(ctx context.Context, in ItemInput) (QuoteItem, error) {
	item := QuoteItem{Kind: in.Kind, Optional: in.Optional}
	item.ID = in.ID
	var name string
	var listPrice int64
	var warranty int
	switch in.Kind {
	case KindService:
		if in.ServiceID == nil || in.ProductID != nil {
			return item, errItemReference
		}
		svc, err := s.registry.GetService(ctx, *in.ServiceID)
		if err != nil {
			return item, err
		}
		if !svc.Active {
			return item, inactiveItem(svc.Name)
		}
		item.ServiceID, name, listPrice, warranty = &svc.ID, svc.Name, svc.PriceCents, svc.WarrantyDays
	case KindPart:
		if in.ProductID == nil || in.ServiceID != nil {
			return item, errItemReference
		}
		p, err := s.registry.GetProduct(ctx, *in.ProductID)
		if err != nil {
			return item, err
		}
		if !p.Active {
			return item, inactiveItem(p.Name)
		}
		item.ProductID, name, listPrice, warranty = &p.ID, p.Name, p.PriceCents, p.WarrantyDays
	default:
		return item, errItemReference
	}
	item.Description = name
	if d := trimmed(in.Description); d != nil {
		item.Description = *d
	}
	item.Quantity = decimal.NewFromInt(1)
	if in.Quantity != nil {
		item.Quantity = decimal.NewFromFloat(*in.Quantity).Round(3)
	}
	if !item.Quantity.IsPositive() {
		return item, apperr.Validation("A quantidade deve ser maior que zero.", apperr.Field("quantity", "Maior que zero."))
	}
	item.ListPriceCents = listPrice
	item.UnitPriceCents = listPrice
	if in.UnitPriceCents != nil {
		item.UnitPriceCents = *in.UnitPriceCents
	}
	if in.DiscountCents != nil {
		item.DiscountCents = *in.DiscountCents
	}
	gross := cents(item.Quantity, item.UnitPriceCents)
	if item.DiscountCents > gross {
		return item, errDiscountExceeds
	}
	item.TotalCents = gross - item.DiscountCents
	item.WarrantyDays = warranty
	if in.WarrantyDays != nil {
		item.WarrantyDays = *in.WarrantyDays
	}
	return item, nil
}

func inactiveItem(name string) error {
	return apperr.Rule("catalog_item_inactive", "\""+name+"\" está desativado no catálogo.")
}

// prefixFields points field errors of an item at its position in the request.
func prefixFields(err error, i int) error {
	var e *apperr.Error
	if errors.As(err, &e) && len(e.Fields) > 0 {
		c := *e
		c.Fields = make([]apperr.FieldError, len(e.Fields))
		for j, f := range e.Fields {
			c.Fields[j] = apperr.Field(fmt.Sprintf("items[%d].%s", i, f.Field), f.Message)
		}
		return &c
	}
	return err
}

// checkDiscount enforces RN-12. The discount of a line is what it costs below the
// catalog price: a lower unit price plus the explicit discount. Owners have no limit,
// attendants up to the branch percentage and technicians none (section 3).
func checkDiscount(a *actor.Actor, settings identity.BranchSettings, item QuoteItem) error {
	listGross := cents(item.Quantity, item.ListPriceCents)
	given := listGross - item.TotalCents
	if given <= 0 || a.HasAnyRole(actor.RoleOwner) {
		return nil
	}
	if !a.HasAnyRole(actor.RoleAttendant) {
		return errDiscountForbidden
	}
	limit := int64(settings.AttendantMaxDiscountPercent)
	if given*100 > limit*listGross {
		return apperr.Forbidden("discount_requires_owner", fmt.Sprintf(
			"O desconto em \"%s\" passa de %d%%, o limite do seu perfil. Peça a liberação ao dono.",
			item.Description, limit))
	}
	return nil
}

// cents is quantity × unit price rounded to whole cents.
func cents(qty decimal.Decimal, unit int64) int64 {
	return qty.Mul(decimal.NewFromInt(unit)).Round(0).IntPart()
}

func totals(items []QuoteItem) (subtotal, discount, total int64) {
	for _, it := range items {
		subtotal += it.TotalCents + it.DiscountCents
		discount += it.DiscountCents
		total += it.TotalCents
	}
	return
}

// ---- sending and decision ---------------------------------------------------------

// SendQuote sends the draft to the customer (RN-09 to RN-11): it gets its validity, the
// earlier versions are superseded and the order waits for approval. A quote with nothing
// to charge, or within the amount pre-authorized at the counter, is approved at once.
func (s *Service) SendQuote(ctx context.Context, orderID, quoteID uuid.UUID) (Order, Quote, error) {
	a := mustActor(ctx)
	o, err := s.lockOrder(ctx, orderID)
	if err != nil {
		return o, Quote{}, err
	}
	if err := ensureCanWork(a, &o); err != nil {
		return o, Quote{}, err
	}
	q, err := s.loadQuote(ctx, o.ID, quoteID)
	if err != nil {
		return o, q, err
	}
	if q.Status != QuoteDraft {
		return o, q, errQuoteNotDraft
	}
	if len(q.Items) == 0 {
		return o, q, errQuoteEmpty
	}
	if !slices.Contains(quoteStatuses, o.Status) {
		return o, q, invalidMove(o.Status, StatusAwaitingApproval)
	}
	settings, loc, err := s.location(ctx, o.BranchID)
	if err != nil {
		return o, q, err
	}
	now := s.now()
	validUntil := endOfDay(now.In(loc).AddDate(0, 0, settings.QuoteValidityDays))

	db := database.Conn(ctx)
	var hadApproved int64
	if err := db.Model(&Quote{}).Where("order_id = ? AND status = ?", o.ID, QuoteApproved).Count(&hadApproved).Error; err != nil {
		return o, q, err
	}
	if err := db.Model(&Quote{}).Where("order_id = ? AND status IN ?", o.ID, []string{QuoteSent, QuoteApproved}).
		Update("status", QuoteSuperseded).Error; err != nil {
		return o, q, err
	}
	if hadApproved > 0 {
		if err := s.inventory.Release(ctx, o.BranchID, o.ID); err != nil {
			return o, q, err
		}
	}
	if err := db.Model(&Quote{Base: database.Base{ID: q.ID}}).Updates(map[string]any{
		"status": QuoteSent, "sent_at": now, "valid_until": validUntil,
	}).Error; err != nil {
		return o, q, err
	}
	q.Status = QuoteSent
	if o.Status != StatusAwaitingApproval {
		if err := s.move(ctx, &o, StatusAwaitingApproval, fmt.Sprintf("Orçamento v%d enviado", q.Number), nil); err != nil {
			return o, q, err
		}
	}

	switch {
	case q.TotalCents == 0:
		err = s.approve(ctx, &o, &q, settings, decision{Channel: ChannelNoCharge, Name: "Sem cobrança"})
	case o.PreAuthorizedCents != nil && q.TotalCents <= *o.PreAuthorizedCents:
		err = s.approve(ctx, &o, &q, settings, decision{Channel: ChannelPreAuthorized, Name: "Pré-autorização na entrada"})
	}
	if err != nil {
		return o, q, err
	}
	return s.orderAndQuote(ctx, o.ID, q.ID)
}

func (s *Service) orderAndQuote(ctx context.Context, orderID, quoteID uuid.UUID) (Order, Quote, error) {
	o, err := s.findOrder(ctx, orderID)
	if err != nil {
		return o, Quote{}, err
	}
	q, err := s.loadQuote(ctx, orderID, quoteID)
	return o, q, err
}

// endOfDay returns the last instant of t's day in t's location, in UTC.
func endOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 23, 59, 59, 0, t.Location()).UTC()
}

// DecisionInput records the customer's answer to a quote (UC-03).
type DecisionInput struct {
	Approve           bool
	Channel           string
	ApproverName      *string
	ApproverContactID *uuid.UUID
	RejectionReason   *string
	// AcceptedItemIDs lists the accepted items; nil accepts everything.
	AcceptedItemIDs []uuid.UUID
}

type decision struct {
	Channel   string
	Name      string
	ContactID *uuid.UUID
	UserID    *uuid.UUID
	IP        *string
}

// DecideQuote records the approval or rejection of a sent quote (UC-03, RN-10, RN-14,
// RN-34). Accepting only some optional items creates a new approved version (RN-11).
func (s *Service) DecideQuote(ctx context.Context, orderID, quoteID uuid.UUID, in DecisionInput) (Order, Quote, error) {
	a := mustActor(ctx)
	o, err := s.lockOrder(ctx, orderID)
	if err != nil {
		return o, Quote{}, err
	}
	q, err := s.loadQuote(ctx, o.ID, quoteID)
	if err != nil {
		return o, q, err
	}
	if q.Status != QuoteSent || o.Status != StatusAwaitingApproval {
		return o, q, errQuoteNotPending
	}
	if q.ValidUntil != nil && s.now().After(*q.ValidUntil) {
		return o, q, errQuoteExpired
	}
	customer, err := s.registry.Customer(ctx, o.CustomerID)
	if err != nil {
		return o, q, err
	}
	d := decision{Channel: in.Channel, UserID: &a.UserID}
	if a.IP != "" {
		d.IP = &a.IP
	}
	var contact *registry.Contact
	if in.ApproverContactID != nil {
		c, err := s.registry.Contact(ctx, customer.ID, *in.ApproverContactID)
		if err != nil {
			return o, q, err
		}
		contact, d.ContactID, d.Name = &c, &c.ID, c.Name
	} else if n := trimmed(in.ApproverName); n != nil && (customer.Type != registry.CustomerCompany || !in.Approve) {
		d.Name = *n
	}
	if d.Name == "" {
		return o, q, errApproverRequired
	}
	settings, _, err := s.location(ctx, o.BranchID)
	if err != nil {
		return o, q, err
	}

	if !in.Approve {
		if err := s.reject(ctx, &o, &q, settings, d, trimmed(in.RejectionReason)); err != nil {
			return o, q, err
		}
		return s.orderAndQuote(ctx, o.ID, q.ID)
	}

	accepted, partial, err := acceptedItems(q, in.AcceptedItemIDs)
	if err != nil {
		return o, q, err
	}
	_, _, total := totals(accepted)
	if contact != nil && customer.Type == registry.CustomerCompany {
		if !contact.IsApprover {
			return o, q, errNotApprover
		}
		if contact.ApprovalLimitCents != nil && total > *contact.ApprovalLimitCents {
			return o, q, errApprovalLimit
		}
	}
	if partial {
		if q, err = s.partialVersion(ctx, o.ID, q, accepted); err != nil {
			return o, q, err
		}
	}
	if err := s.approve(ctx, &o, &q, settings, d); err != nil {
		return o, q, err
	}
	return s.orderAndQuote(ctx, o.ID, q.ID)
}

// acceptedItems resolves the accepted items; only optional items may be left out.
func acceptedItems(q Quote, ids []uuid.UUID) ([]QuoteItem, bool, error) {
	if ids == nil {
		return q.Items, false, nil
	}
	want := map[uuid.UUID]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var accepted []QuoteItem
	for _, it := range q.Items {
		switch {
		case want[it.ID]:
			accepted = append(accepted, it)
			delete(want, it.ID)
		case !it.Optional:
			return nil, false, errRequiredItemDeclined
		}
	}
	if len(want) > 0 {
		return nil, false, errUnknownItem
	}
	if len(accepted) == 0 {
		return nil, false, apperr.Rule("nothing_accepted", "Nenhum item foi aceito. Registre a reprovação do orçamento.")
	}
	return accepted, len(accepted) < len(q.Items), nil
}

// partialVersion supersedes q with a new sent version holding only the accepted items.
func (s *Service) partialVersion(ctx context.Context, orderID uuid.UUID, q Quote, accepted []QuoteItem) (Quote, error) {
	db := database.Conn(ctx)
	if err := db.Model(&Quote{Base: database.Base{ID: q.ID}}).Update("status", QuoteSuperseded).Error; err != nil {
		return q, err
	}
	items := make([]QuoteItem, len(accepted))
	for i, it := range accepted {
		c := it
		c.Base = database.Base{}
		c.Position = i + 1
		c.WarrantyUntil = nil
		items[i] = c
	}
	nq, err := s.insertQuote(ctx, orderID, uuid.Nil, q.Notes, items, &q.ID)
	if err != nil {
		return nq, err
	}
	if err := db.Model(&Quote{Base: database.Base{ID: nq.ID}}).Updates(map[string]any{
		"status": QuoteSent, "sent_at": q.SentAt, "valid_until": q.ValidUntil,
	}).Error; err != nil {
		return nq, err
	}
	nq.Status = QuoteSent
	return nq, nil
}

func (d decision) columns(status string, now time.Time) map[string]any {
	return map[string]any{
		"status": status, "decision_channel": d.Channel, "decided_by_name": d.Name,
		"decided_by_contact_id": d.ContactID, "decided_by_user_id": d.UserID, "decided_at": now,
		"decision_ip": d.IP,
	}
}

// approve records an approval: the order is approved, the diagnostic fee is settled
// (RN-13) and the parts are reserved (RN-20).
func (s *Service) approve(ctx context.Context, o *Order, q *Quote, settings identity.BranchSettings, d decision) error {
	now := s.now()
	if err := database.Conn(ctx).Model(&Quote{Base: database.Base{ID: q.ID}}).
		Updates(d.columns(QuoteApproved, now)).Error; err != nil {
		return err
	}
	q.Status = QuoteApproved
	var fee int64
	if settings.DiagnosticFee.Enabled && !settings.DiagnosticFee.DeductOnApproval {
		fee = settings.DiagnosticFee.AmountCents
	}
	extra := map[string]any{"diagnostic_fee_cents": fee}
	if d.ContactID != nil {
		extra["approver_contact_id"] = *d.ContactID
	}
	reason := fmt.Sprintf("Orçamento v%d aprovado por %s", q.Number, d.Name)
	if err := s.move(ctx, o, StatusApproved, reason, extra); err != nil {
		return err
	}
	var parts []inventory.PartLine
	for _, it := range q.Items {
		if it.Kind == KindPart && it.ProductID != nil {
			parts = append(parts, inventory.PartLine{ProductID: *it.ProductID, Quantity: it.Quantity})
		}
	}
	return s.inventory.Reserve(ctx, o.BranchID, o.ID, parts)
}

// reject records a rejection: the equipment waits for pickup and the diagnostic fee
// becomes due when the branch charges it (RN-14).
func (s *Service) reject(ctx context.Context, o *Order, q *Quote, settings identity.BranchSettings, d decision, reason *string) error {
	now := s.now()
	cols := d.columns(QuoteRejected, now)
	cols["rejection_reason"] = reason
	if err := database.Conn(ctx).Model(&Quote{Base: database.Base{ID: q.ID}}).Updates(cols).Error; err != nil {
		return err
	}
	q.Status = QuoteRejected
	var fee int64
	if settings.DiagnosticFee.Enabled {
		fee = settings.DiagnosticFee.AmountCents
	}
	why := fmt.Sprintf("Orçamento v%d reprovado por %s", q.Number, d.Name)
	if reason != nil {
		why += ": " + *reason
	}
	if err := s.move(ctx, o, StatusAwaitingPickup, why, map[string]any{
		"diagnostic_fee_cents": fee, "ready_at": now,
	}); err != nil {
		return err
	}
	return s.inventory.Release(ctx, o.BranchID, o.ID)
}

// ---- formatting -------------------------------------------------------------------

// brl formats cents as Brazilian reais, e.g. R$ 1.234,56.
func brl(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	whole := fmt.Sprintf("%d", cents/100)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	return fmt.Sprintf("%sR$ %s,%02d", sign, b.String(), cents%100)
}
