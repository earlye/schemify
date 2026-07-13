package schemify_test

// Integration tests that require a live PostgreSQL connection.
// Run via: make test (starts postgres via docker compose, then go test ./...).
// The test skips automatically if the database is unreachable.

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	ss "github.com/earlye/sensitive-strings/golang/ss"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/earlye/schemify/go/schemify"
	idb "github.com/earlye/schemify/go/schemify/internal/db"
	"github.com/earlye/schemify/go/schemify/internal/schema"
)

// itestPrefix is prepended to all table and index names created by integration
// tests so they can be filtered out of the public schema without disturbing
// other tables that may exist in the test database.
const itestPrefix = "schemify_itest_"

// itestNSSchema is a dedicated PostgreSQL schema for non-public introspection tests.
const itestNSSchema = "schemify_itest_ns"

// itestNSSchemaSQL is a minimal fixture with a CREATE SCHEMA preamble; the schema must
// exist in the database before Apply (schemify does not emit CREATE SCHEMA migrations).
const itestNSSchemaSQL = `
CREATE SCHEMA IF NOT EXISTS ` + itestNSSchema + `;

CREATE TABLE ` + itestNSSchema + `.schemify_itest_ns_table (
    id text NOT NULL,
    PRIMARY KEY (id)
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS schemify_itest_idx_ns_table_id
    ON ` + itestNSSchema + `.schemify_itest_ns_table (id);
`

// itestSchemaSQL defines a schema that exercises several index types:
//   - single-column index
//   - multi-column index
//   - unique index
//   - expression index on a jsonb column (tests pg_get_indexdef double-paren
//     normalization introduced in PostgreSQL 18)
//
// A second table uses CamelCase table, column, and index names to verify that
// PostgreSQL identifier normalisation (unquoted names fold to lowercase) is
// handled correctly and that a second apply run produces no migrations.
const itestSchemaSQL = `
CREATE TABLE public.schemify_itest_items (
    name   text NOT NULL,
    label  text NOT NULL,
    props  jsonb NOT NULL,
    PRIMARY KEY (name)
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS schemify_itest_idx_items_label
    ON public.schemify_itest_items (label);

CREATE INDEX CONCURRENTLY IF NOT EXISTS schemify_itest_idx_items_label_name
    ON public.schemify_itest_items (label, name);

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS schemify_itest_idx_items_label_uniq
    ON public.schemify_itest_items (label);

CREATE INDEX CONCURRENTLY IF NOT EXISTS schemify_itest_idx_items_props_kind
    ON public.schemify_itest_items ((props->>'kind'));

CREATE TABLE public.schemify_itest_Records (
    recordId   text NOT NULL,
    recordKind text NOT NULL,
    recordData jsonb NOT NULL,
    PRIMARY KEY (recordId)
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS schemify_itest_idx_Records_recordKind
    ON public.schemify_itest_Records (recordKind);

CREATE INDEX CONCURRENTLY IF NOT EXISTS schemify_itest_idx_Records_recordData_kind
    ON public.schemify_itest_Records ((recordData->>'kind'));

CREATE TABLE public.schemify_itest_fk_parent (
    code text NOT NULL UNIQUE,
    payload text NOT NULL
);

CREATE TABLE public.schemify_itest_fk_child (
    child_id text NOT NULL,
    parent_code text NOT NULL REFERENCES public.schemify_itest_fk_parent (code),
    PRIMARY KEY (child_id)
);

CREATE TABLE public.schemify_itest_channels (
    owner   text  NOT NULL,
    channel jsonb NOT NULL,
    CONSTRAINT schemify_itest_channels_check CHECK (
        channel ? 'kind' AND channel ? 'value'
    )
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS schemify_itest_idx_channels_owner
    ON public.schemify_itest_channels (owner);
`

// TestIntegration_IndexIdempotency verifies that running schemify twice against
// a live database produces no migrations on the second run. It covers:
//   - simple column index (tests generate_subscripts 0-vs-1-based fix)
//   - multi-column index
//   - unique index
//   - jsonb expression index (tests pg_get_indexdef double-paren normalisation)
func TestIntegration_IndexIdempotency(t *testing.T) {
	ctx := context.Background()
	pool := itestPool(t, ctx)
	// Register cleanup before pool.Close so the table drop runs while the
	// connection is still open. t.Cleanup runs in LIFO order.
	t.Cleanup(func() { pool.Close() })
	itestCleanup(t, ctx, pool)
	t.Cleanup(func() { itestCleanup(t, ctx, pool) })

	fsys := fstest.MapFS{
		"schema.sql": &fstest.MapFile{Data: []byte(itestSchemaSQL)},
	}

	// First run: expect table + index creation migrations.
	run1 := itestApply(t, ctx, pool, fsys, "first run")
	if len(run1) == 0 {
		t.Fatal("first run: expected at least one migration (table/index creation), got none")
	}

	// Second run: must be fully idempotent — no migrations.
	run2 := itestApply(t, ctx, pool, fsys, "second run")
	if len(run2) != 0 {
		t.Errorf("second run: expected no migrations (idempotency), got %d: %v", len(run2), run2)
	}
}

// TestIntegration_NonPublicSchemaIdempotency verifies load → diff → apply against a
// schema other than public when the SQL file leads with CREATE SCHEMA IF NOT EXISTS.
func TestIntegration_NonPublicSchemaIdempotency(t *testing.T) {
	ctx := context.Background()
	pool := itestPool(t, ctx)
	t.Cleanup(func() { pool.Close() })
	itestCleanupNamespace(t, ctx, pool)
	t.Cleanup(func() { itestCleanupNamespace(t, ctx, pool) })

	fsys := fstest.MapFS{
		"schema.sql": &fstest.MapFile{Data: []byte(itestNSSchemaSQL)},
	}

	run1 := itestApplyInSchema(t, ctx, pool, fsys, "first run", itestNSSchema)
	if len(run1) == 0 {
		t.Fatal("first run: expected at least one migration, got none")
	}

	run2 := itestApplyInSchema(t, ctx, pool, fsys, "second run", itestNSSchema)
	if len(run2) != 0 {
		t.Errorf("second run: expected no migrations, got %d: %v", len(run2), run2)
	}
}

func TestIntegration_ExtraConstraintsDetectedAsDestructive(t *testing.T) {
	ctx := context.Background()
	pool := itestPool(t, ctx)
	t.Cleanup(func() { pool.Close() })
	itestCleanup(t, ctx, pool)
	t.Cleanup(func() { itestCleanup(t, ctx, pool) })

	fsys := fstest.MapFS{
		"schema.sql": &fstest.MapFile{Data: []byte(itestSchemaSQL)},
	}

	// Apply desired schema first.
	_ = itestApply(t, ctx, pool, fsys, "baseline")

	// Inject extra constraints that are not in desired schema.
	if _, err := pool.Exec(ctx, "ALTER TABLE public.schemify_itest_fk_parent ADD CONSTRAINT schemify_itest_fk_parent_payload_unique UNIQUE (payload)"); err != nil {
		t.Fatalf("add extra unique: %v", err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE public.schemify_itest_fk_child ADD CONSTRAINT schemify_itest_fk_child_child_id_extra_fk FOREIGN KEY (child_id) REFERENCES public.schemify_itest_fk_parent (code)"); err != nil {
		t.Fatalf("add extra fk: %v", err)
	}

	desired, err := schemify.LoadSchema(fsys)
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	actual, err := schemify.Introspect(ctx, pool, "public")
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	actualTables := itestFilterTables(actual.Tables, itestPrefix)
	actualIndexes := itestFilterIndexes(actual.Indexes, itestPrefix)
	desiredNs := schema.CollectDesiredNamespaces(desired)
	actualNs := itestFilterNamespaces(t, ctx, pool, "public")

	_, disallowed := schemify.Diff(desiredNs, actualNs, desired.Tables, actualTables, desired.Indexes, actualIndexes, nil)
	if len(disallowed) == 0 {
		t.Fatal("expected destructive drift for extra constraints, got none")
	}
	var sawDropUnique, sawDropFK bool
	for _, d := range disallowed {
		if d.Kind == "drop_unique_key" && d.Name == "schemify_itest_fk_parent_payload_unique" {
			sawDropUnique = true
		}
		if d.Kind == "drop_foreign_key" && d.Name == "schemify_itest_fk_child_child_id_extra_fk" {
			sawDropFK = true
		}
	}
	if !sawDropUnique || !sawDropFK {
		t.Fatalf("expected drop_unique_key and drop_foreign_key for injected constraints, got %v", disallowed)
	}
}

// itestApply loads the given schema, introspects the DB (filtered to itest
// tables/indexes only), diffs, applies, and returns the applied migrations.
func itestApply(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, label string) []schemify.Migration {
	return itestApplyInSchema(t, ctx, pool, fsys, label, "public")
}

// itestApplyInSchema is like itestApply but introspects the given PostgreSQL schema name.
func itestApplyInSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, label, introspectSchema string) []schemify.Migration {
	t.Helper()

	desired, err := schemify.LoadSchema(fsys)
	if err != nil {
		t.Fatalf("%s: LoadSchema: %v", label, err)
	}

	actual, err := schemify.Introspect(ctx, pool, introspectSchema)
	if err != nil {
		t.Fatalf("%s: Introspect: %v", label, err)
	}

	// Narrow the actual DB snapshot to only test-owned objects so that other
	// tables in the database do not trigger spurious destructive-change errors.
	actualTables := itestFilterTables(actual.Tables, itestPrefix)
	actualIndexes := itestFilterIndexes(actual.Indexes, itestPrefix)
	desiredNs := schema.CollectDesiredNamespaces(desired)
	actualNs := itestFilterNamespaces(t, ctx, pool, introspectSchema)

	migrations, disallowed := schemify.Diff(desiredNs, actualNs, desired.Tables, actualTables, desired.Indexes, actualIndexes, nil)
	if len(disallowed) > 0 {
		t.Fatalf("%s: Diff produced disallowed changes: %v", label, disallowed)
	}

	if err := schemify.Apply(ctx, pool, migrations, schemify.ApplyOptions{}); err != nil {
		t.Fatalf("%s: Apply: %v", label, err)
	}
	return migrations
}

func itestFilterTables(all map[string]*schema.Table, prefix string) map[string]*schema.Table {
	out := make(map[string]*schema.Table, len(all))
	for k, v := range all {
		if strings.HasPrefix(v.Name, prefix) {
			out[k] = v
		}
	}
	return out
}

// itestFilterNamespaces limits namespace diff to the schema under test so unrelated
// namespaces in a shared database do not produce drop_schema drift.
func itestFilterNamespaces(t *testing.T, ctx context.Context, pool *pgxpool.Pool, introspectSchema string) map[string]struct{} {
	t.Helper()
	all, err := idb.ListUserSchemas(ctx, pool)
	if err != nil {
		t.Fatalf("ListUserSchemas: %v", err)
	}
	out := make(map[string]struct{})
	if _, ok := all[introspectSchema]; ok {
		out[introspectSchema] = struct{}{}
	}
	return out
}

func itestFilterIndexes(all map[string]*schema.Index, prefix string) map[string]*schema.Index {
	out := make(map[string]*schema.Index, len(all))
	for k, v := range all {
		if strings.HasPrefix(v.Name, prefix) {
			out[k] = v
		}
	}
	return out
}

// itestCleanup drops any test tables left over from a previous run.
func itestCleanup(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, tbl := range []string{
		"public.schemify_itest_items",
		"public.schemify_itest_records",
		"public.schemify_itest_fk_child",
		"public.schemify_itest_fk_parent",
		"public.schemify_itest_channels",
	} {
		if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+tbl); err != nil {
			t.Logf("cleanup warning: %v", err)
		}
	}
}

func itestCleanupNamespace(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+itestNSSchema+".schemify_itest_ns_table CASCADE"); err != nil {
		t.Logf("cleanup namespace table: %v", err)
	}
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+itestNSSchema+" CASCADE"); err != nil {
		t.Logf("cleanup namespace schema: %v", err)
	}
}

// itestDriftBaselineSQL creates a table with a legacy_code column and an index on it.
const itestDriftBaselineSQL = `
CREATE TABLE public.schemify_itest_drift_things (
    id BIGSERIAL,
    name TEXT NOT NULL,
    legacy_code TEXT NOT NULL
);

CREATE INDEX CONCURRENTLY idx_schemify_itest_drift_things_legacy ON public.schemify_itest_drift_things (legacy_code);
`

// itestDriftDropSQL removes legacy_code and its index via DRIFT DROP blocks.
const itestDriftDropSQL = `
CREATE TABLE public.schemify_itest_drift_things (
    id BIGSERIAL,
    name TEXT NOT NULL
    -- DRIFT cleanup1 DROP (
    --   legacy_code TEXT NOT NULL
    -- )
);

-- DRIFT cleanup1 DROP (
-- CREATE INDEX CONCURRENTLY idx_schemify_itest_drift_things_legacy ON public.schemify_itest_drift_things (legacy_code);
-- )
`

// itestDriftDeprecatedSQL removes legacy_code from desired but marks it DEPRECATED (tolerated).
// No index is desired so the surplus index is handled by a DROP group.
const itestDriftDeprecatedSQL = `
CREATE TABLE public.schemify_itest_drift_things (
    id BIGSERIAL,
    name TEXT NOT NULL
    -- DRIFT tolerate1 DEPRECATED (
    --   legacy_code TEXT NOT NULL
    -- )
);

-- DRIFT cleanup1 DROP (
-- CREATE INDEX CONCURRENTLY idx_schemify_itest_drift_things_legacy ON public.schemify_itest_drift_things (legacy_code);
-- )
`

func itestCleanupDrift(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS public.schemify_itest_drift_things CASCADE"); err != nil {
		t.Logf("cleanup drift: %v", err)
	}
}

func itestApplyDecorated(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, label string) []schemify.Migration {
	t.Helper()
	decorated, err := schemify.LoadDecoratedSchema(fsys)
	if err != nil {
		t.Fatalf("%s: LoadDecoratedSchema: %v", label, err)
	}
	actual, err := schemify.Introspect(ctx, pool, "public")
	if err != nil {
		t.Fatalf("%s: Introspect: %v", label, err)
	}
	actualTables := itestFilterTables(actual.Tables, "schemify_itest_drift_")
	actualIndexes := itestFilterIndexes(actual.Indexes, "schemify_itest_drift_")
	desiredNs := schema.CollectDesiredNamespaces(&decorated.LoadResult)
	actualNs := itestFilterNamespaces(t, ctx, pool, "public")

	migrations, disallowed := schemify.DiffWithDrift(
		desiredNs, actualNs,
		decorated.Tables, actualTables,
		decorated.Indexes, actualIndexes,
		nil,
		decorated.DriftGroups,
	)
	if len(disallowed) > 0 {
		t.Fatalf("%s: Diff produced disallowed changes: %v", label, disallowed)
	}
	if err := schemify.Apply(ctx, pool, migrations, schemify.ApplyOptions{}); err != nil {
		t.Fatalf("%s: Apply: %v", label, err)
	}
	return migrations
}

// TestIntegration_DriftDrop verifies that surplus columns and indexes covered by a
// DRIFT DROP block are planned as allowed drop migrations rather than disallowed changes.
func TestIntegration_DriftDrop(t *testing.T) {
	ctx := context.Background()
	pool := itestPool(t, ctx)
	t.Cleanup(func() { pool.Close() })
	itestCleanupDrift(t, ctx, pool)
	t.Cleanup(func() { itestCleanupDrift(t, ctx, pool) })

	baselineFS := fstest.MapFS{"schema.sql": &fstest.MapFile{Data: []byte(itestDriftBaselineSQL)}}
	dropFS := fstest.MapFS{"schema.sql": &fstest.MapFile{Data: []byte(itestDriftDropSQL)}}

	run1 := itestApplyDecorated(t, ctx, pool, baselineFS, "baseline")
	if len(run1) == 0 {
		t.Fatal("baseline: expected at least one migration, got none")
	}

	run2 := itestApplyDecorated(t, ctx, pool, dropFS, "drift-drop")
	var sawDropColumn, sawDropIndex bool
	for _, m := range run2 {
		if m.Kind == "drop_column" && m.Table == "schemify_itest_drift_things" {
			sawDropColumn = true
		}
		if m.Kind == "drop_index" {
			sawDropIndex = true
		}
	}
	if !sawDropColumn {
		t.Errorf("drift-drop: expected drop_column migration for legacy_code, got %v", run2)
	}
	if !sawDropIndex {
		t.Errorf("drift-drop: expected drop_index migration for legacy index, got %v", run2)
	}

	// Third run must be idempotent.
	run3 := itestApplyDecorated(t, ctx, pool, dropFS, "idempotent")
	if len(run3) != 0 {
		t.Errorf("idempotent run: expected no migrations, got %v", run3)
	}
}

// TestIntegration_DriftDeprecated verifies that surplus columns covered by a
// DRIFT DEPRECATED block produce neither migrations nor disallowed changes.
func TestIntegration_DriftDeprecated(t *testing.T) {
	ctx := context.Background()
	pool := itestPool(t, ctx)
	t.Cleanup(func() { pool.Close() })
	itestCleanupDrift(t, ctx, pool)
	t.Cleanup(func() { itestCleanupDrift(t, ctx, pool) })

	baselineFS := fstest.MapFS{"schema.sql": &fstest.MapFile{Data: []byte(itestDriftBaselineSQL)}}
	deprecatedFS := fstest.MapFS{"schema.sql": &fstest.MapFile{Data: []byte(itestDriftDeprecatedSQL)}}

	itestApplyDecorated(t, ctx, pool, baselineFS, "baseline")

	// Now diff the deprecated schema. The column is surplus but DEPRECATED → tolerated.
	// The index is surplus and covered by a DROP group → drop migration.
	decorated, err := schemify.LoadDecoratedSchema(deprecatedFS)
	if err != nil {
		t.Fatalf("LoadDecoratedSchema: %v", err)
	}
	actual, err := schemify.Introspect(ctx, pool, "public")
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	actualTables := itestFilterTables(actual.Tables, "schemify_itest_drift_")
	actualIndexes := itestFilterIndexes(actual.Indexes, "schemify_itest_drift_")
	desiredNs := schema.CollectDesiredNamespaces(&decorated.LoadResult)
	actualNs := itestFilterNamespaces(t, ctx, pool, "public")

	migrations, disallowed := schemify.DiffWithDrift(
		desiredNs, actualNs,
		decorated.Tables, actualTables,
		decorated.Indexes, actualIndexes,
		nil,
		decorated.DriftGroups,
	)
	if len(disallowed) > 0 {
		t.Errorf("DriftDeprecated: expected no disallowed changes, got %v", disallowed)
	}
	// Only the index drop should be a migration; no column drop.
	for _, m := range migrations {
		if m.Kind == "drop_column" {
			t.Errorf("DriftDeprecated: unexpected drop_column migration (column should be tolerated): %v", m)
		}
	}
}

// itestNotNullDefaultBaselineSQL creates a table with no constrained columns beyond the PK.
const itestNotNullDefaultBaselineSQL = `
CREATE TABLE public.schemify_itest_nnd_things (
    id text NOT NULL,
    PRIMARY KEY (id)
);
`

// itestNotNullDefaultAddedSQL adds a NOT NULL column with a DEFAULT, plus a
// plain nullable column with no DEFAULT to verify introspection reports
// Nullable=true for it (not just Nullable=false for the NOT NULL column).
const itestNotNullDefaultAddedSQL = `
CREATE TABLE public.schemify_itest_nnd_things (
    id text NOT NULL,
    status text NOT NULL DEFAULT 'init',
    note text,
    PRIMARY KEY (id)
);
`

// itestNotNullNoDefaultAddedSQL adds a NOT NULL column with no DEFAULT, which cannot
// be applied safely to a table that may already have rows.
const itestNotNullNoDefaultAddedSQL = `
CREATE TABLE public.schemify_itest_nnd_things (
    id text NOT NULL,
    status text NOT NULL,
    PRIMARY KEY (id)
);
`

func itestCleanupNotNullDefault(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS public.schemify_itest_nnd_things CASCADE"); err != nil {
		t.Logf("cleanup nnd: %v", err)
	}
}

// TestIntegration_AddColumn_NotNullDefault verifies that adding a NOT NULL column with
// a DEFAULT to a table with existing rows applies both constraints at the database
// level and backfills existing rows to the default.
func TestIntegration_AddColumn_NotNullDefault(t *testing.T) {
	ctx := context.Background()
	pool := itestPool(t, ctx)
	t.Cleanup(func() { pool.Close() })
	itestCleanupNotNullDefault(t, ctx, pool)
	t.Cleanup(func() { itestCleanupNotNullDefault(t, ctx, pool) })

	baselineFS := fstest.MapFS{"schema.sql": &fstest.MapFile{Data: []byte(itestNotNullDefaultBaselineSQL)}}
	addedFS := fstest.MapFS{"schema.sql": &fstest.MapFile{Data: []byte(itestNotNullDefaultAddedSQL)}}

	itestApply(t, ctx, pool, baselineFS, "baseline")

	if _, err := pool.Exec(ctx, "INSERT INTO public.schemify_itest_nnd_things (id) VALUES ('row1'), ('row2')"); err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	run2 := itestApply(t, ctx, pool, addedFS, "add column")
	var sawAddColumn bool
	for _, m := range run2 {
		if m.Kind == "add_column" && m.Table == "schemify_itest_nnd_things" {
			sawAddColumn = true
		}
	}
	if !sawAddColumn {
		t.Fatalf("expected add_column migration, got %v", run2)
	}

	var isNullable, columnDefault string
	if err := pool.QueryRow(ctx,
		"SELECT is_nullable, column_default FROM information_schema.columns WHERE table_schema='public' AND table_name='schemify_itest_nnd_things' AND column_name='status'",
	).Scan(&isNullable, &columnDefault); err != nil {
		t.Fatalf("query information_schema: %v", err)
	}
	if isNullable != "NO" {
		t.Errorf("expected is_nullable=NO, got %s", isNullable)
	}
	if !strings.Contains(columnDefault, "init") {
		t.Errorf("expected column_default to reference 'init', got %s", columnDefault)
	}

	actual, err := schemify.Introspect(ctx, pool, "public")
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	tbl, ok := actual.Tables["public.schemify_itest_nnd_things"]
	if !ok {
		t.Fatalf("expected introspected table public.schemify_itest_nnd_things, got %v", actual.Tables)
	}
	var statusCol *schema.Column
	for i := range tbl.Columns {
		if tbl.Columns[i].Name == "status" {
			statusCol = &tbl.Columns[i]
		}
	}
	if statusCol == nil {
		t.Fatalf("expected introspected status column, got %v", tbl.Columns)
	}
	if statusCol.Nullable {
		t.Errorf("expected introspected status column to be Nullable=false, got true")
	}
	if !strings.Contains(statusCol.Default, "init") {
		t.Errorf("expected introspected status column Default to reference 'init', got %q", statusCol.Default)
	}
	var noteCol *schema.Column
	for i := range tbl.Columns {
		if tbl.Columns[i].Name == "note" {
			noteCol = &tbl.Columns[i]
		}
	}
	if noteCol == nil {
		t.Fatalf("expected introspected note column, got %v", tbl.Columns)
	}
	if !noteCol.Nullable {
		t.Errorf("expected introspected note column to be Nullable=true, got false")
	}

	rows, err := pool.Query(ctx, "SELECT status FROM public.schemify_itest_nnd_things ORDER BY id")
	if err != nil {
		t.Fatalf("query rows: %v", err)
	}
	defer rows.Close()
	var statuses []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		statuses = append(statuses, s)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(statuses))
	}
	for _, s := range statuses {
		if s != "init" {
			t.Errorf("expected existing row backfilled to 'init', got %q", s)
		}
	}

	// Idempotency: applying again must produce no migrations.
	run3 := itestApply(t, ctx, pool, addedFS, "idempotent")
	if len(run3) != 0 {
		t.Errorf("idempotent run: expected no migrations, got %v", run3)
	}
}

// TestIntegration_AddColumn_NotNullNoDefault_Disallowed verifies that adding a NOT
// NULL column with no DEFAULT to an existing table is rejected at plan time instead
// of producing SQL that PostgreSQL would refuse to run on a populated table.
func TestIntegration_AddColumn_NotNullNoDefault_Disallowed(t *testing.T) {
	ctx := context.Background()
	pool := itestPool(t, ctx)
	t.Cleanup(func() { pool.Close() })
	itestCleanupNotNullDefault(t, ctx, pool)
	t.Cleanup(func() { itestCleanupNotNullDefault(t, ctx, pool) })

	baselineFS := fstest.MapFS{"schema.sql": &fstest.MapFile{Data: []byte(itestNotNullDefaultBaselineSQL)}}
	noDefaultFS := fstest.MapFS{"schema.sql": &fstest.MapFile{Data: []byte(itestNotNullNoDefaultAddedSQL)}}

	itestApply(t, ctx, pool, baselineFS, "baseline")

	desired, err := schemify.LoadSchema(noDefaultFS)
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	actual, err := schemify.Introspect(ctx, pool, "public")
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	actualTables := itestFilterTables(actual.Tables, itestPrefix)
	actualIndexes := itestFilterIndexes(actual.Indexes, itestPrefix)
	desiredNs := schema.CollectDesiredNamespaces(desired)
	actualNs := itestFilterNamespaces(t, ctx, pool, "public")

	_, disallowed := schemify.Diff(desiredNs, actualNs, desired.Tables, actualTables, desired.Indexes, actualIndexes, nil)
	var saw bool
	for _, d := range disallowed {
		if d.Kind == "add_column_not_null_no_default" && d.Column == "status" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("expected add_column_not_null_no_default disallowed change, got %v", disallowed)
	}
}

// itestPool connects to PostgreSQL using the same env vars as the CLI
// (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, DB_SSLMODE).
// The test is skipped rather than failed if the database is unreachable.
func itestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	cfg := idb.Config{
		Host:     itestEnvOrDefault("DB_HOST", "localhost"),
		Port:     itestEnvOrDefault("DB_PORT", "5432"),
		User:     ss.New(itestEnvOrDefault("DB_USER", "schemify")),
		Password: ss.New(itestEnvOrDefault("DB_PASSWORD", "schemify")),
		Database: itestEnvOrDefault("DB_NAME", "schemify"),
		SSLMode:  itestEnvOrDefault("DB_SSLMODE", "disable"),
	}
	pool, err := idb.Connect(ctx, &cfg)
	if err != nil {
		t.Skipf("postgres not available, skipping integration test: %v", err)
	}
	return pool
}

func itestEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
