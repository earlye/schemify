//! Compare desired vs actual schema (ported from Go internal/diff).

use crate::schema::{
    self, DriftGroup, DriftPolicy, ForeignKey, Index, PrimaryKeyConstraint, Table, UniqueConstraint,
};
use regex::Regex;
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

pub const KIND_CREATE_SCHEMA: &str = "create_schema";
pub const KIND_CREATE_TABLE: &str = "create_table";
pub const KIND_ADD_COLUMN: &str = "add_column";
pub const KIND_DROP_COLUMN: &str = "drop_column";
pub const KIND_DROP_TABLE: &str = "drop_table";
pub const KIND_CREATE_INDEX: &str = "create_index";
pub const KIND_ADD_PK: &str = "add_primary_key";
pub const KIND_ADD_UNIQUE: &str = "add_unique_key";
pub const KIND_ADD_FK: &str = "add_foreign_key";
pub const KIND_DROP_UNIQUE: &str = "drop_unique_key";
pub const KIND_DROP_FK: &str = "drop_foreign_key";
pub const KIND_DROP_INDEX: &str = "drop_index";

#[derive(Debug, Clone)]
pub enum MigrationDetail {
    CreateSchema,
    CreateTable { table_def: Table },
    AddColumn { column: schema::Column },
    DropColumn { column_name: String },
    DropTable,
    CreateIndex { index: Index },
    AddPrimaryKey { primary_key: PrimaryKeyConstraint },
    AddUniqueKey { unique_key: UniqueConstraint },
    AddForeignKey { foreign_key: ForeignKey },
    DropUnique { constraint_name: String },
    DropFk { constraint_name: String },
    DropIndex { index: Index },
}

#[derive(Debug, Clone)]
pub struct Migration {
    pub kind: &'static str,
    pub schema: String,
    pub table: String,
    pub detail: MigrationDetail,
}

#[derive(Debug, Clone)]
pub struct DestructiveChange {
    pub kind: String,
    pub schema: String,
    pub table: String,
    pub column: String,
    pub index: String,
    pub name: String,
    pub detail: String,
}

impl DestructiveChange {
    pub fn to_message_line(&self) -> String {
        match self.kind.as_str() {
            "drop_table" => {
                let mut s = format!("table {}.{} would be dropped", self.schema, self.table);
                if !self.detail.is_empty() {
                    s.push_str(": ");
                    s.push_str(&self.detail);
                }
                s
            }
            "drop_index" => format!("index {}.{} would be dropped", self.schema, self.index),
            "drop_column" => format!(
                "column {}.{}.{} would be dropped",
                self.schema, self.table, self.column
            ),
            "drop_unique_key" => format!(
                "unique constraint {}.{}.{} would be dropped",
                self.schema, self.table, self.name
            ),
            "drop_foreign_key" => format!(
                "foreign key {}.{}.{} would be dropped",
                self.schema, self.table, self.name
            ),
            "drop_schema" => format!("schema {} would be dropped", self.schema),
            "add_column_not_null_no_default" => format!(
                "column {}.{}.{} is NOT NULL with no DEFAULT and cannot be added to an existing table; add a DEFAULT or split this into add-nullable, backfill, and SET NOT NULL steps",
                self.schema, self.table, self.column
            ),
            "primary_key_mismatch" => {
                let mut s = format!(
                    "table {}.{} primary key would change",
                    self.schema, self.table
                );
                if !self.detail.is_empty() {
                    s.push_str(": ");
                    s.push_str(&self.detail);
                }
                s
            }
            _ => format!(
                "{} {}.{} would be dropped",
                self.kind, self.schema, self.table
            ),
        }
    }
}

static INDEX_COL_TYPE_CAST_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"::(?:[a-zA-Z_]\w*(?:\s+[a-zA-Z_]\w*)*)").expect("regex"));

pub fn normalize_index_column(col: &str) -> String {
    let mut col: String = col.split_whitespace().collect();
    col = INDEX_COL_TYPE_CAST_RE.replace_all(&col, "").to_string();
    loop {
        let Some(stripped) = strip_outer_parens(&col) else {
            break;
        };
        col = stripped;
    }
    col.to_lowercase()
}

fn strip_outer_parens(s: &str) -> Option<String> {
    if s.len() < 2 || !s.starts_with('(') || !s.ends_with(')') {
        return None;
    }
    let mut depth = 0i32;
    let bytes = s.as_bytes();
    for i in 0..s.len() - 1 {
        match bytes[i] {
            b'(' => depth += 1,
            b')' => {
                depth -= 1;
                if depth == 0 {
                    return None;
                }
            }
            _ => {}
        }
    }
    Some(s[1..s.len() - 1].to_string())
}

pub fn tables_match_for_drop(actual: Option<&Table>, expected: Option<&Table>) -> bool {
    let (Some(actual), Some(expected)) = (actual, expected) else {
        return false;
    };
    let mut a: Vec<String> = actual
        .columns
        .iter()
        .map(|c| format!("{}:{}", c.name, c.type_))
        .collect();
    let mut e: Vec<String> = expected
        .columns
        .iter()
        .map(|c| format!("{}:{}", c.name, c.type_))
        .collect();
    a.sort();
    e.sort();
    a == e
}

pub fn index_matches(actual: Option<&Index>, desired: Option<&Index>) -> bool {
    let (Some(actual), Some(desired)) = (actual, desired) else {
        return false;
    };
    if actual.unique != desired.unique || actual.index_type != desired.index_type {
        return false;
    }
    if actual.columns.len() != desired.columns.len() {
        return false;
    }
    for i in 0..actual.columns.len() {
        if normalize_index_column(&actual.columns[i]) != normalize_index_column(&desired.columns[i])
        {
            return false;
        }
    }
    true
}

fn describe_column_drift(actual: &Table, expected: &Table) -> String {
    let mut actual_set: HashMap<&str, &str> = HashMap::new();
    for c in &actual.columns {
        actual_set.insert(c.name.as_str(), c.type_.as_str());
    }
    let mut expected_set: HashMap<&str, &str> = HashMap::new();
    for c in &expected.columns {
        expected_set.insert(c.name.as_str(), c.type_.as_str());
    }
    let mut in_db_not_directive: Vec<&str> = Vec::new();
    let mut type_mismatch: Vec<String> = Vec::new();
    for (name, typ) in &actual_set {
        match expected_set.get(name) {
            None => in_db_not_directive.push(name),
            Some(et) if *et != *typ => {
                type_mismatch.push(format!("{} (DB {} vs directive {})", name, typ, et))
            }
            _ => {}
        }
    }
    let mut in_directive_not_db: Vec<&str> = Vec::new();
    for name in expected_set.keys() {
        if !actual_set.contains_key(name) {
            in_directive_not_db.push(name);
        }
    }
    in_db_not_directive.sort();
    in_directive_not_db.sort();
    type_mismatch.sort();
    let mut parts = Vec::new();
    if !in_db_not_directive.is_empty() {
        parts.push(format!(
            "in DB but not in table drop directive: {}",
            in_db_not_directive.join(", ")
        ));
    }
    if !in_directive_not_db.is_empty() {
        parts.push(format!(
            "in table drop directive but not in DB: {}",
            in_directive_not_db.join(", ")
        ));
    }
    if !type_mismatch.is_empty() {
        parts.push(format!("type mismatch: {}", type_mismatch.join("; ")));
    }
    if parts.is_empty() {
        "column list mismatch".into()
    } else {
        parts.join("; ")
    }
}

#[allow(clippy::too_many_arguments)]
pub fn diff_tables_and_indexes(
    desired_namespaces: &HashSet<String>,
    actual_namespaces: &HashSet<String>,
    desired: &HashMap<String, Table>,
    actual: &HashMap<String, Table>,
    desired_indexes: Option<&HashMap<String, Index>>,
    actual_indexes: Option<&HashMap<String, Index>>,
    allow_drop_table_defs: Option<&HashMap<String, Table>>,
    drift_groups: Option<&HashMap<String, DriftGroup>>,
) -> (Vec<Migration>, Vec<DestructiveChange>) {
    let mut migrations = Vec::new();
    let mut disallowed = Vec::new();

    for ns in desired_namespaces {
        if !actual_namespaces.contains(ns) {
            migrations.push(Migration {
                kind: KIND_CREATE_SCHEMA,
                schema: ns.clone(),
                table: String::new(),
                detail: MigrationDetail::CreateSchema,
            });
        }
    }
    for ns in actual_namespaces {
        if !desired_namespaces.contains(ns) && crate::namespace::is_drop_schema_candidate(ns) {
            disallowed.push(DestructiveChange {
                kind: "drop_schema".into(),
                schema: ns.clone(),
                table: String::new(),
                column: String::new(),
                index: String::new(),
                name: String::new(),
                detail: String::new(),
            });
        }
    }

    for (key, t) in actual {
        if desired.get(key).is_none() {
            let expected = allow_drop_table_defs.and_then(|m| m.get(key));
            if expected.is_some() && tables_match_for_drop(Some(t), expected) {
                migrations.push(Migration {
                    kind: KIND_DROP_TABLE,
                    schema: t.schema.clone(),
                    table: t.name.clone(),
                    detail: MigrationDetail::DropTable,
                });
            } else {
                let mut dc = DestructiveChange {
                    kind: "drop_table".into(),
                    schema: t.schema.clone(),
                    table: t.name.clone(),
                    column: String::new(),
                    index: String::new(),
                    name: String::new(),
                    detail: String::new(),
                };
                if let Some(exp) = expected {
                    dc.detail = describe_column_drift(t, exp);
                }
                disallowed.push(dc);
            }
        }
    }

    for (key, want) in desired {
        let Some(have) = actual.get(key) else {
            migrations.push(Migration {
                kind: KIND_CREATE_TABLE,
                schema: want.schema.clone(),
                table: want.name.clone(),
                detail: MigrationDetail::CreateTable {
                    table_def: want.clone(),
                },
            });
            continue;
        };

        let mut want_cols: HashSet<&str> = HashSet::new();
        for c in &want.columns {
            want_cols.insert(c.name.as_str());
        }
        let mut allow_drop_by_col: HashMap<&str, &schema::AllowDropColumn> = HashMap::new();
        for a in &want.allow_drop_columns {
            allow_drop_by_col.insert(a.name.as_str(), a);
        }
        for c in &have.columns {
            if !want_cols.contains(c.name.as_str()) {
                // First check allow_drop_by_col (legacy mechanism).
                let allow_drop_matched = if let Some(allow) = allow_drop_by_col.get(c.name.as_str())
                {
                    allow.type_ == "ANY_TYPE" || allow.type_ == c.type_
                } else {
                    false
                };

                if allow_drop_matched {
                    migrations.push(Migration {
                        kind: KIND_DROP_COLUMN,
                        schema: want.schema.clone(),
                        table: want.name.clone(),
                        detail: MigrationDetail::DropColumn {
                            column_name: c.name.clone(),
                        },
                    });
                    continue;
                }

                // Then check drift groups.
                if let Some(groups) = drift_groups {
                    let mut matched_drop = false;
                    let mut matched_deprecated = false;
                    'outer_col: for group in groups.values() {
                        for antic in &group.anticipated_columns {
                            if antic.name == c.name
                                && (antic.type_ == c.type_ || antic.type_ == "ANY_TYPE")
                            {
                                match group.policy {
                                    DriftPolicy::Drop => {
                                        matched_drop = true;
                                    }
                                    DriftPolicy::Deprecated => {
                                        matched_deprecated = true;
                                    }
                                }
                                break 'outer_col;
                            }
                        }
                    }
                    if matched_drop {
                        migrations.push(Migration {
                            kind: KIND_DROP_COLUMN,
                            schema: want.schema.clone(),
                            table: want.name.clone(),
                            detail: MigrationDetail::DropColumn {
                                column_name: c.name.clone(),
                            },
                        });
                        continue;
                    }
                    if matched_deprecated {
                        tracing::debug!(
                            column = %c.name,
                            table = %want.name,
                            "DEPRECATED column tolerated"
                        );
                        continue;
                    }
                }

                // Fall through to disallowed.
                disallowed.push(DestructiveChange {
                    kind: "drop_column".into(),
                    schema: want.schema.clone(),
                    table: want.name.clone(),
                    column: c.name.clone(),
                    index: String::new(),
                    name: String::new(),
                    detail: String::new(),
                });
            }
        }

        let mut have_cols: HashSet<&str> = HashSet::new();
        for c in &have.columns {
            have_cols.insert(c.name.as_str());
        }
        for c in &want.columns {
            if !have_cols.contains(c.name.as_str()) {
                if !c.nullable && c.default.is_empty() {
                    disallowed.push(DestructiveChange {
                        kind: "add_column_not_null_no_default".into(),
                        schema: want.schema.clone(),
                        table: want.name.clone(),
                        column: c.name.clone(),
                        index: String::new(),
                        name: String::new(),
                        detail: String::new(),
                    });
                    continue;
                }
                migrations.push(Migration {
                    kind: KIND_ADD_COLUMN,
                    schema: want.schema.clone(),
                    table: want.name.clone(),
                    detail: MigrationDetail::AddColumn { column: c.clone() },
                });
            }
        }

        match (&have.primary_key, &want.primary_key) {
            (None, Some(pk)) => {
                migrations.push(Migration {
                    kind: KIND_ADD_PK,
                    schema: want.schema.clone(),
                    table: want.name.clone(),
                    detail: MigrationDetail::AddPrimaryKey {
                        primary_key: pk.clone(),
                    },
                });
            }
            (Some(_), None) => {
                disallowed.push(DestructiveChange {
                    kind: "primary_key_mismatch".into(),
                    schema: want.schema.clone(),
                    table: want.name.clone(),
                    column: String::new(),
                    index: String::new(),
                    name: String::new(),
                    detail: describe_pk_drift(have.primary_key.as_ref(), want.primary_key.as_ref()),
                });
            }
            (Some(h), Some(w)) if !constraint_pk_equal(Some(h), Some(w)) => {
                disallowed.push(DestructiveChange {
                    kind: "primary_key_mismatch".into(),
                    schema: want.schema.clone(),
                    table: want.name.clone(),
                    column: String::new(),
                    index: String::new(),
                    name: String::new(),
                    detail: describe_pk_drift(have.primary_key.as_ref(), want.primary_key.as_ref()),
                });
            }
            _ => {}
        }

        for u in &want.unique_keys {
            if !have_unique(u, &have.unique_keys) {
                migrations.push(Migration {
                    kind: KIND_ADD_UNIQUE,
                    schema: want.schema.clone(),
                    table: want.name.clone(),
                    detail: MigrationDetail::AddUniqueKey {
                        unique_key: u.clone(),
                    },
                });
            }
        }
        'unique_loop: for u in &have.unique_keys {
            if !have_unique(u, &want.unique_keys) {
                if let Some(groups) = drift_groups {
                    for group in groups.values() {
                        for au in &group.anticipated_unique_keys {
                            if au.name == u.name && slice_equal(&au.columns, &u.columns) {
                                if group.policy == DriftPolicy::Drop {
                                    migrations.push(Migration {
                                        kind: KIND_DROP_UNIQUE,
                                        schema: want.schema.clone(),
                                        table: want.name.clone(),
                                        detail: MigrationDetail::DropUnique {
                                            constraint_name: u.name.clone(),
                                        },
                                    });
                                }
                                // Both Drop (already pushed) and Deprecated (tolerate) skip disallowed.
                                continue 'unique_loop;
                            }
                        }
                    }
                }
                disallowed.push(DestructiveChange {
                    kind: "drop_unique_key".into(),
                    schema: want.schema.clone(),
                    table: want.name.clone(),
                    column: String::new(),
                    index: String::new(),
                    name: u.name.clone(),
                    detail: String::new(),
                });
            }
        }

        for fk in &want.foreign_keys {
            if !have_fk(fk, &have.foreign_keys) {
                migrations.push(Migration {
                    kind: KIND_ADD_FK,
                    schema: want.schema.clone(),
                    table: want.name.clone(),
                    detail: MigrationDetail::AddForeignKey {
                        foreign_key: fk.clone(),
                    },
                });
            }
        }
        'fk_loop: for fk in &have.foreign_keys {
            if !have_fk(fk, &want.foreign_keys) {
                if let Some(groups) = drift_groups {
                    for group in groups.values() {
                        for afk in &group.anticipated_foreign_keys {
                            if afk.name == fk.name && slice_equal(&afk.columns, &fk.columns) {
                                if group.policy == DriftPolicy::Drop {
                                    migrations.push(Migration {
                                        kind: KIND_DROP_FK,
                                        schema: want.schema.clone(),
                                        table: want.name.clone(),
                                        detail: MigrationDetail::DropFk {
                                            constraint_name: fk.name.clone(),
                                        },
                                    });
                                }
                                // Both Drop and Deprecated skip disallowed.
                                continue 'fk_loop;
                            }
                        }
                    }
                }
                disallowed.push(DestructiveChange {
                    kind: "drop_foreign_key".into(),
                    schema: want.schema.clone(),
                    table: want.name.clone(),
                    column: String::new(),
                    index: String::new(),
                    name: fk.name.clone(),
                    detail: String::new(),
                });
            }
        }
    }

    if let (Some(di), Some(ai)) = (desired_indexes, actual_indexes) {
        for (_key, want_idx) in di {
            let have_idx = ai.get(&schema::index_key(&want_idx.schema, &want_idx.name));
            if have_idx.is_none() || !index_matches(have_idx, Some(want_idx)) {
                migrations.push(Migration {
                    kind: KIND_CREATE_INDEX,
                    schema: want_idx.schema.clone(),
                    table: want_idx.table_name.clone(),
                    detail: MigrationDetail::CreateIndex {
                        index: want_idx.clone(),
                    },
                });
            }
        }
        'idx_loop: for (_key, have_idx) in ai {
            if di
                .get(&schema::index_key(&have_idx.schema, &have_idx.name))
                .is_none()
            {
                if let Some(groups) = drift_groups {
                    for group in groups.values() {
                        for aidx in &group.anticipated_indexes {
                            if index_matches(Some(have_idx), Some(aidx)) {
                                if group.policy == DriftPolicy::Drop {
                                    migrations.push(Migration {
                                        kind: KIND_DROP_INDEX,
                                        schema: have_idx.schema.clone(),
                                        table: have_idx.table_name.clone(),
                                        detail: MigrationDetail::DropIndex {
                                            index: have_idx.clone(),
                                        },
                                    });
                                }
                                // Both Drop and Deprecated skip disallowed.
                                continue 'idx_loop;
                            }
                        }
                    }
                }
                // Indexes on tables being dropped vanish with the table; don't double-report.
                let table_being_dropped = migrations.iter().any(|m| {
                    m.kind == KIND_DROP_TABLE
                        && m.schema == have_idx.table_schema
                        && m.table == have_idx.table_name
                });
                if !table_being_dropped {
                    disallowed.push(DestructiveChange {
                        kind: "drop_index".into(),
                        schema: have_idx.schema.clone(),
                        table: String::new(),
                        column: String::new(),
                        index: have_idx.name.clone(),
                        name: String::new(),
                        detail: String::new(),
                    });
                }
            }
        }
    }

    migrations = sort_migrations(migrations);
    (migrations, disallowed)
}

fn migration_kind_rank(kind: &str) -> i32 {
    match kind {
        KIND_CREATE_SCHEMA => 0,
        KIND_CREATE_TABLE => 1,
        KIND_ADD_COLUMN => 2,
        KIND_ADD_PK => 3,
        KIND_ADD_UNIQUE => 4,
        KIND_ADD_FK => 5,
        KIND_CREATE_INDEX => 6,
        KIND_DROP_COLUMN => 7,
        KIND_DROP_TABLE => 8,
        KIND_DROP_UNIQUE => 9,
        KIND_DROP_FK => 10,
        KIND_DROP_INDEX => 11,
        _ => 12,
    }
}

fn migration_index_name(m: &Migration) -> &str {
    match &m.detail {
        MigrationDetail::CreateIndex { index } => index.name.as_str(),
        _ => "",
    }
}

fn sort_migrations(migrations: Vec<Migration>) -> Vec<Migration> {
    let mut out = migrations;
    out.sort_by(|a, b| {
        migration_kind_rank(a.kind)
            .cmp(&migration_kind_rank(b.kind))
            .then_with(|| a.schema.cmp(&b.schema))
            .then_with(|| a.table.cmp(&b.table))
            .then_with(|| migration_index_name(a).cmp(migration_index_name(b)))
    });
    topo_sort_create_table(out)
}

fn describe_pk_drift(
    actual: Option<&PrimaryKeyConstraint>,
    desired: Option<&PrimaryKeyConstraint>,
) -> String {
    let db_cols = actual
        .map(|p| format!("({})", p.columns.join(", ")))
        .unwrap_or_else(|| "(none)".into());
    let want_cols = desired
        .map(|p| format!("({})", p.columns.join(", ")))
        .unwrap_or_else(|| "(none)".into());
    format!("DB has {db_cols}, schema has {want_cols}")
}

fn constraint_pk_equal(a: Option<&PrimaryKeyConstraint>, b: Option<&PrimaryKeyConstraint>) -> bool {
    match (a, b) {
        (None, None) => true,
        (Some(_), None) | (None, Some(_)) => false,
        (Some(a), Some(b)) => {
            if a.columns.len() != b.columns.len() {
                return false;
            }
            a.columns == b.columns
        }
    }
}

fn have_unique(u: &UniqueConstraint, list: &[UniqueConstraint]) -> bool {
    if u.name.is_empty() {
        panic!("internal invariant violation: unique constraint name is empty");
    }
    for e in list {
        if e.name.is_empty() {
            panic!("internal invariant violation: unique constraint name is empty");
        }
        if e.name == u.name && slice_equal(&e.columns, &u.columns) {
            return true;
        }
    }
    false
}

fn have_fk(fk: &ForeignKey, list: &[ForeignKey]) -> bool {
    if fk.name.is_empty() {
        panic!("internal invariant violation: foreign key name is empty");
    }
    for e in list {
        if e.name.is_empty() {
            panic!("internal invariant violation: foreign key name is empty");
        }
        if e.name == fk.name
            && slice_equal(&e.columns, &fk.columns)
            && e.references_schema == fk.references_schema
            && e.references_table == fk.references_table
            && slice_equal(&e.references_columns, &fk.references_columns)
            && fk_action_equal(&e.on_delete, &fk.on_delete)
            && fk_action_equal(&e.on_update, &fk.on_update)
        {
            return true;
        }
    }
    false
}

fn fk_action_equal(a: &str, b: &str) -> bool {
    fn norm(s: &str) -> &str {
        if s.is_empty() {
            "NO ACTION"
        } else {
            s
        }
    }
    norm(a) == norm(b)
}

fn slice_equal(a: &[String], b: &[String]) -> bool {
    a.len() == b.len() && a.iter().zip(b.iter()).all(|(x, y)| x == y)
}

fn topo_sort_create_table(migrations: Vec<Migration>) -> Vec<Migration> {
    #[derive(Clone)]
    struct Entry {
        schema: String,
        table: String,
        m: Migration,
    }
    let mut creates: Vec<Entry> = Vec::new();
    let mut create_pos: HashMap<String, usize> = HashMap::new();
    for m in &migrations {
        if m.kind == KIND_CREATE_TABLE {
            let key = format!("{}.{}", m.schema, m.table);
            create_pos.insert(key, creates.len());
            creates.push(Entry {
                schema: m.schema.clone(),
                table: m.table.clone(),
                m: m.clone(),
            });
        }
    }
    if creates.is_empty() {
        return migrations;
    }

    let n = creates.len();
    let mut in_degree = vec![0i32; n];
    let mut adj: Vec<Vec<usize>> = vec![Vec::new(); n];
    for (i, e) in creates.iter().enumerate() {
        let MigrationDetail::CreateTable { table_def } = &e.m.detail else {
            continue;
        };
        for fk in &table_def.foreign_keys {
            let ref_key = format!("{}.{}", fk.references_schema, fk.references_table);
            if let Some(&dep_idx) = create_pos.get(&ref_key) {
                if dep_idx != i {
                    adj[dep_idx].push(i);
                    in_degree[i] += 1;
                }
            }
        }
    }

    let mut queue: Vec<usize> = (0..n).filter(|&i| in_degree[i] == 0).collect();
    let mut sorted: Vec<Migration> = Vec::with_capacity(n);
    while let Some(cur) = queue.first().copied() {
        queue.remove(0);
        sorted.push(creates[cur].m.clone());
        for &next in &adj[cur] {
            in_degree[next] -= 1;
            if in_degree[next] == 0 {
                queue.push(next);
            }
        }
    }

    if sorted.len() < n {
        let mut seen: HashSet<(String, String)> = HashSet::new();
        for m in &sorted {
            if m.kind == KIND_CREATE_TABLE {
                seen.insert((m.schema.clone(), m.table.clone()));
            }
        }
        for e in &creates {
            let k = (e.schema.clone(), e.table.clone());
            if !seen.contains(&k) {
                sorted.push(e.m.clone());
                seen.insert(k);
            }
        }
    }

    let mut result = migrations.clone();
    let mut si = 0usize;
    for slot in &mut result {
        if slot.kind == KIND_CREATE_TABLE {
            *slot = sorted[si].clone();
            si += 1;
        }
    }
    result
}
