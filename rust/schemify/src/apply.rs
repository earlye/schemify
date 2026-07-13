//! Emit SQL and execute migrations (ported from Go internal/apply).

use crate::diff::{KIND_CREATE_INDEX, KIND_DROP_INDEX, Migration, MigrationDetail};
use crate::error::{Error, Result};
use crate::schema::{Column, ForeignKey, Index, PrimaryKeyConstraint, Table, UniqueConstraint};
use tracing::debug;

#[derive(Debug, Clone, Default)]
pub struct ApplyOptions {
    pub dry_run: bool,
}

pub async fn apply(
    client: &mut tokio_postgres::Client,
    migrations: &[Migration],
    opts: &ApplyOptions,
) -> Result<()> {
    for m in migrations {
        let sql = migration_sql(m)?;
        debug!(sql = %sql, "plan");
    }
    if opts.dry_run {
        return Ok(());
    }

    let mut in_tx: Vec<&Migration> = Vec::new();
    let mut out_tx: Vec<&Migration> = Vec::new();
    for m in migrations {
        if m.kind == KIND_CREATE_INDEX || m.kind == KIND_DROP_INDEX {
            out_tx.push(m);
        } else {
            in_tx.push(m);
        }
    }

    if !in_tx.is_empty() {
        let tx = client
            .transaction()
            .await
            .map_err(|e| Error::Apply(format!("begin transaction: {e}")))?;
        for m in &in_tx {
            let sql = migration_sql(m)?;
            debug!(sql = %sql, "running");
            tx.execute(sql.as_str(), &[])
                .await
                .map_err(|e| Error::Apply(format!("execute {sql}: {e}")))?;
        }
        tx.commit()
            .await
            .map_err(|e| Error::Apply(format!("commit: {e}")))?;
    }

    for m in out_tx {
        let sql = migration_sql(m)?;
        debug!(sql = %sql, "running");
        client
            .execute(sql.as_str(), &[])
            .await
            .map_err(|e| Error::Apply(format!("pool.Exec {sql}: {e}")))?;
    }

    Ok(())
}

pub fn migration_sql(m: &Migration) -> Result<String> {
    match &m.detail {
        MigrationDetail::CreateSchema => Ok(create_schema_sql(&m.schema)),
        MigrationDetail::CreateTable { table_def } => Ok(create_table_sql(table_def)),
        MigrationDetail::AddColumn { column } => Ok(add_column_sql(&m.schema, &m.table, column)),
        MigrationDetail::AlterColumn {
            old_column,
            new_column,
            ..
        } => Ok(alter_column_sql(&m.schema, &m.table, old_column, new_column)),
        MigrationDetail::DropColumn { column_name } => {
            Ok(drop_column_sql(&m.schema, &m.table, column_name))
        }
        MigrationDetail::DropTable => Ok(drop_table_sql(&m.schema, &m.table)),
        MigrationDetail::CreateIndex { index } => Ok(create_index_sql(index)),
        MigrationDetail::AddPrimaryKey { primary_key } => {
            Ok(add_pk_sql(&m.schema, &m.table, primary_key))
        }
        MigrationDetail::AddUniqueKey { unique_key } => {
            Ok(add_unique_sql(&m.schema, &m.table, unique_key))
        }
        MigrationDetail::AddForeignKey { foreign_key } => {
            Ok(add_fk_sql(&m.schema, &m.table, foreign_key))
        }
        MigrationDetail::DropUnique { constraint_name } => Ok(format!(
            "ALTER TABLE {}.{} DROP CONSTRAINT IF EXISTS {}",
            m.schema, m.table, constraint_name
        )),
        MigrationDetail::DropFk { constraint_name } => Ok(format!(
            "ALTER TABLE {}.{} DROP CONSTRAINT IF EXISTS {}",
            m.schema, m.table, constraint_name
        )),
        MigrationDetail::DropIndex { index } => Ok(format!(
            "DROP INDEX CONCURRENTLY IF EXISTS {}.{}",
            index.schema, index.name
        )),
    }
}

fn create_schema_sql(schema_name: &str) -> String {
    format!("CREATE SCHEMA IF NOT EXISTS {schema_name}")
}

fn create_table_sql(t: &Table) -> String {
    let mut parts: Vec<String> = Vec::new();
    for c in &t.columns {
        parts.push(column_def(c));
    }
    if let Some(pk) = &t.primary_key {
        if !pk.columns.is_empty() {
            parts.push(format!("PRIMARY KEY ({})", pk.columns.join(", ")));
        }
    }
    for u in &t.unique_keys {
        if u.name.is_empty() {
            panic!("internal invariant violation: unique constraint name is empty");
        }
        if !u.columns.is_empty() {
            let mut clause = format!("UNIQUE ({})", u.columns.join(", "));
            clause = format!("CONSTRAINT {} {}", u.name, clause);
            parts.push(clause);
        }
    }
    for fk in &t.foreign_keys {
        if fk.name.is_empty() {
            panic!("internal invariant violation: foreign key name is empty");
        }
        let mut ref_ = format!("{}.{}", fk.references_schema, fk.references_table);
        if fk.references_schema.is_empty() {
            ref_ = fk.references_table.clone();
        }
        let mut clause = format!(
            "FOREIGN KEY ({}) REFERENCES {} ({})",
            fk.columns.join(", "),
            ref_,
            fk.references_columns.join(", ")
        );
        clause = format!("CONSTRAINT {} {}", fk.name, clause);
        if !fk.on_delete.is_empty() && fk.on_delete != "NO ACTION" {
            clause.push_str(" ON DELETE ");
            clause.push_str(&fk.on_delete);
        }
        if !fk.on_update.is_empty() && fk.on_update != "NO ACTION" {
            clause.push_str(" ON UPDATE ");
            clause.push_str(&fk.on_update);
        }
        parts.push(clause);
    }
    format!(
        "CREATE TABLE {}.{} ({})",
        t.schema,
        t.name,
        parts.join(", ")
    )
}

fn create_index_sql(idx: &Index) -> String {
    let qual = if idx.unique { "UNIQUE " } else { "" };
    let using = if !idx.index_type.is_empty() && idx.index_type != "btree" {
        format!(" USING {}", idx.index_type)
    } else {
        String::new()
    };
    let col_list = index_column_list_sql(&idx.columns);
    format!(
        "CREATE {}INDEX CONCURRENTLY IF NOT EXISTS {} ON {}.{} ({}){}",
        qual, idx.name, idx.table_schema, idx.table_name, col_list, using
    )
}

/// Index attribute list for CREATE INDEX. Expressions using `->` / `->>` must be wrapped in
/// parentheses so the outer `(...)` of CREATE INDEX does not produce invalid SQL like
/// `(props->>'kind')` (parsed as `(props) -> ...`).
fn index_column_list_sql(columns: &[String]) -> String {
    columns
        .iter()
        .map(|c| {
            let t = c.trim();
            if t.contains("->") && !t.starts_with('(') {
                format!("({t})")
            } else {
                t.to_string()
            }
        })
        .collect::<Vec<_>>()
        .join(", ")
}

fn add_pk_sql(schema_name: &str, table_name: &str, pk: &PrimaryKeyConstraint) -> String {
    format!(
        "ALTER TABLE {}.{} ADD PRIMARY KEY ({})",
        schema_name,
        table_name,
        pk.columns.join(", ")
    )
}

fn add_unique_sql(schema_name: &str, table_name: &str, u: &UniqueConstraint) -> String {
    if u.name.is_empty() {
        panic!("internal invariant violation: unique constraint name is empty");
    }
    format!(
        "ALTER TABLE {}.{} ADD CONSTRAINT {} UNIQUE ({})",
        schema_name,
        table_name,
        u.name,
        u.columns.join(", ")
    )
}

fn add_fk_sql(schema_name: &str, table_name: &str, fk: &ForeignKey) -> String {
    if fk.name.is_empty() {
        panic!("internal invariant violation: foreign key name is empty");
    }
    let mut ref_ = format!("{}.{}", fk.references_schema, fk.references_table);
    if fk.references_schema.is_empty() {
        ref_ = fk.references_table.clone();
    }
    let mut s = format!(
        "ALTER TABLE {}.{} ADD CONSTRAINT {} FOREIGN KEY ({}) REFERENCES {} ({})",
        schema_name,
        table_name,
        fk.name,
        fk.columns.join(", "),
        ref_,
        fk.references_columns.join(", ")
    );
    if !fk.on_delete.is_empty() && fk.on_delete != "NO ACTION" {
        s.push_str(" ON DELETE ");
        s.push_str(&fk.on_delete);
    }
    if !fk.on_update.is_empty() && fk.on_update != "NO ACTION" {
        s.push_str(" ON UPDATE ");
        s.push_str(&fk.on_update);
    }
    s
}

fn add_column_sql(schema: &str, table: &str, c: &Column) -> String {
    format!(
        "ALTER TABLE {}.{} ADD COLUMN {}",
        schema,
        table,
        column_def(c)
    )
}

fn alter_column_sql(schema: &str, table: &str, old: &Column, new: &Column) -> String {
    let mut clauses = Vec::new();
    if old.nullable != new.nullable {
        if new.nullable {
            clauses.push(format!("ALTER COLUMN {} DROP NOT NULL", new.name));
        } else {
            clauses.push(format!("ALTER COLUMN {} SET NOT NULL", new.name));
        }
    }
    if old.default != new.default {
        if new.default.is_empty() {
            clauses.push(format!("ALTER COLUMN {} DROP DEFAULT", new.name));
        } else {
            clauses.push(format!(
                "ALTER COLUMN {} SET DEFAULT {}",
                new.name, new.default
            ));
        }
    }
    if old.type_ != new.type_ {
        clauses.push(format!("ALTER COLUMN {} TYPE {}", new.name, new.type_));
    }
    format!("ALTER TABLE {}.{} {}", schema, table, clauses.join(", "))
}

fn drop_column_sql(schema: &str, table: &str, column_name: &str) -> String {
    format!(
        "ALTER TABLE {}.{} DROP COLUMN IF EXISTS {}",
        schema, table, column_name
    )
}

fn drop_table_sql(schema: &str, table: &str) -> String {
    format!("DROP TABLE {}.{}", schema, table)
}

fn column_def(c: &Column) -> String {
    let mut s = format!("{} {}", c.name, c.type_);
    if !c.nullable {
        s.push_str(" NOT NULL");
    }
    if !c.default.is_empty() {
        s.push_str(" DEFAULT ");
        s.push_str(&c.default);
    }
    s
}
