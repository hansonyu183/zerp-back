ENV_FILE ?= .env.local

ifneq (,$(wildcard ./$(ENV_FILE)))
include $(ENV_FILE)
export
endif

.PHONY: run build test test-integration generate migrate-status migrate-up migrate-down bootstrap-admin compose-up compose-down

run:
	go run ./cmd/server

build:
	go build ./...

test:
	go test ./...

test-integration:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required" && exit 1)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./internal/domains/bob -run 'Integration|Database' -count=1

generate:
	go -C tools tool sqlc generate -f ../sqlc.yaml

migrate-status:
	@go -C tools tool goose -dir ../db/migrations postgres "$(DATABASE_URL)" status

migrate-up:
	@go -C tools tool goose -dir ../db/migrations postgres "$(DATABASE_URL)" up

migrate-down:
	@go -C tools tool goose -dir ../db/migrations postgres "$(DATABASE_URL)" down

bootstrap-admin:
	@go run ./cmd/bootstrap-admin

compose-up:
	docker compose --env-file $(ENV_FILE) up --build -d

compose-down:
	docker compose --env-file $(ENV_FILE) down
