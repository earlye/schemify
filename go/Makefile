export DB_SSLMODE=disable
export DB_PORT=5433
GOFILES=$(shell find . -name "*.go")
SHELL=/bin/bash -euo pipefail
DECOLOR:=$(shell ./decolor.sh)

.PHONY: all
all: bin/schemify

.PHONY: dependencies
dependencies: clean
	$(MAKE) .dependencies

.dependencies: $(GOFILES)
	cd schemify && go mod tidy
	go mod tidy
	touch $@

.PHONY: build
build: bin/schemify $(GOFILES)

bin/schemify: bin/ .dependencies
	go build -o bin/schemify ./cmd/schemify

bin/:
	mkdir -p $@

.PHONY: test
test: up
	rm -f .coverage.*
	$(MAKE) .coverage.html

.uncover:
	go install github.com/gregoryv/uncover/cmd/uncover@b56b18c8233ef7a454eed0c045ecf19b30da36dd
	touch $@

.coverage.html: .coverage.out
	go tool cover -html=.coverage.out -o $(abspath $@)

.PHONY: view-coverage
view-coverage: .coverage.html
	open $(abspath $<)

.coverage.out: .coverage.out.tmp .uncover
	./munge-coverage.sh
	$(HOME)/go/bin/uncover -min 5.0 .coverage.out | $(DECOLOR)

.coverage.out.tmp: $(GOFILES) | up
	cd schemify && go test -v ./... -count=1 -coverprofile=$(abspath .coverage.out.tmp) | sed "s/^coverage:/coverage :/g"

.PHONY: clean
clean:
	rm -rf bin
	rm -f .dependencies
	rm -f .coverage.*

.PHONY: nuke
nuke: clean docker-nuke

.PHONY: up
up:
	docker compose --progress=plain up -d postgres

.PHONY: down
down:
	docker compose down

.PHONY: docker-nuke
docker-nuke: down
	docker compose down -v

.PHONY: psql
psql:
	docker compose run --rm -it psql

run: up build
	./bin/schemify $(ARGS)

# Apply schema (requires DB running, e.g. make up)
.PHONY: apply-v01
apply-v01: up build
	./bin/schemify -s ./schemas/demo-v01 -v

.PHONY: apply-v02
apply-v02: up build
	./bin/schemify -s ./schemas/demo-v02 -v

.PHONY: apply-v03-fail
apply-v03-fail: up build
	./bin/schemify -s ./schemas/demo-v03-destructive -v

.PHONY: apply-v03-override
apply-v03-override: up build
	./bin/schemify -s ./schemas/demo-v03-destructive-col-override -v

.PHONY: apply-v04-fail
apply-v04-fail: up build
	./bin/schemify -s ./schemas/demo-v04-destructive -v

.PHONY: apply-v04-override-fail
apply-v04-override-fail: up build
	./bin/schemify -s ./schemas/demo-v04-destructive-tbl-override-wrong -v

.PHONY: apply-v04-override
apply-v04-override: up build
	./bin/schemify -s ./schemas/demo-v04-destructive-tbl-override -v

.PHONY: apply-v05
apply-v05: up build
	./bin/schemify -s ./schemas/demo-v05 -v

.PHONY: apply-broken
apply-broken: up build
	./bin/schemify -s ./schemas/demo-broken -v

.PHONY: apply-constraints-00
apply-constraints-00: up build
	./bin/schemify -s ./schemas/demo-constraints-00 -v

.PHONY: apply-constraints-01
apply-constraints-01: up build
	timeout 30 ./bin/schemify -s ./schemas/demo-constraints-01 -v=TRACE
