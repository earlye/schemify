.PHONY: build test run dry-run clean docker-up docker-down

build: bin/
	go build -o bin/schemify ./cmd/schemify

bin/: 
	mkdir -p $@

test:
	go test ./...

run: build docker-up
	./bin/schemify -s ./schemas/demo-v01 -v

dry-run: build
	./bin/schemify -s ./schemas/demo-v01 -n -v

clean:
	rm -rf bin

nuke: clean docker-nuke

docker-up:
	docker compose up -d postgres

docker-down:
	docker compose down

docker-nuke: docker-down
	docker compose down -v

psql:
	docker compose run --rm -it psql

# Apply schema (requires DB running, e.g. make docker-up)
apply-v01: docker-up build
	./bin/schemify -s ./schemas/demo-v01 -v

apply-v02: docker-up build 
	./bin/schemify -s ./schemas/demo-v02 -v

apply-v03-fail: docker-up build 
	./bin/schemify -s ./schemas/demo-v03-destructive -v


apply-v03-override: docker-up build 
	./bin/schemify -s ./schemas/demo-v03-destructive-col-override -v

apply-v04-fail: docker-up build 
	./bin/schemify -s ./schemas/demo-v04-destructive -v

apply-v04-override-fail: docker-up build 
	./bin/schemify -s ./schemas/demo-v04-destructive-tbl-override-wrong -v

apply-v04-override: docker-up build 
	./bin/schemify -s ./schemas/demo-v04-destructive-tbl-override -v