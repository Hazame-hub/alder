BIN        := bin/alder
PKG        := ./...
COMPOSE    := docker compose -f test/compose/docker-compose.yml
GOLANGCI   := golangci-lint

.DEFAULT_GOAL := help

## help: list the targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F':' '{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

## generate: regenerate the API types from api/openapi.yaml
# The spec is the source of truth. Edit it, run this, then satisfy the compiler.
generate:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config api/codegen.yaml api/openapi.yaml
	cd web && npm run generate

## web: build the SPA into the binary's embedded filesystem
# The Vite build restores internal/web/dist/.gitkeep itself, so that a bare
# "npm run build" cannot leave the tree in a state where "go build" fails.
web:
	cd web && npm install && npm run build

## build: build the alder binary, SPA included
build: web
	go build -o $(BIN) ./cmd/alder

## build-go: build the binary without rebuilding the SPA
build-go:
	go build -o $(BIN) ./cmd/alder

## dev: print the two commands that run the API and the Vite dev server
# Two processes on purpose: the SPA reloads on save without a Go rebuild.
dev:
	@echo "run these in two terminals:"
	@echo "  go run ./cmd/alder serve --addr 127.0.0.1:8899 --allow-http --log-level debug"
	@echo "  cd web && npm run dev"

## test: run the unit tests
test:
	go test $(PKG)

## test-race: run the unit tests with the race detector
test-race:
	go test -race $(PKG)

## cover: run the unit tests and report coverage
cover:
	go test -coverprofile=coverage.out $(PKG)
	go tool cover -func=coverage.out | tail -1

## fuzz: run every fuzz target briefly, as a smoke test
fuzz:
	go test ./internal/dn -run FuzzParse -fuzz FuzzParse -fuzztime 30s
	go test ./internal/filter -run FuzzParse -fuzz FuzzParse -fuzztime 30s
	go test ./internal/schema -run FuzzParseObjectClass -fuzz FuzzParseObjectClass -fuzztime 30s
	go test ./internal/schema -run FuzzParseAttributeType -fuzz FuzzParseAttributeType -fuzztime 30s
	go test ./internal/ldif -run FuzzUnmarshal -fuzz FuzzUnmarshal -fuzztime 30s

## vet: run go vet
vet:
	go vet $(PKG)

## lint: run golangci-lint
lint:
	$(GOLANGCI) run

## fmt: format the Go sources
fmt:
	gofmt -w $(shell git ls-files '*.go')

## check: everything CI runs
check: vet lint test

## seed: regenerate the committed seed LDIF
seed:
	go run ./test/compose/seed/gen -out test/compose/seed

## compose-up: bring the two directory servers up and seed them
compose-up:
	$(COMPOSE) up --build --detach --wait certs openldap ds389
	$(COMPOSE) run --rm --build seed

## compose-down: tear the harness down, keeping the test CA
compose-down:
	$(COMPOSE) down --remove-orphans

## compose-reset: tear the harness down and discard the test CA too
compose-reset:
	$(COMPOSE) down --remove-orphans --volumes
	rm -rf test/compose/certs

## compose-logs: follow the harness logs
compose-logs:
	$(COMPOSE) logs --follow

## test-conformance: run the conformance suite against both servers
# The suite is behind a build tag so that a plain "go test ./..." does not need
# docker. It asserts identical behaviour from one table; there is no per-vendor
# test file and adding one would defeat the point.
test-conformance:
	go test -tags conformance -count=1 -timeout 300s ./test/conformance/

## test-conformance-up: bring the harness up, run the conformance suite
test-conformance-up: compose-up test-conformance

## docker: build the distroless release image
docker:
	docker build -t alder:dev .

## clean: remove build output
clean:
	rm -rf bin dist coverage.out web/dist internal/web/dist/assets internal/web/dist/index.html

.PHONY: help generate web build build-go dev test test-race cover fuzz vet lint fmt check seed \
	compose-up compose-down compose-reset compose-logs test-conformance \
	docker clean
