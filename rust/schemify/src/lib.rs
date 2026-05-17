//! Declarative PostgreSQL schema application (Rust port of `go/schemify`).
//!
//! Loads `*.sql` files, introspects the database, diffs, and applies additive changes only.

pub mod apply;
pub mod db;
pub mod diff;
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
pub use error::{Error, Result};
pub use load::{
    LoadResult, extract_create_schemas, extract_drop_table_block_defs, extract_removed_directives,
    load_allow_drop_table_defs, load_from_dir, parse_ddl,
};
pub use namespace::{
    collect_desired_namespaces, is_drop_schema_candidate, is_system_namespace, union_namespaces,
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

/// Compare desired vs actual and return migrations, or error if destructive drift exists.
pub async fn plan(client: &Client, cfg: &Options) -> Result<Vec<Migration>> {
    let load_result = load_from_dir(&cfg.schema_dir)?;
    let allow_drop_table_defs = load_allow_drop_table_defs(&cfg.schema_dir)?;
    let desired_namespaces = collect_desired_namespaces(&load_result);
    let actual_namespaces = list_user_schemas(client).await?;
    let namespaces = union_namespaces(&desired_namespaces, &actual_namespaces);
    let namespace_list: Vec<String> = namespaces;
    let intro = introspect_all(client, &namespace_list).await?;

    let (migrations, disallowed) = diff_tables_and_indexes(
        &desired_namespaces,
        &actual_namespaces,
        &load_result.tables,
        &intro.tables,
        Some(&load_result.indexes),
        Some(&intro.indexes),
        Some(&allow_drop_table_defs),
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
