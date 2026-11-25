.PHONY: generate build test cover up down logs psql migrate-new lint smoke

generate: ## regenerate server code from api/openapi.yaml
	go generate ./...

build:
	go build -o bin/api ./cmd/api

test: ## run all tests (needs Docker for testcontainers)
	go test -race -count=1 ./...

cover: ## coverage of the business packages, counting every test that exercises them
	go test -count=1 -coverpkg=./internal/identity/,./internal/registry/,./internal/serviceorder/ -coverprofile=coverage.out ./internal/...
	go tool cover -func=coverage.out | tail -1

up: ## start db, run migrations and start the API
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f api

psql: ## open psql as the API role
	docker compose exec db psql -U bancada_app -d bancada

migrate-new: ## create a new migration: make migrate-new name=add_something
	@test -n "$(name)" || (echo "usage: make migrate-new name=..." && exit 1)
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir migrations -s create $(name) sql

lint:
	go vet ./...
	gofmt -l . | (! grep .)

smoke: ## run the end-to-end smoke script against localhost:8080
	./scripts/smoke.sh
