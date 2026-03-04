# Schemify

Schemify applies a **declarative** SQL schema to a PostgreSQL database. You define the desired state in `CREATE TABLE` (and related DDL) in `.sql` files; Schemify compares that to the live database, computes the minimal set of changes, and applies only **additive** changes (new tables, new columns). By default, it **refuses** to apply destructive changes (dropping tables or columns) and exits with an error instead.

This is different from migration-based tools (Flyway, golang-migrate, etc.): you maintain the desired schema as source of truth, not a history of deltas.

## Implementations

- **[go/](go/)** — Go implementation (CLI and library). Recommended.
- [Future] [rust/] - Rust implementation is contemplated, but not implemented (yet)

See [./go/schemify/README.md](./go/schemify/README.md) for supported DDL details.