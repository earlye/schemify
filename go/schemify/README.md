# schemify (Go library)

Schemify applies a **declarative** SQL schema to a PostgreSQL database. You define the desired state in `CREATE TABLE` (and related DDL) in `.sql` files; Schemify compares that to the live database, computes the minimal set of changes, and applies only **additive** changes (new tables, new columns). By default, it **refuses** to apply destructive changes (dropping tables or columns) and exits with an error instead. This can be overridden by using specially-formatted SQL comments.

This is different from migration-based tools (Flyway, golang-migrate, etc.): you maintain the desired schema as source of truth, not a history of deltas.

## Installation

```bash
go get github.com/earlye/schemify/go/schemify
```

## Usage

```go
import "github.com/earlye/schemify/go/schemify"

cfg := &schemify.Options{
    Host:      "localhost",
    Port:      "5432",
    User:      "myuser",
    Password:  "mypass",
    Database:  "mydb",
    SchemaDir: "./schema",
}
sql, err := schemify.Run(ctx, cfg, schemify.ApplyOptions{Verbose: true})
```

You can also call the lower-level steps individually:

```go
// Load desired schema from .sql files
loadResult, err := schemify.LoadSchema("./schema")

// Introspect the live database
pool, _ := pgxpool.New(ctx, connStr)
introResult, err := schemify.Introspect(ctx, pool, "public")

// Diff desired vs actual
migrations, disallowed := schemify.Diff(
    loadResult.Tables, introResult.Tables,
    loadResult.Indexes, introResult.Indexes,
    nil, // or pass LoadAllowDropTableDefs result
)

// Apply additive migrations
sql, err := schemify.Apply(ctx, pool, migrations, schemify.ApplyOptions{DryRun: true})
```

## Schema format

Put one or more `.sql` files in a directory containing DDL. 

### Tables

DDL can contain `CREATE TABLE` statements:

```sql
CREATE TABLE public.users (
    id integer,
    username character varying(255)
);
```

Schemify will create missing tables and add missing columns. If the database has tables or columns that are **not** in your desired schema, it will **not** drop them by default; it will exit with a non-zero status and list the destructive changes that would be required.

**Allowing column drops:** You can allow dropping a column by adding a directive comment with the column name and type (so the drop only applies when the actual column type matches). Use `-- removed: colname type` inside the `CREATE TABLE` body, or `-- removed: colname ANY_TYPE` to allow the drop regardless of type.

**Allowing table drops:** You can allow dropping a table that is no longer in the desired schema by adding a comment block that starts with `-- DROP TABLE schema.tablename (` and lists the table's columns exactly as in the database; the block must end with `-- );`. The column list in the block is parsed and compared to the current table — only if they match exactly (same columns and types) is the drop allowed; otherwise the run fails with a destructive change.

### Indexes

Indexes are declared alongside your `CREATE TABLE` statements using `CREATE INDEX CONCURRENTLY`:

```sql
CREATE TABLE public.users (
    id integer,
    username character varying(255)
);

CREATE INDEX CONCURRENTLY idx_users_username ON public.users (username);
```

**`CONCURRENTLY` is required.** Schemify rejects any `CREATE INDEX` that does not use `CONCURRENTLY` and will return an error at load time. This is enforced to keep index creation safe on large tables in production.

**Unique indexes** are supported:

```sql
CREATE UNIQUE INDEX CONCURRENTLY idx_users_username ON public.users (username);
```

**Index types** (`btree`, `gin`, `gist`, `hash`) are supported via the standard `USING` clause; `btree` is the default if omitted:

```sql
CREATE INDEX CONCURRENTLY idx_events_payload ON public.events USING gin (payload);
```

**Indexes are additive-only.** Schemify will create indexes that are in your schema files but missing from the database. Removing an index from your schema files is a destructive change — Schemify will refuse to apply it and exit with an error listing the disallowed drop.

## License

MIT. Original work Copyright (c) 2018 Earnest; derivative work and maintenance Copyright (c) 2026 earlye.
