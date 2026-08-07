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
test-db:
	ZP_TEST_DATABASE_URL=$(TEST_DB) go test -count=1 ./...

check: fmt vet test

fmt:
	gofmt -l -w .

vet:
	go vet ./...

migrate: build
	./$(BIN) migrate up

models: build
	./$(BIN) models check

openapi: build
	./$(BIN) openapi > openapi.json
	@echo "wrote openapi.json"

clean:
	rm -rf bin openapi.json
