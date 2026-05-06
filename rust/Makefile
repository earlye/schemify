# Rust schemify sandbox (mirrors go/Makefile apply-* targets).
export DB_SSLMODE=disable
export DB_PORT=5433
export DB_HOST=localhost
export DB_USER=schemify
export DB_PASSWORD=schemify
export DB_NAME=schemify

SHELL=/bin/bash -euo pipefail

SCHEMA_ROOT:=../go/schemas

.PHONY: all
all: build

.PHONY: build
build:
	cargo build -p schemify-cli --release

.PHONY: test
test: up
	cargo test --workspace

.PHONY: up
up:
	docker compose --progress=plain up -d postgres

.PHONY: down
down:
	docker compose down

.PHONY: docker-nuke
docker-nuke: down
	docker compose down -v

RUN=cargo run -p schemify-cli --release --

.PHONY: run
run: up build
	$(RUN) $(ARGS)

.PHONY: apply-v01
apply-v01: up build
	$(RUN) -s $(SCHEMA_ROOT)/demo-v01 -v

.PHONY: apply-v02
apply-v02: up build
	$(RUN) -s $(SCHEMA_ROOT)/demo-v02 -v

.PHONY: apply-v03-fail
apply-v03-fail: up build
	$(RUN) -s $(SCHEMA_ROOT)/demo-v03-destructive -v

.PHONY: apply-v03-override
apply-v03-override: up build
	$(RUN) -s $(SCHEMA_ROOT)/demo-v03-destructive-col-override -v

.PHONY: apply-v04-fail
apply-v04-fail: up build
	$(RUN) -s $(SCHEMA_ROOT)/demo-v04-destructive -v

.PHONY: apply-v04-override-fail
apply-v04-override-fail: up build
	$(RUN) -s $(SCHEMA_ROOT)/demo-v04-destructive-tbl-override-wrong -v

.PHONY: apply-v04-override
apply-v04-override: up build
	$(RUN) -s $(SCHEMA_ROOT)/demo-v04-destructive-tbl-override -v

.PHONY: apply-v05
apply-v05: up build
	$(RUN) -s $(SCHEMA_ROOT)/demo-v05 -v

.PHONY: apply-constraints-00
apply-constraints-00: up build
	$(RUN) -s $(SCHEMA_ROOT)/demo-constraints-00 -v

.PHONY: apply-constraints-01
apply-constraints-01: up build
	$(RUN) -s $(SCHEMA_ROOT)/demo-constraints-01 -vv

.PHONY: apply-broken
apply-broken: up build
	$(RUN) -s $(SCHEMA_ROOT)/demo-broken-1 -v
