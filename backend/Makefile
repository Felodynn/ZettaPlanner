BIN := bin/zettaplanner
TEST_DB ?= postgres:///zettaplanner_test

.PHONY: build test test-db check fmt vet migrate models openapi clean

build:
	go build -o $(BIN) ./cmd/zettaplanner

# Unit tests only. The store tests skip themselves without a database.
test:
	go test ./...

# Everything, including the tests that need a real Postgres. The database is
# the product; a mock of it would test nothing.
#
# Each test package gets its own scratch database, created on demand from
# template1 — which is why the setup below installs pgvector there.
#
#   sudo apt install -y postgresql-18-pgvector
#   sudo -u postgres psql -c "CREATE DATABASE zettaplanner_test OWNER $$USER;"
#   sudo -u postgres psql -d template1 -c "CREATE EXTENSION vector;"
test-db:
	ZP_TEST_DATABASE_URL=$(TEST_DB) go test -count=1 ./...

check: fmt vet test

fmt:
	gofmt -l -w .

vet:
	go vet ./...

migrate: build
	./$(BIN) migrate up

# Bootstrap the first admin. Refuses once any account exists.
bootstrap: build
	./$(BIN) admin create-user -email $(EMAIL) -name "$(NAME)" -role admin

models: build
	./$(BIN) models check

openapi: build
	./$(BIN) openapi > openapi.json
	@echo "wrote openapi.json"

clean:
	rm -rf bin openapi.json
