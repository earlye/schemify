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
