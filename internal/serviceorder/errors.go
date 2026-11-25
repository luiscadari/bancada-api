package serviceorder

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/luiscadari/bancada-api/internal/platform/apperr"
)

var (
	errOrderNotFound = apperr.NotFound("order_not_found", "OS não encontrada nesta filial.")
	errQuoteNotFound = apperr.NotFound("quote_not_found", "Orçamento não encontrado nesta OS.")
	errNumberTaken   = apperr.Conflict("order_number_taken",
		"Este número de OS já foi usado. Abra a OS sem número para usar o próximo.")
	errNotAssigned = apperr.Forbidden("order_not_assigned",
		"Esta OS não está atribuída a você. Peça ao atendente para atribuí-la.")
	errTechnicianCannotOpen = apperr.Forbidden("technician_cannot_open_or_deliver",
		"Nesta filial, técnicos não abrem nem entregam OS. Peça ao atendente.")
	errOrderClosed = apperr.Conflict("order_closed",
		"Esta OS já foi encerrada e não pode mais ser alterada.")
	errNumberNeedsDevice = apperr.Rule("number_requires_device",
		"Números reservados só valem para o computador que os reservou.")
	errEquipmentOfOther = apperr.Rule("equipment_not_of_customer",
		"Este equipamento é de outro cliente. Escolha um equipamento deste cliente.")
	errRequesterRequired = apperr.Rule("requester_required",
		"Para cliente pessoa jurídica, informe o contato que trouxe o equipamento.")
	errNotRequester = apperr.Rule("contact_not_requester",
		"Este contato não está autorizado a deixar equipamentos. Marque-o como solicitante no cadastro.")
	errTechnicianNotInBranch = apperr.Rule("technician_not_in_branch",
		"Este usuário não trabalha nesta filial ou está desativado.")
	errNoPassword = apperr.NotFound("no_device_password", "Esta OS não tem senha do equipamento.")

	errDraftExists = apperr.Conflict("quote_draft_exists",
		"Já existe um orçamento em rascunho nesta OS. Edite o rascunho existente.")
	errQuoteNotDraft = apperr.Conflict("quote_not_draft",
		"Só orçamentos em rascunho podem ser alterados. Crie uma nova versão.")
	errQuoteNotPending = apperr.Conflict("quote_not_pending",
		"Este orçamento não está aguardando decisão.")
	errQuoteEmpty   = apperr.Rule("quote_empty", "Adicione ao menos um serviço ou peça antes de enviar.")
	errQuoteExpired = apperr.Rule("quote_expired",
		"Este orçamento venceu. Envie uma nova versão ao cliente.")
	errRequiredItemDeclined = apperr.Rule("required_item_declined",
		"Só itens opcionais podem ser recusados. Para tirar outro item, reprove e envie nova versão.")
	errUnknownItem      = apperr.Rule("item_not_in_quote", "Um dos itens aceitos não pertence a este orçamento.")
	errApproverRequired = apperr.Rule("approver_required",
		"Informe quem autorizou: o nome ou o contato aprovador.")
	errNotApprover = apperr.Rule("contact_not_approver",
		"Este contato não pode aprovar orçamentos. Escolha um contato aprovador do cliente.")
	errApprovalLimit = apperr.Rule("approval_limit_exceeded",
		"O valor passa do limite de aprovação deste contato. Peça a aprovação de outro aprovador.")
	errDiscountForbidden = apperr.Forbidden("discount_not_allowed",
		"Seu perfil não pode conceder desconto. Peça ao atendente ou ao dono.")
	errDiscountExceeds = apperr.Rule("discount_exceeds_price", "O desconto não pode ser maior que o valor do item.")
	errItemReference   = apperr.Rule("item_reference_required",
		"Informe o serviço (service_id) para itens de serviço ou o produto (product_id) para peças.")
	errNotCovered = apperr.Rule("item_not_under_warranty",
		"Este item não está em garantia na OS de origem.")

	errOpenBalanceOwner = apperr.Forbidden("open_balance_requires_owner",
		"Só o dono autoriza entregar com saldo em aberto.")
	errNotDelivered = apperr.Rule("order_not_delivered",
		"O retorno em garantia só pode ser aberto a partir de uma OS entregue.")
	errNoWarranty = apperr.Rule("no_active_warranty",
		"Nenhum item desta OS está mais na garantia. Abra uma OS normal.")
)

func isUnique(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
