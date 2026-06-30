# Issue: schemify does not apply NOT NULL or DEFAULT when adding columns

**Symptom:** Adding a column defined as `TEXT NOT NULL DEFAULT 'init'` to an existing
table via `schemas/tasks.sql` caused schemify to generate an `ALTER TABLE` that added
the column as nullable with no default. Existing rows received NULL; the NOT NULL
constraint and default value were silently dropped from the migration.

**Workaround applied (2026-06-09):** Manually set all existing NULLs and ran
`ALTER TABLE tasks.task ALTER COLUMN state SET NOT NULL` and
`ALTER TABLE tasks.task ALTER COLUMN state SET DEFAULT 'init'` by hand.

**Impact:** Any schema change that adds a NOT NULL column with a default to an existing
table will require manual intervention until this is fixed in schemify.

**Action needed:** File a bug against schemify. The generated ALTER TABLE should
include `SET DEFAULT` and `SET NOT NULL` steps (with a backfill UPDATE for existing rows
if needed) when the target schema declares both constraints on a new column.

## Investigation (2026-06-30)

Confirmed in **both** the Go and Rust implementations, and broader than originally
reported: it also affects `CREATE TABLE`, not just `ADD COLUMN`.

**Root cause:** a shared SQL-rendering helper renders only `name type`, discarding
`NOT NULL`/`DEFAULT` entirely, even though the parsed `Column` struct already carries
both fields correctly.

- Go: `columnDef()` in `go/schemify/internal/apply/apply.go:217-219`, used by both
  `createTableSQL` (apply.go:119) and `addColumnSQL` (apply.go:206).
- Rust: `column_def()` in `rust/schemify/src/apply.rs:258-260`, used by both
  `create_table_sql` (apply.rs:106) and `add_column_sql` (apply.rs:243).

No existing test in either repo asserts on `NOT NULL`/`DEFAULT` in generated SQL or
checks `information_schema` post-apply, so this has never been caught.

A related but separate gap — DB introspection hardcoding `nullable = true` instead of
reading `is_nullable`, and the missing "alter existing column" migration kind needed for
future nullable/default drift detection — is tracked separately in
`issue-019f1a33-88af-7c06-88c5-0539c3128dd8-schemify-introspection-nullable-hardcoded.md`.
It is not required to fix this issue, since the diff engine never compares
nullable/default on existing columns today.

## Fix plan

1. **Fix the rendering helper in both languages.**
   - Go `columnDef` (`apply.go:217`): append `" NOT NULL"` if `!c.Nullable`, append
     `" DEFAULT " + c.Default` if `c.Default != ""`.
   - Rust `column_def` (`apply.rs:258`): same logic.
   - Fixes both `CREATE TABLE` and `ADD COLUMN` since they share the helper.

2. **Guard the unsafe case at plan time.** `ADD COLUMN ... NOT NULL DEFAULT v` is safe
   and atomic on Postgres 11+, but `ADD COLUMN ... NOT NULL` with no `DEFAULT` against a
   table that already exists will fail outright. Detect this combination when building
   the `AddColumn` migration (Go: `internal/diff/diff.go:363-378`; Rust: `src/diff.rs`
   around line 402) and return a planning error with a clear message instead of emitting
   SQL that Postgres will reject.

3. **Tests in both languages.**
   - Unit test: add-column diff with `NOT NULL DEFAULT 'x'` → assert generated SQL
     contains both clauses.
   - Unit test: add-column with `NOT NULL` and no default → assert planning errors.
   - Integration test (both repos have real-Postgres integration tests): add a
     `NOT NULL DEFAULT` column to a table with existing rows, apply, then query
     `information_schema.columns` to confirm `is_nullable='NO'` and `column_default`
     matches, and confirm existing rows backfilled to the default.
