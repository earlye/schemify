//! Live PostgreSQL tests (ported from `go/schemify/schemify_integration_test.go`).
//! Skips if `DB_HOST` / default localhost:5432 is unreachable.

use schemify::apply::{ApplyOptions, apply};
use schemify::collect_desired_namespaces;
use schemify::db::{DatabaseConfig, connect, introspect, list_user_schemas};
use schemify::diff::diff_tables_and_indexes;
use schemify::load::load_from_dir;
use schemify::schema::{Index, Table};
use std::collections::HashMap;
use std::fs;
use std::path::Path;
use tempfile::tempdir;

const ITEST_PREFIX: &str = "schemify_itest_";

const ITEST_SCHEMA_SQL: &str = r#"
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
"#;

/// Dedicated PostgreSQL schema for non-`public` introspection tests.
const ITEST_NS_SCHEMA: &str = "schemify_itest_ns";

/// Minimal fixture with a `CREATE SCHEMA` preamble; the schema must exist before apply.
const ITEST_NS_SCHEMA_SQL: &str = r#"
CREATE SCHEMA IF NOT EXISTS schemify_itest_ns;

CREATE TABLE schemify_itest_ns.schemify_itest_ns_table (
    id text NOT NULL,
    PRIMARY KEY (id)
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS schemify_itest_idx_ns_table_id
    ON schemify_itest_ns.schemify_itest_ns_table (id);
"#;

/// Baseline table with no constrained columns beyond the PK.
const ITEST_NND_BASELINE_SQL: &str = r#"
CREATE TABLE public.schemify_itest_nnd_things (
    id text NOT NULL,
    PRIMARY KEY (id)
);
"#;

/// Adds a NOT NULL column with a DEFAULT, plus a plain nullable column with no
/// DEFAULT to verify introspection reports nullable=true for it (not just
/// nullable=false for the NOT NULL column).
const ITEST_NND_ADDED_SQL: &str = r#"
CREATE TABLE public.schemify_itest_nnd_things (
    id text NOT NULL,
    status text NOT NULL DEFAULT 'init',
    note text,
    PRIMARY KEY (id)
);
"#;

/// Adds a NOT NULL column with no DEFAULT, which cannot be applied safely to a
/// table that may already have rows.
const ITEST_NND_NO_DEFAULT_SQL: &str = r#"
CREATE TABLE public.schemify_itest_nnd_things (
    id text NOT NULL,
    status text NOT NULL,
    PRIMARY KEY (id)
);
"#;

async fn itest_cleanup_nnd(client: &tokio_postgres::Client) {
    let _ = client
        .execute(
            "DROP TABLE IF EXISTS public.schemify_itest_nnd_things CASCADE",
            &[],
        )
        .await;
}

fn env_or(key: &str, def: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| def.into())
}

async fn itest_client() -> Option<schemify::DbConnection> {
    let cfg = DatabaseConfig {
        host: env_or("DB_HOST", "localhost"),
        port: env_or("DB_PORT", "5432"),
        user: env_or("DB_USER", "schemify"),
        password: env_or("DB_PASSWORD", "schemify"),
        database: env_or("DB_NAME", "schemify"),
        ssl_mode: env_or("DB_SSLMODE", "disable"),
        ..Default::default()
    };
    connect(&cfg).await.ok()
}

fn filter_tables(all: &HashMap<String, Table>, prefix: &str) -> HashMap<String, Table> {
    all.iter()
        .filter(|(_, v)| v.name.starts_with(prefix))
        .map(|(k, v)| (k.clone(), v.clone()))
        .collect()
}

fn filter_indexes(all: &HashMap<String, Index>, prefix: &str) -> HashMap<String, Index> {
    all.iter()
        .filter(|(_, v)| v.name.starts_with(prefix))
        .map(|(k, v)| (k.clone(), v.clone()))
        .collect()
}

async fn itest_cleanup(client: &tokio_postgres::Client) {
    for tbl in [
        "public.schemify_itest_items",
        "public.schemify_itest_records",
        "public.schemify_itest_fk_child",
        "public.schemify_itest_fk_parent",
        "public.schemify_itest_channels",
    ] {
        let _ = client
            .execute(&format!("DROP TABLE IF EXISTS {tbl} CASCADE"), &[])
            .await;
    }
}

async fn filter_namespaces(
    client: &tokio_postgres::Client,
    introspect_schema: &str,
) -> std::collections::HashSet<String> {
    let all = list_user_schemas(client).await.unwrap_or_default();
    if all.contains(introspect_schema) {
        [introspect_schema.into()].into_iter().collect()
    } else {
        std::collections::HashSet::new()
    }
}

async fn itest_cleanup_namespace(client: &tokio_postgres::Client) {
    let _ = client
        .execute(
            "DROP TABLE IF EXISTS schemify_itest_ns.schemify_itest_ns_table CASCADE",
            &[],
        )
        .await;
    let _ = client
        .execute("DROP SCHEMA IF EXISTS schemify_itest_ns CASCADE", &[])
        .await;
}

async fn itest_apply(
    client: &mut tokio_postgres::Client,
    dir: &Path,
    label: &str,
    introspect_schema: &str,
) -> Vec<schemify::diff::Migration> {
    let desired = load_from_dir(dir).expect(label);
    let actual = introspect(client, introspect_schema).await.expect(label);

    let actual_tables = filter_tables(&actual.tables, ITEST_PREFIX);
    let actual_indexes = filter_indexes(&actual.indexes, ITEST_PREFIX);
    let desired_ns = collect_desired_namespaces(&desired);
    let actual_ns = filter_namespaces(&client, introspect_schema).await;

    let (migrations, disallowed) = diff_tables_and_indexes(
        &desired_ns,
        &actual_ns,
        &desired.tables,
        &actual_tables,
        Some(&desired.indexes),
        Some(&actual_indexes),
        None,
        None,
    );
    assert!(disallowed.is_empty(), "{label}: disallowed: {disallowed:?}");
    apply(client, &migrations, &ApplyOptions::default())
        .await
        .expect(label);
    migrations
}

#[tokio::test]
async fn integration_index_idempotency() {
    let Some(mut client) = itest_client().await else {
        eprintln!("skip integration_index_idempotency: postgres not reachable");
        return;
    };

    itest_cleanup(&client).await;

    let dir = tempdir().unwrap();
    fs::write(dir.path().join("schema.sql"), ITEST_SCHEMA_SQL).unwrap();

    let run1 = itest_apply(&mut client, dir.path(), "first run", "public").await;
    assert!(!run1.is_empty(), "first run: expected migrations");

    let run2 = itest_apply(&mut client, dir.path(), "second run", "public").await;
    assert!(
        run2.is_empty(),
        "second run: expected idempotency, got {run2:?}"
    );

    itest_cleanup(&client).await;
}

#[tokio::test]
async fn integration_non_public_schema_idempotency() {
    let Some(mut client) = itest_client().await else {
        eprintln!("skip integration_non_public_schema_idempotency: postgres not reachable");
        return;
    };

    itest_cleanup_namespace(&client).await;

    let dir = tempdir().unwrap();
    fs::write(dir.path().join("schema.sql"), ITEST_NS_SCHEMA_SQL).unwrap();

    let run1 = itest_apply(&mut client, dir.path(), "first run", ITEST_NS_SCHEMA).await;
    assert!(!run1.is_empty(), "first run: expected migrations");

    let run2 = itest_apply(&mut client, dir.path(), "second run", ITEST_NS_SCHEMA).await;
    assert!(
        run2.is_empty(),
        "second run: expected idempotency, got {run2:?}"
    );

    itest_cleanup_namespace(&client).await;
}

#[tokio::test]
async fn integration_extra_constraints_destructive() {
    let Some(mut client) = itest_client().await else {
        eprintln!("skip integration_extra_constraints_destructive: postgres not reachable");
        return;
    };

    itest_cleanup(&client).await;

    let dir = tempdir().unwrap();
    fs::write(dir.path().join("schema.sql"), ITEST_SCHEMA_SQL).unwrap();

    itest_apply(&mut client, dir.path(), "baseline", "public").await;

    client
        .execute(
            "ALTER TABLE public.schemify_itest_fk_parent ADD CONSTRAINT schemify_itest_fk_parent_payload_unique UNIQUE (payload)",
            &[],
        )
        .await
        .expect("extra unique");
    client
        .execute(
            "ALTER TABLE public.schemify_itest_fk_child ADD CONSTRAINT schemify_itest_fk_child_child_id_extra_fk FOREIGN KEY (child_id) REFERENCES public.schemify_itest_fk_parent (code)",
            &[],
        )
        .await
        .expect("extra fk");

    let desired = load_from_dir(dir.path()).unwrap();
    let actual = introspect(&client, "public").await.unwrap();
    let actual_tables = filter_tables(&actual.tables, ITEST_PREFIX);
    let actual_indexes = filter_indexes(&actual.indexes, ITEST_PREFIX);
    let desired_ns = collect_desired_namespaces(&desired);
    let actual_ns = filter_namespaces(&client, "public").await;

    let (_, disallowed) = diff_tables_and_indexes(
        &desired_ns,
        &actual_ns,
        &desired.tables,
        &actual_tables,
        Some(&desired.indexes),
        Some(&actual_indexes),
        None,
        None,
    );

    assert!(!disallowed.is_empty(), "expected destructive drift");
    let mut saw_u = false;
    let mut saw_fk = false;
    for d in &disallowed {
        if d.kind == "drop_unique_key" && d.name == "schemify_itest_fk_parent_payload_unique" {
            saw_u = true;
        }
        if d.kind == "drop_foreign_key" && d.name == "schemify_itest_fk_child_child_id_extra_fk" {
            saw_fk = true;
        }
    }
    assert!(saw_u && saw_fk, "disallowed={disallowed:?}");

    itest_cleanup(&client).await;
}

#[tokio::test]
async fn integration_add_column_not_null_default() {
    let Some(mut client) = itest_client().await else {
        eprintln!("skip integration_add_column_not_null_default: postgres not reachable");
        return;
    };

    itest_cleanup_nnd(&client).await;

    let baseline_dir = tempdir().unwrap();
    fs::write(baseline_dir.path().join("schema.sql"), ITEST_NND_BASELINE_SQL).unwrap();
    let added_dir = tempdir().unwrap();
    fs::write(added_dir.path().join("schema.sql"), ITEST_NND_ADDED_SQL).unwrap();

    itest_apply(&mut client, baseline_dir.path(), "baseline", "public").await;

    client
        .execute(
            "INSERT INTO public.schemify_itest_nnd_things (id) VALUES ('row1'), ('row2')",
            &[],
        )
        .await
        .expect("seed rows");

    let run2 = itest_apply(&mut client, added_dir.path(), "add column", "public").await;
    assert!(
        run2.iter()
            .any(|m| m.kind == "add_column" && m.table == "schemify_itest_nnd_things"),
        "expected add_column migration, got {run2:?}"
    );

    let row = client
        .query_one(
            "SELECT is_nullable, column_default FROM information_schema.columns WHERE table_schema='public' AND table_name='schemify_itest_nnd_things' AND column_name='status'",
            &[],
        )
        .await
        .expect("query information_schema");
    let is_nullable: String = row.get(0);
    let column_default: String = row.get(1);
    assert_eq!(is_nullable, "NO");
    assert!(
        column_default.contains("init"),
        "expected column_default to reference 'init', got {column_default}"
    );

    let actual = introspect(&client, "public").await.expect("Introspect");
    let tbl = actual
        .tables
        .get("public.schemify_itest_nnd_things")
        .expect("expected introspected table public.schemify_itest_nnd_things");
    let status_col = tbl
        .columns
        .iter()
        .find(|c| c.name == "status")
        .expect("expected introspected status column");
    assert!(
        !status_col.nullable,
        "expected introspected status column to be nullable=false, got true"
    );
    assert!(
        status_col.default.contains("init"),
        "expected introspected status column default to reference 'init', got {}",
        status_col.default
    );
    let note_col = tbl
        .columns
        .iter()
        .find(|c| c.name == "note")
        .expect("expected introspected note column");
    assert!(
        note_col.nullable,
        "expected introspected note column to be nullable=true, got false"
    );

    let rows = client
        .query(
            "SELECT status FROM public.schemify_itest_nnd_things ORDER BY id",
            &[],
        )
        .await
        .expect("query rows");
    assert_eq!(rows.len(), 2);
    for row in &rows {
        let status: String = row.get(0);
        assert_eq!(status, "init");
    }

    let run3 = itest_apply(&mut client, added_dir.path(), "idempotent", "public").await;
    assert!(run3.is_empty(), "idempotent run: expected no migrations, got {run3:?}");

    itest_cleanup_nnd(&client).await;
}

#[tokio::test]
async fn integration_add_column_not_null_no_default_disallowed() {
    let Some(mut client) = itest_client().await else {
        eprintln!("skip integration_add_column_not_null_no_default_disallowed: postgres not reachable");
        return;
    };

    itest_cleanup_nnd(&client).await;

    let baseline_dir = tempdir().unwrap();
    fs::write(baseline_dir.path().join("schema.sql"), ITEST_NND_BASELINE_SQL).unwrap();
    let no_default_dir = tempdir().unwrap();
    fs::write(
        no_default_dir.path().join("schema.sql"),
        ITEST_NND_NO_DEFAULT_SQL,
    )
    .unwrap();

    itest_apply(&mut client, baseline_dir.path(), "baseline", "public").await;

    let desired = load_from_dir(no_default_dir.path()).unwrap();
    let actual = introspect(&client, "public").await.unwrap();
    let actual_tables = filter_tables(&actual.tables, ITEST_PREFIX);
    let actual_indexes = filter_indexes(&actual.indexes, ITEST_PREFIX);
    let desired_ns = collect_desired_namespaces(&desired);
    let actual_ns = filter_namespaces(&client, "public").await;

    let (_, disallowed) = diff_tables_and_indexes(
        &desired_ns,
        &actual_ns,
        &desired.tables,
        &actual_tables,
        Some(&desired.indexes),
        Some(&actual_indexes),
        None,
        None,
    );

    assert!(
        disallowed
            .iter()
            .any(|d| d.kind == "add_column_not_null_no_default" && d.column == "status"),
        "expected add_column_not_null_no_default disallowed change, got {disallowed:?}"
    );

    itest_cleanup_nnd(&client).await;
}
