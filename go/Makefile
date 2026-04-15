export DB_SSLMODE=disable
GOFILES=$(shell find . -name "*.go")

.PHONY: all
all: bin/schemify

.PHONY: dependencies
dependencies: clean
	$(MAKE) .dependencies

.dependencies: $(GOFILES)
	cd schemify && go mod tidy
	go mod tidy
	touch $@

.PHONY: buildb
build: bin/schemify $(GOFILES)

bin/schemify: bin/ .dependencies
	go build -o bin/schemify ./cmd/schemify

bin/:
	mkdir -p $@

.PHONY: test
test: up
	go test ./...
	cd schemify && go test ./...

.PHONY: clean
clean:
	rm -rf bin
	rm -f .dependencies

.PHONY: nuke
nuke: clean docker-nuke

.PHONY: up
up:
	docker compose up -d postgres

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
