#!/usr/bin/env bash
# End-to-end smoke test of a running API (default http://localhost:8080):
# signup → customer and equipment → open order → diagnosis → quote → approval →
# execution → tests → ready → delivery with the owner's authorization → checks.
# Needs curl and python3.
set -euo pipefail

API="${API:-http://localhost:8080}"
SUFFIX="$(date +%s)$RANDOM"
TOKEN=""
BRANCH=""

fail() { echo "FALHOU: $*" >&2; exit 1; }

# json <expression> reads JSON from stdin and prints data<expression> (Python syntax).
json() { python3 -c "import json,sys; d=json.load(sys.stdin); v=d$1; print(v if not isinstance(v,(dict,list)) else json.dumps(v))"; }

# call <method> <path> [body] prints the response body and fails on HTTP errors.
call() {
  local method="$1" path="$2" body="${3:-}" out status
  local args=(-sS -X "$method" "$API$path" -H "Content-Type: application/json" -w '\n%{http_code}')
  [[ -n "$TOKEN" ]] && args+=(-H "Authorization: Bearer $TOKEN")
  [[ -n "$BRANCH" ]] && args+=(-H "X-Branch-ID: $BRANCH")
  [[ -n "$body" ]] && args+=(--data "$body")
  out="$(curl "${args[@]}")"
  status="${out##*$'\n'}"
  out="${out%$'\n'*}"
  [[ "$status" -lt 400 ]] || fail "$method $path → $status $out"
  printf '%s' "$out"
}

step() { echo "• $*"; }

step "readyz"
call GET /readyz >/dev/null

step "signup"
EMAIL="smoke$SUFFIX@example.com"
SESSION="$(call POST /v1/auth/signup "{
  \"account\": {\"trade_name\": \"Smoke $SUFFIX\"},
  \"branch\": {\"name\": \"Matriz\", \"os_prefix\": \"MAT\"},
  \"owner\": {\"name\": \"Dona\", \"email\": \"$EMAIL\", \"password\": \"senha-forte-123\"}
}")"

step "login"
SESSION="$(call POST /v1/auth/login "{\"email\": \"$EMAIL\", \"password\": \"senha-forte-123\"}")"
TOKEN="$(echo "$SESSION" | json '["access_token"]')"
BRANCH="$(call GET /v1/branches | json '["items"][0]["id"]')"

step "customer and equipment (pacote de nicho: Notebook)"
TYPE_ID="$(call GET /v1/equipment-types | python3 -c "import json,sys; print(next(t['id'] for t in json.load(sys.stdin)['items'] if t['name']=='Notebook'))")"
CUSTOMER_ID="$(call POST /v1/customers '{
  "type": "individual", "name": "José Araújo", "contacts": [{"name": "José", "phone": "(19) 98888-7777"}]
}' | json '["id"]')"
EQUIPMENT_ID="$(call POST /v1/equipment "{
  \"customer_id\": \"$CUSTOMER_ID\", \"type_id\": \"$TYPE_ID\", \"brand\": \"Dell\", \"model\": \"Inspiron\",
  \"serial_number\": \"SMK-$SUFFIX\", \"attributes\": {\"ram_gb\": 8}
}" | json '["id"]')"
SERVICE_ID="$(call GET '/v1/services?limit=1' | json '["items"][0]["id"]')"

step "open order"
ORDER="$(call POST /v1/service-orders "{
  \"customer_id\": \"$CUSTOMER_ID\", \"equipment_id\": \"$EQUIPMENT_ID\",
  \"reported_defect\": \"Não liga\", \"device_password\": \"1234\",
  \"entry_checklist\": {\"charger\": true, \"battery\": true, \"screen_ok\": true, \"keyboard_ok\": true,
                        \"powers_on\": false, \"physical_state\": \"Riscos na tampa\"}
}")"
ORDER_ID="$(echo "$ORDER" | json '["order"]["id"]')"
NUMBER="$(echo "$ORDER" | json '["order"]["number"]')"
echo "  $NUMBER"
[[ "$NUMBER" == MAT-* ]] || fail "número inesperado: $NUMBER"

step "diagnosis and quote"
call POST "/v1/service-orders/$ORDER_ID/transitions" '{"to": "diagnosing"}' >/dev/null
call PATCH "/v1/service-orders/$ORDER_ID" '{"diagnosis": "Fonte queimada"}' >/dev/null
QUOTE_ID="$(call POST "/v1/service-orders/$ORDER_ID/quotes" "{\"items\": [{\"kind\": \"service\", \"service_id\": \"$SERVICE_ID\"}]}" | json '["id"]')"
STATUS="$(call POST "/v1/service-orders/$ORDER_ID/quotes/$QUOTE_ID/send" | json '["order"]["status"]')"
[[ "$STATUS" == awaiting_approval ]] || fail "status após envio: $STATUS"

step "approval"
call POST "/v1/service-orders/$ORDER_ID/quotes/$QUOTE_ID/decision" \
  '{"decision": "approved", "channel": "phone", "approver_name": "José"}' >/dev/null

step "execution, tests, ready"
for to in in_progress testing ready; do
  call POST "/v1/service-orders/$ORDER_ID/transitions" "{\"to\": \"$to\"}" >/dev/null
done

step "delivery with the owner's authorization"
DELIVERED="$(call POST "/v1/service-orders/$ORDER_ID/deliver" '{"authorize_open_balance": true}')"
[[ "$(echo "$DELIVERED" | json '["status"]')" == delivered ]] || fail "não entregou"
[[ "$(echo "$DELIVERED" | json '["has_device_password"]')" == False ]] || fail "senha não foi apagada"

step "checks"
UNTIL="$(call GET "/v1/service-orders/$ORDER_ID/quotes" | json '["items"][0]["items"][0]["warranty_until"]')"
[[ "$UNTIL" != None ]] || fail "garantia não iniciada"
echo "  garantia até $UNTIL"
HISTORY="$(call GET "/v1/service-orders/$ORDER_ID/history" | python3 -c "import json,sys; print(' → '.join(h['to_status'] for h in json.load(sys.stdin)['items']))")"
echo "  $HISTORY"
WARRANTIES="$(call GET "/v1/equipment/$EQUIPMENT_ID/history" | python3 -c "import json,sys; print(len(json.load(sys.stdin)['active_warranties']))")"
[[ "$WARRANTIES" -ge 1 ]] || fail "garantia ativa não aparece no histórico do equipamento"
FOUND="$(call GET "/v1/service-orders?q=jose%20araujo" | python3 -c "import json,sys; print(len(json.load(sys.stdin)['items']))")"
[[ "$FOUND" -ge 1 ]] || fail "busca por cliente não encontrou a OS"

echo "OK: $NUMBER entregue."
