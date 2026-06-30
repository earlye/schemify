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
3. Decide whether to add an "alter column" migration kind (type/nullable/default drift
   on existing columns) in both languages, or explicitly scope that out for now. This is
   a design decision, not a mechanical fix — raise it with the maintainer before
   implementing.
