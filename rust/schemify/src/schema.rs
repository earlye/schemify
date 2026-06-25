//! Desired/introspected schema types.

#[derive(Debug, Clone)]
pub struct AllowDropColumn {
    pub name: String,
    pub type_: String,
}

#[derive(Debug, Clone)]
pub struct PrimaryKeyConstraint {
    pub name: String,
    pub columns: Vec<String>,
}

#[derive(Debug, Clone)]
pub struct UniqueConstraint {
    pub name: String,
    pub columns: Vec<String>,
}

#[derive(Debug, Clone)]
pub struct ForeignKey {
    pub name: String,
    pub columns: Vec<String>,
    pub references_schema: String,
    pub references_table: String,
    pub references_columns: Vec<String>,
    pub on_delete: String,
    pub on_update: String,
}

#[derive(Debug, Clone)]
pub struct Table {
    pub schema: String,
    pub name: String,
    pub columns: Vec<Column>,
    pub allow_drop_columns: Vec<AllowDropColumn>,
    pub primary_key: Option<PrimaryKeyConstraint>,
    pub unique_keys: Vec<UniqueConstraint>,
    pub foreign_keys: Vec<ForeignKey>,
}

#[derive(Debug, Clone)]
pub struct Column {
    pub name: String,
    pub type_: String,
    pub nullable: bool,
    pub default: String,
}

#[derive(Debug, Clone)]
pub struct Index {
    pub name: String,
    pub schema: String,
    pub table_schema: String,
    pub table_name: String,
    pub columns: Vec<String>,
    pub unique: bool,
    pub index_type: String,
    pub concurrently: bool,
}

pub fn table_key(schema: &str, name: &str) -> String {
    let schema = if schema.is_empty() { "public" } else { schema };
    format!("{schema}.{name}")
}

pub fn index_key(schema: &str, index_name: &str) -> String {
    let schema = if schema.is_empty() { "public" } else { schema };
    format!("{schema}.{index_name}")
}

pub fn normalize_type(t: &str) -> String {
    let t = t.trim().to_lowercase();
    match t.as_str() {
        "int" | "int4" => "integer".into(),
        "int8" => "bigint".into(),
        "int2" => "smallint".into(),
        _ => {
            if t.starts_with("character varying") || t.starts_with("varchar") {
                format!("character varying{}", type_length_suffix(&t))
            } else if t.starts_with("char(") || t == "character" {
                format!("character{}", type_length_suffix(&t))
            } else {
                t
            }
        }
    }
}

pub fn type_length_suffix(t: &str) -> String {
    match t.find('(') {
        Some(i) => t[i..].to_string(),
        None => String::new(),
    }
}

pub fn normalize_info_schema_type(t: &str) -> String {
    let t = t.trim().to_lowercase();
    match t.as_str() {
        "integer" => "integer".into(),
        "bigint" => "bigint".into(),
        "smallint" => "smallint".into(),
        _ => {
            if t.starts_with("character varying") {
                format!("character varying{}", type_length_suffix(&t))
            } else if t.starts_with("varchar") {
                format!("character varying{}", type_length_suffix(&t))
            } else if t.starts_with("character(") {
                format!("character{}", type_length_suffix(&t))
            } else {
                t
            }
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub enum DriftPolicy {
    #[default]
    Drop,
    Deprecated,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DriftScope {
    Table,
    File,
}

#[derive(Debug, Clone)]
pub struct DriftBlock {
    pub id: String,
    pub policy: DriftPolicy,
    pub raw_body: String,
    pub scope: DriftScope,
    pub table_schema: String,
    pub table_name: String,
    pub anticipated_table: Option<Table>,
    pub anticipated_indexes: Vec<Index>,
}

#[derive(Debug, Clone)]
pub struct DecoratedTable {
    pub table: Table,
    pub drift_blocks: Vec<DriftBlock>,
}

#[derive(Debug, Clone, Default)]
pub struct DriftGroup {
    pub id: String,
    pub policy: DriftPolicy,
    pub anticipated_columns: Vec<Column>,
    pub anticipated_unique_keys: Vec<UniqueConstraint>,
    pub anticipated_foreign_keys: Vec<ForeignKey>,
    pub anticipated_indexes: Vec<Index>,
}

pub struct DecoratedLoadResult {
    pub schemas: std::collections::BTreeSet<String>,
    pub tables: std::collections::HashMap<String, Table>,
    pub indexes: std::collections::HashMap<String, Index>,
    pub decorated_tables: std::collections::HashMap<String, DecoratedTable>,
    pub file_level_drift: Vec<DriftBlock>,
    pub drift_groups: std::collections::HashMap<String, DriftGroup>,
}
