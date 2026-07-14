# schemify: manual index-expr deparse fallback has no FuncCall arm, breaks function-call `DEFAULT`s

## Context

`bd331c6` ("Fix missing NOT NULL/DEFAULT on added columns in Go and Rust
(#9)") fixed `parse_column_def` in `rust/schemify/src/load.rs` to actually
read `NOT NULL`/`DEFAULT` off `CreateStmt` column constraints instead of
silently dropping them. That's the intended fix — but it has a side effect:
`DEFAULT` expressions on `CREATE TABLE` columns are now actually parsed and
deparsed via `deparse_expr` → `deparse_expr_for_index`, and that
manual-deparse fallback (originally written only for index expressions) has
no arm for `FuncCall` nodes.

Minimal SQL that reproduces it, loaded via `load_decorated_from_dir` (i.e. a
plain `CREATE TABLE` in a `.sql` file in the schema dir):

```sql
CREATE TABLE example.thing (
    id         UUID PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Loading this fails with:

```
Error: load schema: <file>.sql: parse SQL: unsupported index expression node (manual deparse)
```

Concretely: `deparse_expr_for_index` (load.rs) tries `ne.deparse()` first,
and falls back to a manual `match` over a handful of node kinds
(`CollateClause`, `TypeCast`, `ColumnRef`, `AConst`, `AExpr`) when the
libpg_query deparser rejects the bare (non-statement-level) node. `NOW()`
parses to a `FuncCall` node, which isn't one of those arms, so it falls into
the catch-all `"unsupported index expression node (manual deparse)"` error.

Before `bd331c6`, `parse_column_def` read `cd.raw_default`, which per that
commit's own message is never populated by the raw parser for `CREATE TABLE`
columns — so `default` was always empty and no deparse ever ran for these
columns. The new constraint-based parsing is correct in reading the
`DEFAULT` expression at all, but the manual deparse fallback wasn't extended
to match, so **any function-call default** — not just `NOW()`, any
zero-or-more-arg `FuncCall` such as `gen_random_uuid()`, `current_timestamp()`,
etc. — now breaks schema loading entirely. Since `id ... DEFAULT
gen_random_uuid()` and `created_at ... DEFAULT NOW()` are about as common as
`CREATE TABLE` columns get, this affects a large fraction of realistic
schemas, not an edge case.

## Relevant files

- `rust/schemify/src/load.rs:394` (`index_elem_sql`) and `:412`
  (`deparse_expr_for_index`) — the manual deparse fallback missing the
  `FuncCall` arm.
- `rust/schemify/src/load.rs:560` (`deparse_expr`, added in `bd331c6`) —
  reuses `deparse_expr_for_index` for column `DEFAULT` expressions, which is
  what newly exposes this gap outside of index expressions.
- `rust/schemify/src/load.rs:595`-ish (`parse_column_def`) — now correctly
  reads `DEFAULT` off `cd.constraints`, which is what surfaces the bug.

## Root cause hypothesis

`deparse_expr_for_index`'s manual fallback was written narrowly for the
index-expression cases it was first built for (`CollateClause`/`TypeCast`
around things like `(doc->>'kind')`) and never covered `FuncCall`. Reusing it
for column `DEFAULT` expressions (via `bd331c6`'s `deparse_expr`) exposed the
gap because function-call defaults are common in `CREATE TABLE`, unlike in
index expressions.

## Next steps

- Add a `FuncCall` arm to `deparse_expr_for_index` (or a sibling function) in
  `rust/schemify/src/load.rs` that renders `func_name(args...)`, handling at
  least zero-arg calls (`NOW()`, `gen_random_uuid()`) and simple
  literal/column-ref args.
- Add regression coverage: a `CREATE TABLE` column with a
  `DEFAULT <function_call>()` default, including a case with arguments (not
  just `NOW()`, to catch arg-rendering too).
