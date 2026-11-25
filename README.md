# bancada-api

API REST do Bancada, SaaS multi-tenant de ordens de serviço para assistências técnicas.
Atende o app desktop (Electron, offline-first) e o front web.

**Stack:** Go 1.26, Gin, GORM (pgx), PostgreSQL 18 com Row-Level Security, migrações goose, contrato
OpenAPI 3.0.3 com oapi-codegen (strict server), testes com testcontainers-go, Docker.

## Como rodar

```sh
cp .env.example .env          # opcional: o compose já tem valores de desenvolvimento
make up                       # sobe o Postgres, roda as migrações e inicia a API em :8080
curl localhost:8080/readyz    # {"status":"ok"}
make smoke                    # fluxo completo de uma OS contra a API rodando (curl + python3)
```

| Comando | O que faz |
|---|---|
| `make generate` | regenera `internal/api/gen` a partir de `api/openapi.yaml` |
| `make test` | todos os testes com `-race` (precisa de Docker) |
| `make cover` | cobertura dos pacotes de negócio |
| `make lint` | `go vet` e `gofmt` |
| `make logs` / `make psql` | logs da API / psql como o papel da API |
| `make migrate-new name=...` | nova migração SQL |

Fora do Docker: `go run ./cmd/api migrate up` e `go run ./cmd/api serve`, com as variáveis de `.env.example`.

### Configuração

| Variável | Uso |
|---|---|
| `DATABASE_URL` | papel `bancada_app` (sem BYPASSRLS), usado em todas as requisições |
| `SYSTEM_DATABASE_URL` | papel `bancada_system` (BYPASSRLS), só para login, refresh e convites, antes de existir tenant |
| `MIGRATION_DATABASE_URL` | papel `bancada_owner`, dono das tabelas, só para `migrate` |
| `JWT_SECRET` | segredo HS256, mínimo de 32 caracteres |
| `DEVICE_PASSWORD_KEY` | chave AES-256 em base64 (32 bytes) para a senha do equipamento |
| `ACCESS_TOKEN_TTL`, `REFRESH_TOKEN_TTL`, `INVITATION_TTL` | validade dos tokens (15m, 720h, 168h) |
| `CORS_ORIGINS`, `PUBLIC_APP_URL` | origens do front e base do link de convite |

## Organização

```
cmd/api/              serve | migrate up|down|status
api/openapi.yaml      contrato (fonte da verdade)
internal/
  api/                composição dos handlers e política de papéis por operação (policies.go)
  api/gen/            código gerado — não editar
  identity/           contas, filiais e configurações, usuários, convites, dispositivos, numeração, auth
  registry/           clientes, contatos, centros de custo, tipos de equipamento, equipamentos, catálogo, pacotes de nicho
  serviceorder/       OS: abertura, fluxo de status, orçamentos, entrega, garantia, histórico
  inventory/          fronteira com o estoque (stub NoOp até o módulo existir)
  finance/            fronteira com o financeiro (stub NoPayments até o módulo existir)
  platform/           config, banco e RLS, auditoria, JWT/argon2, AES-GCM, problem+json, paginação
migrations/           SQL versionado (goose), embutido no binário
db/init/              cria os papéis do Postgres no primeiro start do volume
scripts/smoke.sh      teste ponta a ponta
```

Os módulos conversam por interfaces (ADR-04): `serviceorder` depende de `Registry` e `Identity`, declaradas no
próprio pacote, e não grava em tabelas de outro módulo.

## Convenções

- **Multi-tenant (ADR-01, RN-01):** toda tabela de negócio tem `account_id`, RLS com `FORCE` e a política
  `account_id = current_account_id()`. Cada requisição autenticada roda numa transação com
  `set_config('app.account_id', ..., true)`; commit se a resposta for < 400, rollback caso contrário.
- **Filial (RN-02):** OS pertencem a uma filial e exigem o header `X-Branch-ID`. Cadastros são da conta.
- **Sincronização (ADR-03):** ids UUIDv7 que o cliente pode gerar; criar de novo com o mesmo `id` devolve o
  registro existente. Toda tabela tem `version` (incrementado por trigger), `deleted_at`, `origin_device_id`,
  `created_by` e `updated_by`.
- **Dinheiro** em centavos (`*_cents`). Quantidades em `numeric`.
- **Erros** em `application/problem+json`, com `code` estável em inglês e `detail` em pt-BR dizendo o que fazer.
- **PATCH:** string vazia limpa um campo de texto opcional.
- **Auditoria (RN-03):** criações e alterações vão para `audit_log` (append-only) com usuário, filial,
  dispositivo, IP e request id. A cifra da senha do equipamento nunca entra no log.
- **Permissões:** a matriz da seção 3 está em `internal/api/policies.go`. Uma operação sem política impede a API
  de subir. Regras que dependem do dado (técnico atribuído, limite de desconto, configuração da filial) ficam
  nos serviços.

## Ordem de serviço

Fluxo de status da seção 8 (`internal/serviceorder/statemachine.go`):

```
received           → diagnosing | canceled
diagnosing         → awaiting_approval | awaiting_pickup (sem conserto) | canceled
awaiting_approval  → approved | awaiting_pickup (reprovado) | canceled
approved           → in_progress | awaiting_approval (nova versão antes de iniciar, RN-11)
in_progress        → awaiting_part | testing | awaiting_approval (nova versão, RN-11)
awaiting_part      → in_progress
testing            → ready | in_progress (teste falhou)
ready              → delivered | abandoned
awaiting_pickup    → delivered | abandoned
abandoned          → delivered
delivered, canceled: finais
```

| Operação | Regra |
|---|---|
| `POST /v1/service-orders` | exige cliente, equipamento, defeito e o checklist do tipo (RN-05); número do contador da filial ou de um bloco reservado pelo dispositivo (RN-04); senha cifrada com AES-256-GCM; responde com as garantias ativas do equipamento (UC-01 2b). Cliente PJ exige contato solicitante (RN-34) |
| `POST .../transitions` | passos de bancada: diagnóstico (todos os papéis), execução, peça, testes e pronta (técnico ou dono). Técnico só mexe nas OS atribuídas a ele; ao iniciar o diagnóstico de uma OS sem técnico, ela passa a ser dele |
| `POST .../quotes`, `PUT .../quotes/{id}` | rascunho único por OS; preço e garantia vêm do catálogo. Técnico não dá desconto; atendente até `attendant_max_discount_percent`; dono sem limite (RN-12). Preço abaixo do catálogo conta como desconto |
| `POST .../quotes/{id}/send` | validade até o fim do dia em `quote_validity_days` no fuso da filial (RN-09); versões anteriores viram `superseded` (RN-11). Total zero ou dentro de `pre_authorized_cents` é aprovado na hora (RN-10) |
| `POST .../quotes/{id}/decision` | dono ou atendente, com canal e quem autorizou; orçamento vencido é recusado. PJ: contato aprovador dentro do limite (RN-34). Aprovação parcial gera nova versão só com os itens aceitos (UC-03 3b). Reprovação leva a `awaiting_pickup` com a taxa de diagnóstico (RN-14). Na aprovação a taxa entra no valor devido, salvo se a filial a abate (RN-13) |
| `POST .../deliver` | saldo em aberto só com `authorize_open_balance` do dono (RN-15); cada item aprovado ganha `warranty_until` (RN-18); a senha do equipamento é apagada (RN-31); retirada por terceiro registra nome e documento |
| `POST .../cancel` | dono ou atendente, com motivo, antes da execução (RN-08) |
| `POST .../abandon` | só o dono, depois de `abandonment_days` desde que o equipamento ficou disponível (RN-16) |
| `POST .../warranty-return` | nova OS ligada à original com rascunho de orçamento dos itens cobertos a custo zero (RN-19) |
| `POST .../device-password/reveal` | mostra a senha e audita o acesso |
| `GET /v1/service-orders?q=` | fila e busca por número, nome do cliente (sem acento), telefone ou série (RF-11, RF-17) |
| `GET /v1/customers/{id}/history`, `GET /v1/equipment/{id}/history` | histórico de OS e garantias ativas (RF-35, RF-36) |

**Estoque e financeiro ainda não existem.** As interfaces `inventory.Reserver` (reserva na aprovação, libera ao
substituir ou cancelar, baixa na entrega) e `finance.Ledger` (valor pago e conta a receber) estão ligadas a stubs.
Com `NoPayments`, nenhuma OS aparece como paga: toda entrega com valor devido pede a autorização do dono até o
módulo financeiro chegar.

## Fora desta entrega

Sincronização offline (só as convenções estão prontas), anexos e fotos, estoque, financeiro e caixa, portal do
cliente, notificações, relatórios, importação CSV, comprovante e etiqueta (RF-10), comentários internos (RF-18),
MFA e cobrança do SaaS.
