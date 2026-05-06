//! Live PostgreSQL tests (ported from `go/schemify/schemify_integration_test.go`).
//! Skips if `DB_HOST` / default localhost:5432 is unreachable.

use schemify::apply::{ApplyOptions, apply};
use schemify::db::{DatabaseConfig, connect, introspect};
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

fn env_or(key: &str, def: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| def.into())
}

async fn itest_client() -> Option<tokio_postgres::Client> {
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

async fn itest_apply(
    client: &mut tokio_postgres::Client,
    dir: &Path,
    label: &str,
) -> Vec<schemify::diff::Migration> {
    let desired = load_from_dir(dir).expect(label);
    let actual = introspect(client, "public").await.expect(label);

    let actual_tables = filter_tables(&actual.tables, ITEST_PREFIX);
    let actual_indexes = filter_indexes(&actual.indexes, ITEST_PREFIX);

    let (migrations, disallowed) = diff_tables_and_indexes(
        &desired.tables,
        &actual_tables,
        Some(&desired.indexes),
        Some(&actual_indexes),
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

    let run1 = itest_apply(&mut client, dir.path(), "first run").await;
    assert!(!run1.is_empty(), "first run: expected migrations");

    let run2 = itest_apply(&mut client, dir.path(), "second run").await;
    assert!(
        run2.is_empty(),
        "second run: expected idempotency, got {run2:?}"
    );

    itest_cleanup(&client).await;
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

    itest_apply(&mut client, dir.path(), "baseline").await;

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

    let (_, disallowed) = diff_tables_and_indexes(
        &desired.tables,
        &actual_tables,
        Some(&desired.indexes),
        Some(&actual_indexes),
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
