ENV_FILE ?= .env.local

ifneq (,$(wildcard ./$(ENV_FILE)))
include $(ENV_FILE)
export
endif

.PHONY: run build test generate migrate-status migrate-up migrate-down compose-up compose-down

run:
	go run ./cmd/server

build:
	go build ./...

test:
	go test ./...

generate:
	go -C tools tool sqlc generate -f ../sqlc.yaml

migrate-status:
	@go -C tools tool goose -dir ../db/migrations postgres "$(DATABASE_URL)" status

migrate-up:
	@go -C tools tool goose -dir ../db/migrations postgres "$(DATABASE_URL)" up

migrate-down:
	@go -C tools tool goose -dir ../db/migrations postgres "$(DATABASE_URL)" down

compose-up:
	docker compose --env-file $(ENV_FILE) up --build -d

compose-down:
	docker compose --env-file $(ENV_FILE) down
