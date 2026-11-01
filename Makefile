GOOSE := $(CURDIR)/.bin/goose
DATABASE_URL ?= postgres://postgres:postgres@localhost:5432/cashapp?sslmode=disable
GOOSE_TAGS := no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb

.PHONY: tools deps up down migrate-up migrate-down run-ledger run-gateway build test integration coverage coverage-integration smoke

deps:
	cd ledger && go mod download
	cd gateway && go mod download

tools:
	GOBIN=$(CURDIR)/.bin go install -tags='$(GOOSE_TAGS)' github.com/pressly/goose/v3/cmd/goose@v3.24.3

up:
	docker compose up -d --wait

down:
	docker compose down

migrate-up:
	$(GOOSE) -dir ./ledger/migrations postgres '$(DATABASE_URL)' up

migrate-down:
	$(GOOSE) -dir ./ledger/migrations postgres '$(DATABASE_URL)' down

run-ledger:
	go run ./ledger/cmd/ledger

run-gateway:
	go run ./gateway

build:
	go build -o .bin/ledger ./ledger/cmd/ledger
	go build -o .bin/gateway ./gateway

test:
	go test -race ./ledger/... ./gateway/...
	go vet ./ledger/... ./gateway/...

integration:
	go test -race -tags=integration -count=1 -v ./ledger/... ./gateway/...

coverage:
	go test -cover -coverprofile=cover.out ./ledger/... ./gateway/...
	go tool cover -func=cover.out
	go tool cover -html=cover.out -o cover.html

coverage-integration:
	go test -race -tags=integration -count=1 -coverpkg=./ledger/...,./gateway/... -coverprofile=cover.out ./ledger/... ./gateway/...
	go tool cover -func=cover.out
	go tool cover -html=cover.out -o cover.html

smoke: build
	python3 ./scripts/smoke.py
