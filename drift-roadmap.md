# Drift management roadmap

This document captures the design direction for **drift management**: explicit, comment-driven control over how the tool reconciles **desired** schema files with **actual** databases, including destructive or tolerant handling of surplus objects.

## Terminology

| Term | Meaning |
|------|---------|
| **Desired schema (source)** | Human-authored `*.sql` (DDL plus drift comments). Single source file set. |
| **Desired model** | Result of parsing DDL only: tables, columns, constraints, indexes as structured types (e.g. `schema.Table`). Comments are not represented here unless extracted. |
| **Actual schema** | Result of introspection against a live database (same conceptual shapes as desired model). |
| **Drift directives** | Data extracted from comments: what extra or legacy state is expected, tolerated, or should be removed. Today: `-- removed:` columns, `-- DROP TABLE` blocks. Planned: grouped constraint/column drift blocks. |
| **Decorated schema** | **Desired model + drift directives** in one place. This is the durable input to **planning** (diff): “what we want” plus “how to treat known classes of mismatch.” |
| **Anticipated drift model** | Ephemeral parse artifact: a fragment of schema shape the author believes **might still exist** in some deployed databases (used for matching, allowlisting, or validating directives). Not persisted; optional `slog.Debug` of synthetic SQL / parsed structs. Formerly discussed as “potential schema” or “shadow surplus model”; **anticipated drift model** is the preferred name. |

## Pipeline (target)

1. **Load / decorate**  
   Parse desired SQL → desired model. Extract drift directives from comments → attach to decorated schema.

2. **Directive interpretation (optional per block)**  
   For each directive that carries table-body-shaped text, build **synthetic DDL** (e.g. `CREATE TABLE __drift_probe__ ( … );`), run the existing SQL parser (`postgresparser` via `parseDDL`), and populate an **anticipated drift model** fragment for that block. Use only for matching and validation; log at debug if useful.

3. **Plan**  
   Compare **decorated schema** to **actual schema**. Emit additive migrations and classify surplus as destructive errors unless a directive explicitly allows or commands handling (drop, ignore, etc.).

4. **Apply**  
   Execute planned migrations only; no re-parsing of directives.

## Grouped drift blocks (proposed syntax)

Drift annotations should be **groupable** so dependent objects (e.g. column + FK on that column) are managed atomically.

Sketch (inside `CREATE TABLE`):

```sql
CREATE TABLE public.questions (
  id BIGSERIAL,
  -- … columns as desired …
  -- DRIFT group1 DROP (
  --   old_column TEXT NOT NULL,
  --   CONSTRAINT old_fk FOREIGN KEY (old_column) REFERENCES public.other (id)
  -- )
  -- DRIFT group2 DEPRECATED (
  --   legacy_flag TEXT NOT NULL
  -- )
);
```

- **`DRIFT {name}`** — Human-readable group id; internally key by table + name + policy.
- **`DROP` | `DEPRECATED`** — Policy (exact semantics TBD):
  - **DROP**: If actual matches this bundle, plan explicit drops (or treat as allowed destructive work) rather than failing as unexplained drift.
  - **DEPRECATED**: Tolerate presence in actual without treating as fatal / or downgrade to warning — define precisely when implemented.
- **Inner body** — Table-subset DDL (columns + table-level constraints). Parentheses keep it **SQL-ish** and familiar.

### Parsing notes

- Comments are invisible to the main DDL parser; drift blocks must be **extracted first** (line scan over raw file or statement `RawSQL`).
- **Balanced parentheses**: inner bodies can contain `CHECK (…)`, casts, and nested parens. Extraction must be **depth-aware** (and ideally quote/string aware), not “first closing `)`”.
- **Closing line**: mirror existing `-- DROP TABLE … (` / `-- );` style with an unambiguous end (e.g. `-- )` only after depth returns to zero) for consistency with the codebase’s block-comment precedent.

### Indexes and file-level drift

Many indexes are not declared inside `CREATE TABLE`. Plan for either:

- file-level `DRIFT` blocks, or  
- a parallel convention for `CREATE INDEX` adjacent comments,

so index surplus is groupable the same way as in-table constraints.

### Why not require indexes only inside `CREATE TABLE`?

**Decision:** keep supporting **standalone** `CREATE INDEX` (and the project’s existing **`CREATE INDEX CONCURRENTLY`** requirement for safety). Do **not** require all indexes to live inside `CREATE TABLE` just to simplify drift comments.

**Reasons (summary):**

1. **Concurrency** — Large-table practice relies on `CREATE INDEX CONCURRENTLY`. Index creation inside `CREATE TABLE` does not offer the same operational model as a separate concurrent index build.
2. **Partial and expression indexes** — Predicates (`WHERE …`) and expressions in index definitions are not expressible as ordinary inline table constraints; the tool already depends on this class of index (e.g. JSONB expression indexes in tests).
3. **Unique index vs UNIQUE constraint** — Teams use `CREATE UNIQUE INDEX` for shapes that differ from a table `UNIQUE` constraint; collapsing to table-only DDL removes that flexibility.
4. **Lifecycle and noise** — Multiple indexes per table, tuning iterations, and `INCLUDE` / opclass details are easier to manage as separate statements than as one giant `CREATE TABLE`.

**Implication for drift:** simplification comes from **clear scoping rules** (which `DRIFT` block applies to which object), not from forbidding standalone index DDL.

### DRIFT blocks and outside-table declarations (serious concern)

Grouping drift for **columns and constraints that appear in the `CREATE TABLE` body** is relatively local: the block sits in one statement, synthetic parse is a `CREATE TABLE` probe, and attachment to the decorated table is obvious.

**Outside-table entities** (primarily **standalone indexes**, and possibly other file-scoped DDL later) are a **serious design concern**:

- **Attachment** — A `DRIFT` block must unambiguously bind to a specific index (name + table + schema), or to a span of statements, without fragile “nearest comment wins” heuristics.
- **Ordering** — Drift that spans “drop column X then drop index on X” (or the reverse) needs explicit group semantics or documented ordering rules so planning stays deterministic.
- **Synthetic parse shape** — Table-body DDL maps cleanly to `CREATE TABLE __probe__(...)`. A standalone index maps to `CREATE INDEX CONCURRENTLY ...` probes (and must respect the same **CONCURRENTLY** rules as desired schema loading). Mixed groups (column + index) may need **multiple synthetic statements** per block, not one probe.
- **Surplus vs desired** — Index diff today is keyed separately from tables; decorated schema and planner must thread **index-shaped drift** through the same “disallowed unless allowed” pipeline as FK/UNIQUE surplus.

Treat **file-level or index-adjacent `DRIFT`** as a first-class design item: specify syntax, attachment rules, and tests before assuming in-table `DRIFT` syntax generalizes.

## Reuse of the real parser

The project already uses **`postgresparser`** for `parseDDL`. Drift inner text should be lifted into **synthetic statements** and parsed the same way as normal DDL—not reimplemented with ad-hoc regex for full SQL.

Enclosing table context (schema, table name) must be supplied when building synthetic SQL (e.g. for `FOREIGN KEY` resolution).

## Relation to today’s directives

| Mechanism | Scope |
|-----------|--------|
| `-- removed: col type` / `ANY_TYPE` | Column drop allowlist (regex in `internal/schema/load.go`). |
| `-- DROP TABLE schema.t (` … `-- );` | Table drop allowlist (`LoadAllowDropTableDefs`). |
| Surplus FK/UNIQUE vs desired | Today: `drop_foreign_key` / `drop_unique_key` destructive unless future directive allows drop. |

Grouped `DRIFT` blocks are the natural generalization: one syntax for **bundles** of legacy columns and constraints (and eventually indexes at file scope), with explicit policy.

## Implementation order (suggested)

1. **Decorated schema type** — Single struct (or pair) holding parsed tables/indexes plus attached drift blocks; thread through load → diff CLI path.
2. **Block extractor** — Paren-aware scanner for `-- DRIFT name POLICY (` … `-- )` (exact end token TBD).
3. **Anticipated drift model** — Synthetic `CREATE TABLE` probe + `parseDDL`; validate and normalize like real tables.
4. **Planner rules** — Map matched surplus to allowed drops or ignorable deprecated per policy; keep destructive default when unmatched.
5. **Apply** — `ALTER TABLE … DROP CONSTRAINT` / `DROP COLUMN` / etc., consistent with existing safety (transactions, idempotency).
6. **Tests** — Unit tests for extractor + parser glue; integration tests with live Postgres for end-to-end drift acceptance.

## Non-goals (for early versions)

- Parsing arbitrary comment placement inside arbitrary SQL without clear block delimiters.
- Auto-dropping without an explicit `DROP` policy in a drift block or existing allowlist pattern.
- Full PostgreSQL DDL coverage inside drift blocks on day one — start with a **documented subset** (columns, FK, UNIQUE, CHECK) and expand.

## References in repo

- Directive parsing today: `go/schemify/internal/schema/load.go` (`removedDirectiveRE`, `extractDropTableBlockDefs`, `LoadAllowDropTableDefs`).
- Surplus constraint detection: `go/schemify/internal/diff/diff.go` (`drop_unique_key`, `drop_foreign_key`).
- Constraint naming / idempotency: `go/schemify/internal/helpers/constraints.go`, load + introspect alignment.

---

*This file is a design roadmap, not a commitment to ship order or final syntax. Update as decisions land.*
