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

// itestApply loads the given schema, introspects the DB (filtered to itest
// tables/indexes only), diffs, applies, and returns the applied migrations.
func itestApply(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, label string) []schemify.Migration {
	t.Helper()

	desired, err := schemify.LoadSchema(fsys)
	if err != nil {
		t.Fatalf("%s: LoadSchema: %v", label, err)
	}

	actual, err := schemify.Introspect(ctx, pool, "public")
	if err != nil {
		t.Fatalf("%s: Introspect: %v", label, err)
	}

	// Narrow the actual DB snapshot to only test-owned objects so that other
	// tables in the database do not trigger spurious destructive-change errors.
	actualTables := itestFilterTables(actual.Tables, itestPrefix)
	actualIndexes := itestFilterIndexes(actual.Indexes, itestPrefix)

	migrations, disallowed := schemify.Diff(desired.Tables, actualTables, desired.Indexes, actualIndexes, nil)
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
		"public.schemify_itest_channels",
	} {
		if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+tbl); err != nil {
			t.Logf("cleanup warning: %v", err)
		}
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
