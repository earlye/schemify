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

## Local authoring, global planning inputs

**Authoring stays local:** drift comments live next to the relevant `CREATE TABLE`, index statement, or file region. Humans edit small, scoped surfaces.

**Planning inputs are global artifacts:** the loader and planner must see the **entire** desired source (all files in the schema set) before drift can be resolved. In particular:

- The **desired model** is already global (all tables and indexes from all files).
- The **decorated schema** is global: drift directives are extracted and attached, and namespaced `DRIFT {id}` fragments from different places are **merged** into one logical group per `{id}` before planning.
- **Anticipated drift** is global *per id*: for each `{id}`, the tool builds a **merged bundle** (union of fragments from table-local blocks, file-level blocks, index-related blocks, etc.). There is not one monolithic “anticipated entire database” unless you explicitly define that; typically you keep **`{id} → merged anticipated fragment(s)`** for matching against **actual**.

**Implication:** load order and merge rules for the same `{id}` must be well-defined across the whole schema input; diff/plan never runs on “just this table’s comments” in isolation once `DRIFT` namespaces span scopes.

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

- **`DRIFT {id}`** — Namespace for a **drift effort**: the same `{id}` can appear in more than one place (see below). Policy (`DROP` / `DEPRECATED`) should be **consistent** for a given `{id}` within a file (or define merge rules if not).
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

### Namespaced `DRIFT` ids (cross-scope groups)

Use **`DRIFT {id}`** as a **namespace** so one logical “effort” (migration, release, cleanup) can attach fragments from different scopes. Load merges all blocks with the same `{id}` (and compatible policy) into one **decorated** group for planning.

Example: table-local surplus plus file-global surplus under the same effort `x`:

```sql
CREATE TABLE a (
  ...
  -- DRIFT x DROP (
  --  ... things removed from table `a` under effort `x` ...
  -- )
);

-- DRIFT x DROP (
--   ... things removed globally under effort `x` (e.g. standalone indexes, other tables)
-- )
```

**Why this helps**

- **Outside-table entities** (indexes, objects not inside a single `CREATE TABLE`) can contribute to the **same** anticipated drift / drop-allowance group as in-table fragments, without inventing artificial “fake tables” to hold index-only drift.
- **Human workflow** — Authors think in terms of “effort `x`” across a file, not only per-table comment islands.

**Rules to pin down when implementing**

- **Merge semantics** — Same `{id}`: concatenate anticipated fragments, single policy per id, deterministic ordering when applying drops within the group.
- **Conflicts** — Same `{id}` with different policies (`DROP` vs `DEPRECATED`) in one file should be a **load error** (or last-wins with explicit warning—pick one; prefer error).
- **Scope of “global” block** — File-level inner body likely uses **full statement-shaped** synthetic DDL (`CREATE INDEX …`, `ALTER TABLE …`, or multiple probes), not only `CREATE TABLE` probes; grammar subset must be documented.
- **Cross-file** — Unless explicitly supported later, treat `{id}` as **per-file** namespace to avoid collisions across schema dirs.

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

---

## Development roadmap

This section translates the design above into a concrete, sequenced implementation plan. Each phase is a shippable increment with defined success criteria. Later phases depend on earlier ones; within a phase, PRs are largely independent.

### Phase 1 — Decorated schema types

**Goal:** Introduce the `DriftBlock` / `DecoratedLoadResult` types and thread them through the load path without changing any existing behaviour.

**Files to touch:**
- `go/schemify/internal/schema/types.go` — add new types (no existing types change):
  ```go
  type DriftPolicy string
  const (
      DriftPolicyDrop       DriftPolicy = "DROP"
      DriftPolicyDeprecated DriftPolicy = "DEPRECATED"
  )
  type DriftScope int
  const (
      DriftScopeTable DriftScope = iota // block inside CREATE TABLE body
      DriftScopeFile                    // block at file level (for indexes, etc.)
  )
  type DriftBlock struct {
      ID       string      // the {id} from "-- DRIFT {id} POLICY ("
      Policy   DriftPolicy
      RawBody  string      // stripped inner text between the delimiters
      Scope    DriftScope
      // Set when Scope == DriftScopeTable:
      TableSchema string
      TableName   string
  }
  type DecoratedTable struct {
      Table
      DriftBlocks []DriftBlock // blocks extracted from this table's statement RawSQL
  }
  type DecoratedLoadResult struct {
      LoadResult                       // embedded; unchanged callers still compile
      DecoratedTables map[string]*DecoratedTable // same key as Tables
      FileLevelDrift  []DriftBlock               // file-level blocks (outside CREATE TABLE)
  }
  ```
- `go/schemify/internal/schema/load.go` — add `LoadDecoratedFromFS(fsys fs.FS) (*DecoratedLoadResult, error)` that calls existing `LoadFromFS` and returns a `DecoratedLoadResult` with empty drift slices; no extraction yet.
- `go/schemify/schemify.go` — expose `LoadDecoratedSchema(fsys fs.FS) (*schema.DecoratedLoadResult, error)`.

**Success criteria:**
- `LoadDecoratedSchema` returns a non-nil result with empty `DecoratedTables` and `FileLevelDrift`; all existing tests pass unchanged.

---

### Phase 2 — Block extractor

**Goal:** Parse `-- DRIFT {id} POLICY (` … `-- )` blocks from raw SQL with balanced-paren and quote awareness.

**Files to create / touch:**
- `go/schemify/internal/schema/drift.go` (new file):
  - `driftBlockOpenRE`: `(?m)^\s*--\s*DRIFT\s+(\w+)\s+(DROP|DEPRECATED)\s*\(`
  - `driftBlockCloseRE`: `(?m)^\s*--\s*\)\s*$`
  - `extractDriftBlocks(rawSQL, tableSchema, tableName string) ([]DriftBlock, error)`:
    - Line-scan state machine; when open pattern matches, enter block, increment paren depth.
    - Strip `-- ` prefix from each body line; increment depth for each unquoted `(`, decrement for `)`.
    - Exit when depth returns to zero at a line matching `driftBlockCloseRE`.
    - Return error for unclosed blocks; return error if the same `{id}` appears with two different policies (conflict).
    - Non-block comment lines (outside an open block) are ignored.
  - Quote/string awareness: track single-quote and dollar-quote state so `'hello (world)'` doesn't confuse depth counting.
- `go/schemify/internal/schema/drift_test.go` (new file): unit tests for `extractDriftBlocks`:
  - Basic DROP block inside a table statement.
  - DEPRECATED block.
  - Block containing `CHECK (...)` (nested parens).
  - Block containing a single-quoted literal with `(`.
  - Unclosed block → error.
  - Same `{id}`, same policy, two blocks → ok (separate DriftBlock entries, merge happens later).
  - Same `{id}`, different policies → error.
  - File-level block (empty tableSchema/tableName).

**Wire into load:** In `LoadDecoratedFromFS`, after `parseDDL` populates a table statement, call `extractDriftBlocks(stmt.RawSQL, tableSchema, tableName)` and attach results to `DecoratedTable.DriftBlocks`. Also scan the entire file body for file-level blocks (those outside any `CREATE TABLE` statement—implementation approach: collect offsets of parsed statements and scan the gaps, or do a second pass over the whole file with empty table context).

**Success criteria:**
- All extractor unit tests pass.
- `LoadDecoratedSchema` on the existing demo schemas returns zero drift blocks (no existing schema uses the new syntax).

---

### Phase 3 — Anticipated drift model

**Goal:** Parse each `DriftBlock.RawBody` through the existing DDL parser to produce a validated `AnticipatedTable` (and optional `AnticipatedIndexes`) for use in planner matching.

**Files to touch:**
- `go/schemify/internal/schema/types.go` — extend `DriftBlock`:
  ```go
  type DriftBlock struct {
      // ... existing fields ...
      AnticipatedTable   *Table    // non-nil after Phase 3 population
      AnticipatedIndexes []*Index  // for file-level blocks containing CREATE INDEX
  }
  ```
- `go/schemify/internal/schema/drift.go` — add `buildAnticipatedDrift(block *DriftBlock) error`:
  - **Table-scope blocks:** wrap body as `CREATE TABLE __drift_probe__ (\n{rawBody}\n);` and call `parseDDL()`. Assign result to `block.AnticipatedTable`. Propagate schema/table name from block context into FK references if needed (substitute `__drift_probe__` back to actual names post-parse).
  - **File-scope blocks:** call `parseDDL(rawBody)` directly (body contains full DDL statements). Populate `block.AnticipatedIndexes` from any `CREATE INDEX CONCURRENTLY` statements parsed; populate `block.AnticipatedTable` if any `CREATE TABLE` statement is found.
  - Return a descriptive error on parse failure (include block `ID`, policy, and raw body excerpt in the message).
- `go/schemify/internal/schema/load.go` — after calling `extractDriftBlocks`, call `buildAnticipatedDrift` for each block; propagate errors as load errors.
- `go/schemify/internal/schema/drift_test.go` — add tests:
  - Table-scope block with a column and a FK → `AnticipatedTable` has expected shape.
  - Table-scope block with `CHECK (val > 0)` → parses without error.
  - File-scope block with `CREATE INDEX CONCURRENTLY idx ON schema.t (col)` → `AnticipatedIndexes` populated.
  - Invalid DDL inside body → load returns error.

**Also:** add `DriftGroups map[string]*DriftGroup` to `DecoratedLoadResult` and a helper `MergeDriftGroups(blocks []DriftBlock) (map[string]*DriftGroup, error)`:
```go
type DriftGroup struct {
    ID     string
    Policy DriftPolicy
    // Union of all anticipated tables/indexes across all blocks with this ID:
    AnticipatedColumns     []Column
    AnticipatedConstraints struct {
        UniqueKeys  []UniqueConstraint
        ForeignKeys []ForeignKey
    }
    AnticipatedIndexes []*Index
}
```
Merge is a per-file operation; treat same `{id}` with different policies as a load error.

**Success criteria:**
- A test schema with a valid `-- DRIFT x DROP (old_col TEXT NOT NULL)` block loads without error.
- A schema with syntactically invalid body returns a clear load error naming the block.
- `DriftGroups` contains one entry per distinct `{id}` with unified anticipated columns.

---

### Phase 4 — Planner rules

**Goal:** Promote drift-covered surplus from `disallowed` to planned migrations (for `DROP` policy) or silently tolerate (for `DEPRECATED` policy).

**Files to touch:**
- `go/schemify/internal/diff/diff.go`:
  - Add `DriftGroups map[string]*schema.DriftGroup` parameter to `Diff()` (add it at the end; callers passing `nil` keep existing behaviour).
  - In the surplus-column loop (where `drop_column` disallowed changes are emitted today): for each surplus column, check all `DROP` groups' `AnticipatedColumns` for a match on `Name` + normalized `Type`. On match: emit a `Migration{Kind: KindDropColumn, Detail: &DropColumnDetail{ColumnName: ...}}` instead of a `DestructiveChange`.
  - Same for surplus UNIQUE constraints: check `AnticipatedConstraints.UniqueKeys` by name + columns.
  - Same for surplus FK constraints: check `AnticipatedConstraints.ForeignKeys` by name + columns.
  - Same for surplus indexes: check `AnticipatedIndexes` by name + table + columns (using existing `IndexMatches`).
  - For `DEPRECATED` groups: on match, emit neither migration nor disallowed (log at `slog.Debug`).
  - Unmatched surplus: existing disallowed behaviour unchanged.
- `go/schemify/schemify.go` — update `Diff()` wrapper to accept and pass `DriftGroups`.

**New migration kinds needed (in diff.go):**
- `KindDropUnique = "drop_unique_key"` and `DropUniqueDetail{ConstraintName string}`.
- `KindDropFK = "drop_foreign_key"` and `DropFKDetail{ConstraintName string}`.
- `KindDropIndex = "drop_index"` and `DropIndexDetail{Index *schema.Index}`.

(Note: `KindDropColumn` and `DropColumnDetail` already exist; `KindDropTable` and `DropTableDetail` also exist — these can be reused for the `DROP` policy on whole-table drift once table-level drift blocks are supported.)

**Success criteria (unit tests in `go/schemify/internal/diff/` or inline):**
- Desired has no `old_col`, actual has `old_col TEXT NOT NULL`, drift group `x` has `DROP` for `old_col TEXT NOT NULL` → `drop_column` migration emitted, not a disallowed change.
- Same setup but `DEPRECATED` policy → neither migration nor disallowed.
- Surplus column not covered by any drift group → still disallowed (regression guard).
- Surplus FK covered by `DROP` group → `drop_foreign_key` migration.
- Surplus index covered by `DROP` group → `drop_index` migration.

---

### Phase 5 — Apply

**Goal:** Generate correct SQL for the new drift-allowed drop migration kinds.

**Files to touch:**
- `go/schemify/internal/apply/apply.go`:
  - `drop_column`: SQL already generated by `dropColumnSQL` (line 90). Verify idempotency (`DROP COLUMN IF EXISTS`).
  - `drop_unique_key`: `ALTER TABLE {schema}.{table} DROP CONSTRAINT IF EXISTS {name}`.
  - `drop_foreign_key`: same pattern as unique.
  - `drop_index`: `DROP INDEX CONCURRENTLY IF EXISTS {schema}.{name}` — must run **outside** the transaction (same precedent as `CREATE INDEX CONCURRENTLY`; see `Apply()` transaction grouping logic in apply.go).
  - Wire all new detail types into `migrationSQL()`.
- `go/schemify/internal/apply/apply.go` — ensure `DROP INDEX CONCURRENTLY` migrations are excluded from the main transaction block and executed in the non-transactional step (parallel to `CREATE INDEX CONCURRENTLY`).

**Success criteria:**
- Unit tests for each new `migrationSQL` case.
- `dropColumnSQL` returns `ALTER TABLE schema.t DROP COLUMN IF EXISTS col` (add `IF EXISTS` if not already present).

---

### Phase 6 — Integration tests and demo schemas

**Goal:** End-to-end validation with a live PostgreSQL instance; observable demo for the new syntax.

**New demo schemas (`go/schemas/`):**
- `demo-drift-v01/`: baseline — table `public.things` with columns `id`, `name`, `legacy_code TEXT NOT NULL` and index `CREATE INDEX CONCURRENTLY idx_things_legacy ON public.things (legacy_code)`.
- `demo-drift-v02-drop/`: same table without `legacy_code` or index, but with drift blocks:
  ```sql
  CREATE TABLE public.things (
    id BIGSERIAL,
    name TEXT NOT NULL
    -- DRIFT cleanup1 DROP (
    --   legacy_code TEXT NOT NULL,
    -- )
  );
  -- DRIFT cleanup1 DROP (
  -- CREATE INDEX CONCURRENTLY idx_things_legacy ON public.things (legacy_code);
  -- )
  ```
  Cross-scope merge for `cleanup1` should allow both the column drop and index drop.
- `demo-drift-v03-deprecated/`: table keeps `name` and `legacy_code` in desired, but `legacy_code` is `DEPRECATED`:
  ```sql
  -- DRIFT tolerate1 DEPRECATED (
  --   legacy_code TEXT NOT NULL,
  -- )
  ```
  Running against a DB with `legacy_code` present should produce no error and no migration.

**Integration tests (`go/schemify/schemify_integration_test.go`):**
- Apply v01 → diff/apply v02-drop → verify `legacy_code` column and `idx_things_legacy` index are gone.
- Apply v01 → diff v03-deprecated → verify zero migrations and zero disallowed changes.
- Apply v01 → diff with no drift directive → verify `drop_column` is in disallowed (regression guard).

**Makefile targets (`go/Makefile`):**
- `apply-drift-v01`, `apply-drift-v02-drop`, `apply-drift-v03-deprecated` for manual demos.

---

### Phase 7 — Cross-scope merge and conflict rules

**Goal:** Harden the `{id}` namespace rules across files and scope types, per the design above.

**Scope:**
- Same `{id}` in two different `.sql` files: currently treated as per-file (load error if policies conflict in one file; silently merge across files). Decide and document one of: (a) cross-file merge with a conflict check, or (b) load error on any cross-file collision.
- Add a validation pass at the end of `LoadDecoratedFromFS` that scans all `DriftGroups` across all files and reports conflicts.
- Document the chosen rule in this file and in the `LoadDecoratedFromFS` godoc.

**Success criteria:**
- Same `{id}` + same policy across two files: both anticipated fragments merged (no error).
- Same `{id}` + different policies across two files: load error with clear message naming both files and policies.

---

### Phase 8 — Legacy directive deprecation

**Goal:** Once the `DRIFT` block system is end-to-end (Phases 1–7), detect the old comment-based directives (`-- removed:` and `-- DROP TABLE`) and fail at load time with a clear migration prompt so authors convert to the new syntax.

**Background:** The legacy mechanisms are implemented in `go/schemify/internal/schema/load.go`:
- `removedDirectiveRE` — scans `-- removed: colname type` inside `CREATE TABLE` bodies.
- `extractDropTableBlockDefs` / `LoadAllowDropTableDefs` — scans `-- DROP TABLE schema.t (` … `-- );` blocks at file level.

Keeping both systems active in parallel is not the goal: after the new syntax is stable, the legacy forms should become hard load errors that show the author exactly what to write instead.

**Files to touch:**
- `go/schemify/internal/schema/load.go`:
  - In `parseDDL`, after extracting `allowDrops` via `extractRemovedDirectives`: if any are found, return a load error instead of attaching them to the table. Error message must include the table name and a ready-to-paste replacement `-- DRIFT` block, e.g.:
    ```
    legacy "-- removed:" directive found in table public.users (column: passwordhash character varying(64)).
    Replace with:
      -- DRIFT <choose-an-id> DROP (
      --   passwordhash character varying(64),
      -- )
    ```
  - In `extractDropTableBlockDefs` / `LoadAllowDropTableDefs`: when a `-- DROP TABLE` block is detected, return a load error with a ready-to-paste file-level `-- DRIFT` replacement:
    ```
    legacy "-- DROP TABLE" directive found for public.events.
    Replace with:
      -- DRIFT <choose-an-id> DROP (
      -- CREATE TABLE public.events (
      --   id integer,
      --   event character varying(255)
      -- );
      -- )
    ```
  - Remove or guard `removedDirectiveRE`, `dropTableCommentRE`, `dropTableBlockEndRE` behind a `legacyDirectivesEnabled` flag (see below) so the error path can be tested by enabling the legacy path in tests.

- `go/schemify/schemify.go`: remove the `LoadAllowDropTableDefs` export (or deprecate it) once the legacy path is disabled.

**Migration guide:** add a short section to `README.md` (or a `MIGRATING.md`) describing the one-time conversion from legacy directives to `DRIFT` blocks, with before/after SQL examples.

**Transition strategy — two-phase rollout:**

Phase 8a (this PR): emit a **structured warning** (not an error) via `slog.Warn` when a legacy directive is detected, and include the ready-to-paste replacement in the log message. Both old and new syntax continue to work.

Phase 8b (follow-up PR, after teams have had time to migrate): promote the warning to a **hard load error**. Remove the regex constants and related helpers once no test schema uses the old syntax. Update integration tests to use the new syntax throughout.

**Success criteria (Phase 8a):**
- Loading a schema with `-- removed:` emits a `slog.Warn` message containing the table name, column, type, and suggested replacement block; load still succeeds.
- Loading a schema with `-- DROP TABLE` emits a `slog.Warn` with a suggested file-level `DRIFT` replacement; load still succeeds.
- All existing demo schemas that use legacy directives continue to pass their integration tests.

**Success criteria (Phase 8b):**
- Loading a schema with `-- removed:` returns a load error; no tests use that directive any more.
- Loading a schema with `-- DROP TABLE` returns a load error; no tests use that directive any more.
- `LoadAllowDropTableDefs` is deleted; `removedDirectiveRE` and friends are deleted.
- A new `go/schemas/demo-drift-legacy-error/` schema exists to document the error message shape as a snapshot test.

---

### Out of scope for this plan

- Rust port of drift block support (parallel effort after Go implementation stabilises).
- Auto-generating drift block stubs from actual surplus (a tooling/UX feature, not a planning feature).
- `DEPRECATED` generating warnings in structured log output (deferred; add as a follow-up once DEPRECATED semantics are proven).
- Full SQL grammar coverage inside drift blocks on day one — documented subset is columns, FK, UNIQUE, CHECK, and standalone `CREATE INDEX CONCURRENTLY`.
