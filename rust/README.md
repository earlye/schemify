# schemify (Rust)

Rust library and CLI mirroring [go/schemify](../go/schemify): load declarative `.sql`, introspect PostgreSQL, diff, apply additive migrations only (refuse destructive drift unless overrides in SQL comments).

## Build

```bash
cargo build -p schemify-cli --release
```

Binary: `target/release/schemify`.

## CLI

Same flags/env as the Go CLI (`DB_*`, `SCHEMA_DIR`, `-s` schema dir, `-n` dry-run, `-v` / `-vv` for logging). Defaults match Go (`localhost`, user/database/password `schemify`, schema dir `./schemas/demo-v01`).

```bash
export DB_SSLMODE=disable DB_PORT=11543
./target/release/schemify -s ../go/schemas/demo-v01 -v
```

## Docker sandbox + demo schemas

From this directory:

```bash
make up          # postgres:18 on localhost:11543
make test        # cargo test (starts postgres if needed)
make apply-v01   # applies ../go/schemas/demo-v01
```

See [Makefile](./Makefile) for other `apply-*` targets (same names as `go/Makefile`).

## Library

```rust
use schemify::{run, Options, DatabaseConfig, ApplyOptions};
```

See crate root docs in `schemify/src/lib.rs`.

### Schemas (namespaces)

`CREATE SCHEMA IF NOT EXISTS …` in SQL is optional; namespaces are also inferred from qualified `CREATE TABLE` / `CREATE INDEX` DDL. `plan` / `run` create missing namespaces (including `public` when referenced) and refuse surplus namespaces as `drop_schema` destructive drift (`public` is never flagged for drop).

### Loading schema files

`load_from_dir` / `load_schema` require **every** `*.sql` file in the directory to parse successfully. A syntax or policy error in one file (for example `CREATE INDEX` without `CONCURRENTLY`) fails the whole load with an error naming the file. See [CHANGELOG.md](./CHANGELOG.md) for the rationale (fail-fast; avoids silent partial models).

### Changelog

Behavior changes are recorded in [CHANGELOG.md](./CHANGELOG.md).

## Tests

Unit tests live under `schemify/tests/`. Integration tests connect with `DB_*` (default port `5432`); use `DB_PORT=11543` when using this folder’s `docker-compose.yml` (or rely on `make test`, which sets it).

License: MIT (match repo).
