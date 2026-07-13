# schemify applies `NOT NULL DEFAULT` columns as nullable with no default

## Context

Discovered while debugging a live 500 in the `c3` project (hub, `crates/hub/src/timeslices.rs`):
a new column, declared in `schemas/tasks.sql` as

```sql
CREATE TABLE tasks.task_timeslice (
    ...
    assignment_attempts INT NOT NULL DEFAULT 0
);
```

was added to an already-populated table via schemify (`GET/POST
/admin/v1/schemify/plan/`, which diffs the live DB against `schemas/*.sql` and
applies additive changes — see `schemas/tasks.sql`'s own header comment and
`crates/hub/src/admin.rs`'s `schemify_plan`/`schemify_apply` handlers).

After schemify applied it and `schemify-plan` reported "no migrations pending",
the live column turned out to be nullable with **no** default:

```
                  Table "tasks.task_timeslice"
       Column        |  Type   | Collation | Nullable | Default
---------------------+---------+-----------+----------+---------
 id                  | uuid    |           | not null |
 task_id             | uuid    |           |          |
 task_attempt_id     | uuid    |           |          |
 kind                | text    |           |          |
 state               | text    |           |          |
 automaton_id        | uuid    |           |          |
 assignment_attempts | integer |           |          |
 timeslice_queue     | text    |           |          |
```

Application code (`project_timeslice_queued` in
`crates/project-timeslices/crates/server/src/lib.rs`) inserted rows without
explicitly setting `assignment_attempts`, expecting the column's `DEFAULT 0` to
apply — instead the column was `NULL`. Downstream, `crates/hub/src/timeslices.rs`
decoded it via `sqlx`'s `Row::get::<i32, _>(...)` (non-`Option`), which panics on
a `NULL` value, producing a live `500 Service panicked` on
`GET /timeslices/v1/assigned/:automaton_id`.

**This isn't limited to the new column.** Checking a pre-existing table shows the
same pattern for columns that were `NOT NULL` (no default) from the table's
*original* `CREATE TABLE` statement, not just a later `ADD COLUMN`:

```sql
-- schemas/tasks.sql
CREATE TABLE tasks.task (
    id              UUID    PRIMARY KEY,
    ...
    script_ref      TEXT    NOT NULL,
    ...
    state           TEXT    NOT NULL DEFAULT 'init'
);
```

```
                   Table "tasks.task"
     Column     | Type  | Collation | Nullable | Default
----------------+-------+-----------+----------+---------
 id             | uuid  |           | not null |
 ...
 script_ref     | text  |           |          |
 ...
 state          | text  |           |          |
```

`id` (the `PRIMARY KEY`) correctly shows `not null`; every other declared
`NOT NULL` (with or without `DEFAULT`) column does not. So this looks like a
general gap in how schemify translates `NOT NULL`/`DEFAULT` clauses into DDL,
not something specific to `ADD COLUMN` on a non-empty table — though the `ADD
COLUMN` case is the one that actually surfaced a bug (existing app code relies
on `NOT NULL`/`DEFAULT` from schema files as documentation of intent, not as an
enforced DB constraint, so a *new* column whose only initial population path is
"rely on the DB default" is the first place this silently breaks something).

Compounding issue: `schemify-plan` reports "no migrations pending" against this
drifted state, meaning schemify's own diff doesn't consider the missing
`NOT NULL`/`DEFAULT` as pending drift either — so re-running apply doesn't
self-heal it.

## Example SQL to reproduce

```sql
-- v1: initial schema
CREATE TABLE example.widget (
    id    UUID PRIMARY KEY,
    name  TEXT NOT NULL
);
```

Apply via schemify against an empty DB, then check `\d example.widget` in psql.
Expected: `name` shows `Nullable: not null`. (Not yet confirmed whether the
initial-create path is affected or only demonstrated via `task.script_ref`
above, which was created a while ago — worth checking both a fresh
apply-from-empty and an apply-onto-existing-table case.)

```sql
-- v2: add a NOT NULL DEFAULT column to the same table
CREATE TABLE example.widget (
    id           UUID PRIMARY KEY,
    name         TEXT NOT NULL,
    retry_count  INT  NOT NULL DEFAULT 0
);
```

Re-run schemify plan/apply against a DB that already has rows in
`example.widget`. Expected: `ALTER TABLE example.widget ADD COLUMN retry_count
INT NOT NULL DEFAULT 0` (or equivalent, backfilling existing rows with `0`).
Observed (in the `c3` case above): column added as nullable, no default,
existing and new rows end up `NULL` unless application code explicitly sets
the value on every `INSERT`.

## Relevant files (in `c3`, the consuming project)

- `schemas/tasks.sql` — declares `assignment_attempts INT NOT NULL DEFAULT 0`
  and other `NOT NULL`/`DEFAULT` columns that don't reflect live DB state.
- `crates/hub/src/admin.rs` — `schemify_plan`/`schemify_apply`, the hub-side
  wiring for schemify's plan/apply endpoints.
- `crates/hub/Cargo.toml` — `schemify = { git = "https://github.com/earlye/schemify" }`.

## Next steps

Hand off to schemify itself (not a `c3` fix) to determine whether:
1. `NOT NULL`/`DEFAULT` clauses are being parsed but dropped when generating
   `CREATE TABLE`/`ALTER TABLE ... ADD COLUMN` DDL, or
2. They're applied correctly on `CREATE TABLE` but only lost on `ADD COLUMN`
   (needs the fresh-empty-DB repro above to distinguish), and
3. Why `schemify-plan`'s diff doesn't flag the resulting drift as pending.

## Resolution (2026-07-11)

This turned out to be a near-duplicate of
`issues/resolved/resolved-019eac8a-1414-77f1-88e3-47d64a3a40b6-schemify-not-null-default-missing.md`,
already fixed by commit `bd331c6` ("Fix missing NOT NULL/DEFAULT on added columns in Go
and Rust (#9)") before this issue file was added. That commit fixed the shared
`columnDef`/`column_def` rendering helper (Go and Rust) so both `CREATE TABLE` and
`ADD COLUMN` emit `NOT NULL`/`DEFAULT`, and added a plan-time guard rejecting an unsafe
bare-`NOT NULL`-no-`DEFAULT` add-column.

**Verified live (Go, fresh Postgres via `go/docker-compose.yml`), no regression:**
applied `CREATE TABLE public.widget (id UUID PRIMARY KEY, name TEXT NOT NULL)` from
empty via `schemify -s ... -v`. Generated SQL was
`CREATE TABLE public.widget (id uuid NOT NULL, name text NOT NULL, PRIMARY KEY (id))`,
and `\d public.widget` confirmed `name` is `not null` in the live DB. This settles
item 2 in "Next steps" above (either from parse-then-check the same as `state` in
`tasks.task`, unaffected).

**Compounding issue (item 3) confirmed real but not new:** manually dropped `NOT NULL`
on the live `name` column to simulate pre-existing drift (as would happen from a
pre-fix apply, or any other bypass of schemify), then re-ran `schemify -n` (dry-run)
against the still-`NOT NULL` desired schema. It reported zero pending migrations —
silently blind to the mismatch. Root cause is the same as
`issue-019f1a33-88af-7c06-88c5-0539c3128dd8-schemify-introspection-nullable-hardcoded.md`:
introspection hardcodes `Nullable: true` in both languages, and there's no
alter-column migration kind, so the diff engine cannot see or fix a `NOT NULL`/
`DEFAULT` mismatch on an existing column. No further action needed under *this*
issue — tracked exclusively under `019f1a33` going forward.

Closing this issue as resolved/duplicate.

## Grill Log

### 2026-07-11

- Q: Is this issue superseded by the already-merged fix (`bd331c6`) and the still-open
  `019f1a33`, or does it capture something neither does? — A: Keep it open pending an
  explicit live re-verification of the `CREATE TABLE`-from-empty path (possible
  regression concern), rather than assuming from git history alone.
- Q: After verifying `CREATE TABLE` is fixed with no regression, and confirming the
  drift-detection gap is real but is `019f1a33`'s root cause, how should this issue be
  closed out? — A: Close as resolved/duplicate; move to `issues/resolved/`.
