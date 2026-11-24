package identity

import (
	"net/http"

	"github.com/luiscadari/bancada-api/internal/platform/apperr"
)

var (
	errInvalidCredentials = apperr.Unauthorized("invalid_credentials", "E-mail ou senha incorretos.")
	errTooManyAttempts    = apperr.New(http.StatusTooManyRequests, "too_many_attempts",
		"Muitas tentativas de entrar. Espere alguns minutos e tente de novo.")
	errUserDisabled = apperr.Forbidden("user_disabled",
		"Seu acesso foi desativado. Fale com o dono da conta.")
	errInvalidRefresh = apperr.Unauthorized("invalid_refresh_token", "Sua sessão expirou. Entre novamente.")
	errDeviceRevoked  = apperr.Forbidden("device_revoked",
		"Este computador foi desconectado da conta. Peça ao dono para liberá-lo de novo.")
	errEmailTaken = apperr.Conflict("email_taken",
		"Este e-mail já está em uso. Entre com ele ou use outro e-mail.")
	errPrefixTaken = apperr.Conflict("os_prefix_taken",
		"Outra filial já usa este prefixo de OS. Escolha outro.")
	errInvalidTimezone = apperr.Validation("Fuso horário inválido.",
		apperr.Field("timezone", "use um nome IANA, como America/Sao_Paulo"))
	errInvitationInvalid = apperr.NotFound("invitation_not_found",
		"Convite não encontrado. Peça um novo convite ao dono da conta.")
	errInvitationExpired = apperr.Rule("invitation_expired",
		"Este convite expirou. Peça um novo convite ao dono da conta.")
	errBranchNotFound = apperr.NotFound("branch_not_found", "Filial não encontrada.")
	errUserNotFound   = apperr.NotFound("user_not_found", "Usuário não encontrado.")
	errDeviceNotFound = apperr.NotFound("device_not_found", "Computador não encontrado.")
	errLastOwner      = apperr.Rule("last_owner",
		"A conta precisa de pelo menos um dono ativo. Defina outro dono antes.")
	errSelfDisable  = apperr.Rule("cannot_disable_self", "Você não pode desativar o seu próprio acesso.")
	errBranchAccess = apperr.Forbidden("branch_forbidden", "Você não tem acesso a esta filial.")
)
