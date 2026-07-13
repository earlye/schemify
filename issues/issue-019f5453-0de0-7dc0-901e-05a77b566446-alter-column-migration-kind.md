# Add an "alter column" migration kind for existing-column NOT NULL/DEFAULT/type drift (Go + Rust)

## Context

Split out from `issue-019f1a33-88af-7c06-88c5-0539c3128dd8-schemify-introspection-nullable-hardcoded.md`
during grilling on 2026-07-11. That issue covers two things: (1) introspection hardcoding
`nullable = true` instead of reading `is_nullable`, and (2) the missing "alter column"
migration kind. Per that grill session, the two were split because the introspection fix
is a trivial, zero-behavior-change bug fix, while this — adding an actual migration kind
for altering an existing column — is a net-new feature with its own design surface
(safety semantics for narrowing changes, backfill strategy). Impact today is none, since
the diff engine never compares `Nullable`/`Default`/type for existing columns.

Currently there is no migration `Kind`/`Detail` for altering an existing column's
nullable, default, or type. The diff engine's column-comparison loop only handles
add-by-name and drop-by-name; for columns present in both the desired and actual schema,
nothing is compared.

**Depends on** the introspection fix in `019f1a33` (Go `introspect.go:172` / Rust
`db.rs:275` currently hardcode `nullable = true`) landing first — without accurate
introspection, this new diff logic would have nothing real to compare against.

## Relevant files

- `go/schemify/internal/diff/diff.go:314-389` — column-diff loop; only handles
  add-by-name/drop-by-name today, would need a third branch for "present in both, but
  drifted" columns
- `go/schemify/internal/diff/diff.go:90-103` — `Kind*` constants; needs a new
  `KindAlterColumn`
- `go/schemify/internal/diff/diff.go:106-130` — `MigrationDetail` types; needs a new
  detail struct for the alter-column case
- `go/schemify/internal/apply/apply.go:81-110` — `migrationSQL` switch; needs a new case
- `go/schemify/internal/apply/apply.go:217-226` — `columnDef`; does not share syntax with
  `ALTER COLUMN SET/DROP NOT NULL`, `SET/DROP DEFAULT`, or `TYPE` changes — new SQL
  generation needed, not a reuse of this helper
- `rust/schemify/src/diff.rs:311-410` (approx.) — column-diff loop, same shape as Go
- `rust/schemify/src/diff.rs:24-37` — `MigrationDetail` enum; needs a new variant
- `rust/schemify/src/apply.rs:67-95` — `migration_sql` match; needs a new arm
- `rust/schemify/src/apply.rs:258+` — `column_def`; same non-reuse caveat as Go
- `go/schemify/internal/diff/diff_test.go` (839 lines, 28 test funcs) — reference for
  expected test density/shape
- `rust/schemify/tests/diff_tests.rs` (437 lines, 13 test funcs) — reference for expected
  test density/shape

## Scope

1. New `Kind` constant + `Detail` struct/enum variant in both languages.
2. New SQL generation for `ALTER COLUMN SET/DROP NOT NULL`, `SET/DROP DEFAULT`, and
   `TYPE` change.
3. A safety guard for narrowing `NOT NULL` without a backfill strategy, analogous to the
   `ADD COLUMN NOT NULL`-without-`DEFAULT` guard from the resolved `019eac8a` issue.
4. Tests in both languages — roughly 15 new per-language cases judging by existing test
   density: nullable-tightens, nullable-loosens, default-changes, type-changes,
   combinations, and disallowed narrowing cases.

This is a net-new feature, not a bug fix.
