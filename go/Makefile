export DB_SSLMODE=disable

.PHONY: all
all: bin/schemify

.PHONY: dependencies
dependencies: clean 
	$(MAKE) .dependencies

.dependencies:
	go get -u github.com/earlye/eaux/go/types
	touch $@

.PHONY: buildb
build: bin/schemify

bin/schemify: bin/ .dependencies
	go build -o bin/schemify ./cmd/schemify

bin/: 
	mkdir -p $@

.PHONY: test
test:
	go test ./...
	cd schemify && go test ./...

.PHONY: clean
clean:
	rm -rf bin
	rm -f .dependencies

.PHONY: nuke
nuke: clean docker-nuke

.PHONY: docker-up
docker-up:
	docker compose up -d postgres

.PHONY: docker-down
docker-down:
	docker compose down

.PHONY: docker-nuke
docker-nuke: docker-down
	docker compose down -v

.PHONY: psql
psql:
	docker compose run --rm -it psql

# Apply schema (requires DB running, e.g. make docker-up)
.PHONY: apply-v01
apply-v01: docker-up build
	./bin/schemify -s ./schemas/demo-v01 -v

.PHONY: apply-v02
apply-v02: docker-up build 
	./bin/schemify -s ./schemas/demo-v02 -v

.PHONY: apply-v03-fail
apply-v03-fail: docker-up build 
	./bin/schemify -s ./schemas/demo-v03-destructive -v

.PHONY: apply-v03-override
apply-v03-override: docker-up build 
	./bin/schemify -s ./schemas/demo-v03-destructive-col-override -v

.PHONY: apply-v04-fail
apply-v04-fail: docker-up build 
	./bin/schemify -s ./schemas/demo-v04-destructive -v

.PHONY: apply-v04-override-fail
apply-v04-override-fail: docker-up build 
	./bin/schemify -s ./schemas/demo-v04-destructive-tbl-override-wrong -v

.PHONY: apply-v04-override
apply-v04-override: docker-up build 
	./bin/schemify -s ./schemas/demo-v04-destructive-tbl-override -v

.PHONY: apply-v05
apply-v05: docker-up build 
	./bin/schemify -s ./schemas/demo-v05 -v