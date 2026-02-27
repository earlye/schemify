.PHONY: build test run dry-run clean docker-up docker-down

build: bin/
	go build -o bin/schemify ./cmd/schemify

bin/: 
	mkdir -p $@

test:
	go test ./...

run: build docker-up
	./schemify -s ./demo/schema-v01 -v

dry-run: build
	./schemify -s ./demo/schema-v01 -n -v

clean:
	rm -f schemify

docker-up:
	docker compose up -d pgsql-dev

docker-down:
	docker compose down

docker-nuke: docker-down
	docker compose down -v

psql:
	docker compose run --rm -it psql

# Apply schema (requires DB running, e.g. make docker-up)
apply-v01: build
	./schemify -s ./demo/schema-v01 -v

apply-v02: build
	./schemify -s ./demo/schema-v02 -v
