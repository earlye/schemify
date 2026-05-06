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
export DB_SSLMODE=disable DB_PORT=5433
./target/release/schemify -s ../go/schemas/demo-v01 -v
```

## Docker sandbox + demo schemas

From this directory:

```bash
make up          # postgres:18 on localhost:5433
make test        # cargo test (starts postgres if needed)
make apply-v01   # applies ../go/schemas/demo-v01
```

See [Makefile](./Makefile) for other `apply-*` targets (same names as `go/Makefile`).

## Library

```rust
use schemify::{run, Options, DatabaseConfig, ApplyOptions};
```

See crate root docs in `schemify/src/lib.rs`.

## Tests

Unit tests live under `schemify/tests/`. Integration tests connect with `DB_*` (default port `5432`); use `DB_PORT=5433` when using this folder’s `docker-compose.yml`.

License: MIT (match repo).
