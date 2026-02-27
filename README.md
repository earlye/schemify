# Schemify

Schemify applies a **declarative** SQL schema to a PostgreSQL database. You define the desired state in `CREATE TABLE` (and related DDL) in `.sql` files; Schemify compares that to the live database, computes the minimal set of changes, and applies only **additive** changes (new tables, new columns). It **refuses** to apply destructive changes (dropping tables or columns) and exits with an error instead.

This is different from migration-based tools (Flyway, golang-migrate, etc.): you maintain the desired schema as source of truth, not a history of deltas.

## Implementations

- **[go/](go/)** — Go implementation (CLI and library). Recommended.

## Quick start (Go)

From the `go/` directory:

```bash
cd go
make build
# Start Postgres 18 (Docker)
make docker-up
# Apply schema (or use -n for dry-run)
./schemify -s ./demo/schema-v01 -v
```

Connection defaults: `localhost:5432`, user `schemify`, password `schemify`, database `schemify`. Override with flags (`-H`, `-p`, `-U`, `-P`, `-d`) or env (`DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`). Schema directory: `-s` or `SCHEMA_DIR`.

## Schema format

Put one or more `.sql` files in a directory. Each file can contain `CREATE TABLE` statements:

```sql
CREATE TABLE public.users (
    id integer,
    username character varying(255)
);
```

Schemify will create missing tables and add missing columns. If the database has tables or columns that are **not** in your desired schema, it will **not** drop them; it will exit with a non-zero status and list the destructive changes that would be required.

## Using as a library

```go
import "github.com/earlye/schemify/schemify"

cfg := &schemify.Config{
    Host:      "localhost",
    Port:      "5432",
    User:      "myuser",
    Password:  "mypass",
    Database:  "mydb",
    SchemaDir: "./schema",
}
sql, err := schemify.Run(ctx, cfg, schemify.ApplyOptions{Verbose: true})
// Or: load schema, introspect, diff, and apply in separate steps via
// schemify.LoadSchema, schemify.Introspect, schemify.Diff, schemify.Apply.
```

## Docker and Postgres 18

The Go app uses Postgres 18 in Docker. The data volume must be mounted at **`/var/lib/postgresql`** (not `/var/lib/postgresql/data`), because Postgres 18 creates a versioned subdirectory (e.g. `18/data`) under that path. See [go/docker-compose.yml](go/docker-compose.yml).

## License

MIT. Original work Copyright (c) 2018 Earnest; derivative work and maintenance Copyright (c) 2026 earlye.
