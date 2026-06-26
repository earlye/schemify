//! Declarative PostgreSQL schema application (Rust port of `go/schemify`).
//!
//! Loads `*.sql` files, introspects the database, diffs, and applies additive changes only.

pub mod apply;
pub mod db;
pub mod diff;
pub mod drift;
pub mod error;
pub mod helpers;
pub mod load;
pub mod namespace;
pub mod schema;

pub use apply::{ApplyOptions, apply, migration_sql};
pub use db::{
    DatabaseConfig, IntrospectResult, connect, introspect, introspect_all, list_user_schemas,
};
pub use diff::{DestructiveChange, Migration, MigrationDetail, diff_tables_and_indexes};
pub use drift::merge_drift_groups;
pub use error::{Error, Result};
pub use load::{
    LoadResult, extract_create_schemas, extract_drop_table_block_defs, extract_removed_directives,
    load_allow_drop_table_defs, load_from_dir, parse_ddl,
};
pub use namespace::{
    collect_desired_namespaces, is_drop_schema_candidate, is_system_namespace, union_namespaces,
};
pub use schema::{
    DecoratedLoadResult, DecoratedTable, DriftBlock, DriftGroup, DriftPolicy, DriftScope,
};

use db::connect as db_connect;
use std::path::PathBuf;
use tokio_postgres::Client;

/// Database connection + schema directory + apply flags (mirrors Go `Options`).
#[derive(Debug, Clone)]
pub struct Options {
    pub schema_dir: PathBuf,
    pub database: DatabaseConfig,
    pub apply_options: ApplyOptions,
}

/// Load desired schema from a directory of `.sql` files.
pub fn load_schema(dir: impl Into<PathBuf>) -> Result<LoadResult> {
    load_from_dir(dir.into())
}

/// Load desired schema with drift block information from a directory of `.sql` files.
pub fn load_decorated_schema(dir: &std::path::Path) -> Result<DecoratedLoadResult> {
    load::load_decorated_from_dir(dir)
}

/// Compare desired vs actual with drift group support.
#[allow(clippy::too_many_arguments)]
pub fn diff_with_drift(
    desired_namespaces: &std::collections::HashSet<String>,
    actual_namespaces: &std::collections::HashSet<String>,
    desired: &std::collections::HashMap<String, schema::Table>,
    actual: &std::collections::HashMap<String, schema::Table>,
    desired_indexes: Option<&std::collections::HashMap<String, schema::Index>>,
    actual_indexes: Option<&std::collections::HashMap<String, schema::Index>>,
    allow_drop_defs: Option<&std::collections::HashMap<String, schema::Table>>,
    drift_groups: &std::collections::HashMap<String, schema::DriftGroup>,
) -> (Vec<diff::Migration>, Vec<diff::DestructiveChange>) {
    diff::diff_tables_and_indexes(
        desired_namespaces,
        actual_namespaces,
        desired,
        actual,
        desired_indexes,
        actual_indexes,
        allow_drop_defs,
        Some(drift_groups),
    )
}

/// Compare desired vs actual and return migrations, or error if destructive drift exists.
pub async fn plan(client: &Client, cfg: &Options) -> Result<Vec<Migration>> {
    let decorated = load::load_decorated_from_dir(&cfg.schema_dir)?;
    let allow_drop_table_defs = load_allow_drop_table_defs(&cfg.schema_dir)?;

    // Build a LoadResult-compatible view from decorated result.
    let load_result = LoadResult {
        schemas: decorated.schemas.clone(),
        tables: decorated.tables.clone(),
        indexes: decorated.indexes.clone(),
    };

    let desired_namespaces = collect_desired_namespaces(&load_result);
    let actual_namespaces = list_user_schemas(client).await?;
    let namespaces = union_namespaces(&desired_namespaces, &actual_namespaces);
    let namespace_list: Vec<String> = namespaces;
    let intro = introspect_all(client, &namespace_list).await?;

    let (migrations, disallowed) = diff_tables_and_indexes(
        &desired_namespaces,
        &actual_namespaces,
        &decorated.tables,
        &intro.tables,
        Some(&decorated.indexes),
        Some(&intro.indexes),
        Some(&allow_drop_table_defs),
        Some(&decorated.drift_groups),
    );

    if !disallowed.is_empty() {
        let mut lines = vec!["destructive changes are not allowed:".to_string()];
        for d in &disallowed {
            lines.push(format!("  - {}", d.to_message_line()));
        }
        return Err(Error::Destructive(lines.join("\n")));
    }

    Ok(migrations)
}

/// Connect, plan, and apply (or dry-run).
pub async fn run(cfg: &Options) -> Result<()> {
    let mut client = db_connect(&cfg.database).await?;
    let migrations = plan(&client, cfg).await?;
    apply(&mut client, &migrations, &cfg.apply_options).await
}
