# Issue: schemify introspection hardcodes column nullability, and there is no migration path for altering an existing column's NOT NULL/DEFAULT

**Background:** found during investigation of
`issue-019eac8a-1414-77f1-88e3-47d64a3a40b6-schemify-not-null-default-missing.md`. Not
required to fix that issue (the diff engine never compares `NOT NULL`/`DEFAULT` on
existing columns today), but it's a real gap that would silently misbehave if anyone
adds that capability later.

**Symptom:** Both implementations read back the current state of a column from the
database without reading its actual nullability, hardcoding `nullable = true` instead:

- Go: `go/schemify/internal/db/introspect.go:172` — `Nullable: true, // we don't read
  is_nullable for now; can add`
- Rust: `rust/schemify/src/db.rs:275` — `nullable: true,`; the introspection query at
  `db.rs:251` doesn't even `SELECT is_nullable`.

Separately, neither diff engine has any migration kind for altering an existing column's
type, nullability, or default — only add/drop column are handled (Go:
`internal/diff/diff.go`, `Kind*` constants around line 100; Rust: `src/diff.rs`,
`MigrationDetail` enum around line 26-36). So even after introspection is fixed, there's
no code path that would detect or apply a `NOT NULL`/`DEFAULT` change on a column that
already exists.

**Impact:** Today, low — the diff engine doesn't use the (currently wrong) nullable
value for anything. But it means:
- Drift detection can never flag "this column's NOT NULL/DEFAULT no longer matches the
  declared schema" for existing columns.
- Any future feature that diffs column nullability/default will silently see every
  existing column as nullable with no default, regardless of the real DB state, until
  introspection is fixed first.

**Action needed:**
1. Go: add `is_nullable` to the introspection query in `introspect.go` and populate
   `Column.Nullable` from it instead of hardcoding `true`.
2. Rust: add `is_nullable` to the `SELECT` in `db.rs` and populate `Column.nullable` from
   it instead of hardcoding `true`.

The "alter column" migration kind (type/nullable/default drift on existing columns) has
been split out into its own issue:
`issue-019f5453-0de0-7dc0-901e-05a77b566446-alter-column-migration-kind.md`. This issue
is now scoped to just the introspection fix (items 1-2) above.

## Grill Log

### 2026-07-11

- Q: Should this issue also add an "alter column" migration kind, or scope that out and
  only implement the introspection fix now? — A: Split it out into its own issue; this
  issue is now scoped to introspection only.
- Q: Should the alter-column follow-up be tracked as a new issue file now, or dropped
  until it comes up again? — A: Create it now, in this branch, via `md-issue-track`. See
  `issue-019f5453-0de0-7dc0-901e-05a77b566446-alter-column-migration-kind.md`.
