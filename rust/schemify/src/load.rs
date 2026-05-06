//! Load desired schema from `.sql` files (ported from Go internal/schema/load).

use crate::error::{Error, Result};
use crate::helpers::{predicted_foreign_key_constraint_name, predicted_unique_constraint_name};
use crate::schema::{
    self, AllowDropColumn, ForeignKey, Index, PrimaryKeyConstraint, Table, UniqueConstraint,
};
use pg_query::protobuf::node::Node as PgNode;
use pg_query::protobuf::{
    ColumnDef, ConstrType, Constraint, CreateStmt, IndexElem, IndexStmt, Node, TypeName,
};
use regex::Regex;
use std::collections::HashMap;
use std::fs;
use std::path::Path;
use std::sync::LazyLock;

#[derive(Debug, Default)]
pub struct LoadResult {
    pub tables: HashMap<String, Table>,
    pub indexes: HashMap<String, Index>,
}

static REMOVED_DIRECTIVE_RE: LazyLock<Regex> = LazyLock::new(|| {
    // Use horizontal whitespace only so newline cannot absorb `);` from the next SQL line.
    Regex::new(r"(?m)^[ \t]*--[ \t]*removed:[ \t]*(\w+)[ \t]+(\S+(?:[ \t]+\S+)*)[ \t]*$")
        .expect("regex")
});
static DROP_TABLE_COMMENT_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?m)^\s*--\s*DROP TABLE\s+(\w+(?:\.\w+)?)\s*\(").expect("regex"));
static DROP_TABLE_BLOCK_END_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"^\s*--\s*\)\s*;?\s*$").expect("regex"));

pub fn load_from_dir(dir: impl AsRef<Path>) -> Result<LoadResult> {
    let dir = dir.as_ref();
    let mut entries: Vec<_> = fs::read_dir(dir)?
        .filter_map(|e| e.ok())
        .filter(|e| e.path().is_file())
        .filter(|e| {
            e.path()
                .extension()
                .and_then(|s| s.to_str())
                .map(|ext| ext.eq_ignore_ascii_case("sql"))
                .unwrap_or(false)
        })
        .map(|e| e.file_name().to_string_lossy().into_owned())
        .collect();
    entries.sort();

    let mut tables = HashMap::new();
    let mut indexes = HashMap::new();

    for name in entries {
        let body = fs::read_to_string(dir.join(&name))
            .map_err(|e| Error::LoadSchema(format!("read {name}: {e}")))?;
        let Ok((tbls, idxs)) = parse_ddl(&body) else {
            continue;
        };
        for t in tbls {
            tables.insert(schema::table_key(&t.schema, &t.name), t);
        }
        for idx in idxs {
            indexes.insert(schema::index_key(&idx.schema, &idx.name), idx);
        }
    }

    Ok(LoadResult { tables, indexes })
}

pub fn load_allow_drop_table_defs(dir: impl AsRef<Path>) -> Result<HashMap<String, Table>> {
    let dir = dir.as_ref();
    let mut entries: Vec<_> = fs::read_dir(dir)?
        .filter_map(|e| e.ok())
        .filter(|e| e.path().is_file())
        .filter(|e| {
            e.path()
                .extension()
                .and_then(|s| s.to_str())
                .map(|ext| ext.eq_ignore_ascii_case("sql"))
                .unwrap_or(false)
        })
        .map(|e| e.file_name().to_string_lossy().into_owned())
        .collect();
    entries.sort();

    let mut out = HashMap::new();
    for name in entries {
        let body = fs::read_to_string(dir.join(&name))
            .map_err(|e| Error::LoadSchema(format!("read {name}: {e}")))?;
        for (k, t) in extract_drop_table_block_defs(&body) {
            out.insert(k, t);
        }
    }
    Ok(out)
}

pub fn extract_drop_table_block_defs(raw_sql: &str) -> HashMap<String, Table> {
    let lines: Vec<&str> = raw_sql.lines().collect();
    let mut out = HashMap::new();
    let mut i = 0usize;
    while i < lines.len() {
        let line = lines[i];
        if DROP_TABLE_COMMENT_RE.captures(line).is_none() {
            i += 1;
            continue;
        };
        let start = i;
        let mut end: Option<usize> = None;
        for j in (i + 1)..lines.len() {
            if DROP_TABLE_BLOCK_END_RE.is_match(lines[j]) {
                end = Some(j);
                break;
            }
        }
        let Some(end) = end else {
            i += 1;
            continue;
        };
        let mut sb = String::new();
        for l in &lines[start..=end] {
            let mut trimmed = l.trim_start();
            trimmed = trimmed.strip_prefix("--").unwrap_or(trimmed).trim_start();
            sb.push_str(trimmed);
            sb.push('\n');
        }
        let create_sql = sb.replacen("DROP TABLE", "CREATE TABLE", 1);
        if let Ok((tbls, _)) = parse_ddl(&create_sql) {
            if let Some(t) = tbls.into_iter().next() {
                let key = schema::table_key(&t.schema, &t.name);
                out.insert(key, t);
            }
        }
        i = end + 1;
    }
    out
}

pub fn extract_removed_directives(raw_sql: &str) -> Vec<AllowDropColumn> {
    let mut out = Vec::new();
    for caps in REMOVED_DIRECTIVE_RE.captures_iter(raw_sql) {
        let col_name = caps.get(1).map(|m| m.as_str().trim()).unwrap_or("");
        let mut type_str = caps.get(2).map(|m| m.as_str().trim()).unwrap_or("");
        // Match Go `TrimRight(..., "),")` only when removing stray comma; keep `)` for types like varchar(n).
        type_str = type_str.trim_end_matches(|c: char| c.is_whitespace() || c == ',');
        if col_name.is_empty() {
            continue;
        }
        let allow_type = if type_str.eq_ignore_ascii_case("ANY_TYPE") {
            "ANY_TYPE".into()
        } else {
            schema::normalize_type(type_str)
        };
        out.push(AllowDropColumn {
            name: col_name.into(),
            type_: allow_type,
        });
    }
    out
}

pub fn parse_ddl(sql: &str) -> Result<(Vec<Table>, Vec<Index>)> {
    let parsed = pg_query::parse(sql).map_err(|e| Error::ParseSql(e.to_string()))?;
    let mut tables = Vec::new();
    let mut indexes = Vec::new();

    for raw in &parsed.protobuf.stmts {
        let stmt_sql = slice_raw_stmt(sql, raw);
        let allow_drops = extract_removed_directives(stmt_sql);

        let Some(stmt_box) = &raw.stmt else {
            continue;
        };
        let Some(pg) = &stmt_box.node else {
            continue;
        };

        match pg {
            PgNode::CreateStmt(cs) => {
                let mut t = parse_create_stmt(cs)?;
                t.allow_drop_columns = allow_drops;
                tables.push(t);
            }
            PgNode::IndexStmt(ix) => {
                if !ix.concurrent {
                    let name = ix.idxname.clone();
                    return Err(Error::ParseSql(format!(
                        "CREATE INDEX {name} must use CONCURRENTLY (required for large-table safety)"
                    )));
                }
                indexes.push(parse_index_stmt(ix)?);
            }
            _ => {}
        }
    }

    Ok((tables, indexes))
}

fn slice_raw_stmt<'a>(full: &'a str, raw: &pg_query::protobuf::RawStmt) -> &'a str {
    let start = (raw.stmt_location as isize).clamp(0, full.len() as isize) as usize;
    let len = raw.stmt_len;
    if len <= 0 {
        return full.get(start..).unwrap_or("");
    }
    let end = start.saturating_add(len as usize).min(full.len());
    full.get(start..end).unwrap_or("")
}

fn parse_index_stmt(stmt: &IndexStmt) -> Result<Index> {
    let rel = stmt
        .relation
        .as_ref()
        .ok_or_else(|| Error::ParseSql("index missing relation".into()))?;
    let table_schema = if rel.schemaname.is_empty() {
        "public".into()
    } else {
        rel.schemaname.to_lowercase()
    };
    let table_name = rel.relname.to_lowercase();
    let idx_schema = table_schema.clone();

    let mut cols = Vec::new();
    for n in &stmt.index_params {
        let Some(inner) = &n.node else {
            continue;
        };
        let PgNode::IndexElem(elem) = inner else {
            continue;
        };
        cols.push(index_elem_sql(elem)?);
    }

    let mut idx_type = stmt.access_method.clone();
    if idx_type.is_empty() {
        idx_type = "btree".into();
    }

    Ok(Index {
        name: stmt.idxname.to_lowercase(),
        schema: idx_schema,
        table_schema,
        table_name,
        columns: cols,
        unique: stmt.unique,
        index_type: idx_type,
        concurrently: true,
    })
}

fn index_elem_sql(elem: &IndexElem) -> Result<String> {
    if !elem.name.is_empty() {
        return Ok(elem.name.to_lowercase());
    }
    let Some(expr) = &elem.expr else {
        return Err(Error::ParseSql("index elem missing expr".into()));
    };
    let Some(ne) = &expr.node else {
        return Err(Error::ParseSql("index expr empty".into()));
    };
    match ne.deparse() {
        Ok(s) => Ok(s),
        Err(_) => deparse_expr_for_index(expr),
    }
}

/// Deparse index expressions when libpg_query cannot deparse the subtree standalone
/// (e.g. `CollateClause` / `'x'::text` inside `(doc->>'kind')`).
fn deparse_expr_for_index(n: &Node) -> Result<String> {
    let Some(ne) = &n.node else {
        return Err(Error::ParseSql("empty expr node".into()));
    };
    match ne {
        PgNode::CollateClause(cc) => {
            let Some(arg) = &cc.arg else {
                return Err(Error::ParseSql("collate without arg".into()));
            };
            deparse_expr_for_index(arg)
        }
        PgNode::TypeCast(tc) => {
            let Some(arg) = &tc.arg else {
                return Err(Error::ParseSql("typecast without arg".into()));
            };
            let inner = deparse_expr_for_index(arg)?;
            let Some(tn) = &tc.type_name else {
                return Ok(inner);
            };
            let tn_s = format_type_name(tn);
            Ok(format!("{inner}::{tn_s}"))
        }
        PgNode::ColumnRef(cr) => column_ref_sql(cr),
        PgNode::AConst(ac) => Ok(a_const_sql(ac)?),
        PgNode::AExpr(e) => {
            let op = aexpr_op(e);
            let l = e
                .lexpr
                .as_ref()
                .map(|b| deparse_expr_for_index(b))
                .transpose()?
                .unwrap_or_default();
            let r = e
                .rexpr
                .as_ref()
                .map(|b| deparse_expr_for_index(b))
                .transpose()?
                .unwrap_or_default();
            Ok(format!("{l}{op}{r}"))
        }
        _ => Err(Error::ParseSql(format!(
            "unsupported index expression node (manual deparse)"
        ))),
    }
}

fn aexpr_op(e: &pg_query::protobuf::AExpr) -> String {
    let mut out = String::new();
    for n in &e.name {
        let Some(ne) = &n.node else {
            continue;
        };
        if let PgNode::String(s) = ne {
            out.push_str(&s.sval);
        }
    }
    out
}

fn column_ref_sql(cr: &pg_query::protobuf::ColumnRef) -> Result<String> {
    let mut parts = Vec::new();
    for f in &cr.fields {
        let Some(ne) = &f.node else {
            continue;
        };
        match ne {
            PgNode::String(s) => parts.push(s.sval.clone()),
            _ => {}
        }
    }
    if parts.is_empty() {
        return Err(Error::ParseSql("empty columnref".into()));
    }
    Ok(parts.join("."))
}

fn a_const_sql(ac: &pg_query::protobuf::AConst) -> Result<String> {
    use pg_query::protobuf::a_const::Val;
    match &ac.val {
        Some(Val::Sval(s)) => Ok(format!("'{}'", s.sval.replace('\'', "''"))),
        Some(Val::Ival(i)) => Ok(i.ival.to_string()),
        Some(Val::Boolval(b)) => Ok(if b.boolval { "true" } else { "false" }.into()),
        Some(Val::Fval(f)) => Ok(f.fval.clone()),
        _ => Err(Error::ParseSql("unsupported A_CONST in index expr".into())),
    }
}

fn parse_create_stmt(stmt: &CreateStmt) -> Result<Table> {
    let rel = stmt
        .relation
        .as_ref()
        .ok_or_else(|| Error::ParseSql("CREATE TABLE missing relation".into()))?;
    let schema_name = if rel.schemaname.is_empty() {
        "public".into()
    } else {
        rel.schemaname.to_lowercase()
    };
    let table_name = rel.relname.to_lowercase();

    let mut columns: Vec<schema::Column> = Vec::new();
    let mut primary_key: Option<PrimaryKeyConstraint> = None;
    let mut unique_keys: Vec<UniqueConstraint> = Vec::new();
    let mut foreign_keys: Vec<ForeignKey> = Vec::new();

    let chain = stmt.table_elts.iter().chain(stmt.constraints.iter());
    for elt in chain {
        let Some(inner) = &elt.node else {
            continue;
        };
        match inner {
            PgNode::ColumnDef(cd) => {
                columns.push(parse_column_def(cd)?);
                for cnode in &cd.constraints {
                    let Some(PgNode::Constraint(c)) = cnode.node.as_ref() else {
                        continue;
                    };
                    apply_constraint(
                        c,
                        &table_name,
                        &mut primary_key,
                        &mut unique_keys,
                        &mut foreign_keys,
                        Some(cd.colname.as_str()),
                    )?;
                }
            }
            PgNode::Constraint(c) => {
                apply_constraint(
                    c,
                    &table_name,
                    &mut primary_key,
                    &mut unique_keys,
                    &mut foreign_keys,
                    None,
                )?;
            }
            _ => {}
        }
    }

    Ok(Table {
        schema: schema_name,
        name: table_name,
        columns,
        allow_drop_columns: Vec::new(),
        primary_key,
        unique_keys,
        foreign_keys,
    })
}

fn parse_column_def(cd: &ColumnDef) -> Result<schema::Column> {
    let type_str = cd
        .type_name
        .as_ref()
        .map(format_type_name)
        .unwrap_or_default();
    let default = cd
        .raw_default
        .as_ref()
        .and_then(|b| b.node.as_ref())
        .map(|ne| {
            ne.deparse()
                .map_err(|e| Error::ParseSql(format!("deparse default: {e}")))
        })
        .transpose()?
        .unwrap_or_default();

    Ok(schema::Column {
        name: cd.colname.to_lowercase(),
        type_: schema::normalize_type(&type_str),
        nullable: !cd.is_not_null,
        default,
    })
}

fn format_type_name(tn: &TypeName) -> String {
    let mut parts = Vec::new();
    for n in &tn.names {
        let Some(ne) = &n.node else {
            continue;
        };
        if let PgNode::String(s) = ne {
            parts.push(s.sval.clone());
        }
    }
    if parts.len() >= 2 && parts[0] == "pg_catalog" {
        parts.remove(0);
    }
    let mut base = parts.join(" ").to_lowercase();
    base = match base.as_str() {
        "int4" | "int" => "integer".into(),
        "int8" => "bigint".into(),
        "int2" => "smallint".into(),
        "varchar" => "character varying".into(),
        _ => base,
    };
    if tn.typmods.is_empty() {
        return base;
    }
    let mods: Vec<String> = tn.typmods.iter().filter_map(typmod_to_string).collect();
    if mods.is_empty() {
        base
    } else {
        format!("{}({})", base, mods.join(", "))
    }
}

fn typmod_to_string(n: &Node) -> Option<String> {
    let ne = n.node.as_ref()?;
    let PgNode::AConst(ac) = ne else {
        return None;
    };
    match &ac.val {
        Some(pg_query::protobuf::a_const::Val::Ival(i)) => Some(i.ival.to_string()),
        Some(pg_query::protobuf::a_const::Val::Sval(s)) => Some(s.sval.clone()),
        _ => None,
    }
}

fn apply_constraint(
    c: &Constraint,
    table_name: &str,
    primary_key: &mut Option<PrimaryKeyConstraint>,
    unique_keys: &mut Vec<UniqueConstraint>,
    foreign_keys: &mut Vec<ForeignKey>,
    inline_column: Option<&str>,
) -> Result<()> {
    let ct = ConstrType::try_from(c.contype).unwrap_or(ConstrType::Undefined);
    match ct {
        ConstrType::ConstrPrimary => {
            let mut cols = column_list_from_nodes(&c.keys);
            if cols.is_empty() {
                if let Some(col) = inline_column {
                    cols.push(col.to_lowercase());
                }
            } else {
                cols = cols.into_iter().map(|s| s.to_lowercase()).collect();
            }
            if !cols.is_empty() {
                *primary_key = Some(PrimaryKeyConstraint {
                    name: c.conname.clone(),
                    columns: cols,
                });
            }
        }
        ConstrType::ConstrUnique => {
            let mut cols = column_list_from_nodes(&c.keys);
            if cols.is_empty() {
                if let Some(col) = inline_column {
                    cols.push(col.to_lowercase());
                }
            } else {
                cols = cols.into_iter().map(|s| s.to_lowercase()).collect();
            }
            if cols.is_empty() {
                return Ok(());
            }
            let name = if c.conname.is_empty() {
                predicted_unique_constraint_name(table_name, &cols)
            } else {
                c.conname.clone()
            };
            unique_keys.push(UniqueConstraint {
                name,
                columns: cols,
            });
        }
        ConstrType::ConstrForeign => {
            let mut cols = fk_columns_from_nodes(&c.fk_attrs);
            if cols.is_empty() {
                if let Some(col) = inline_column {
                    cols.push(col.to_lowercase());
                }
            } else {
                cols = cols.into_iter().map(|s| s.to_lowercase()).collect();
            }
            let pktable = c
                .pktable
                .as_ref()
                .ok_or_else(|| Error::ParseSql("foreign key missing pktable".into()))?;
            let mut ref_schema = pktable.schemaname.clone();
            if ref_schema.is_empty() {
                ref_schema = "public".into();
            } else {
                ref_schema = ref_schema.to_lowercase();
            }
            let ref_table = pktable.relname.to_lowercase();
            let mut refcols = fk_columns_from_nodes(&c.pk_attrs);
            refcols = refcols.into_iter().map(|s| s.to_lowercase()).collect();

            let name = if c.conname.is_empty() {
                predicted_foreign_key_constraint_name(table_name, &cols)
            } else {
                c.conname.clone()
            };

            foreign_keys.push(ForeignKey {
                name,
                columns: cols,
                references_schema: ref_schema,
                references_table: ref_table,
                references_columns: refcols,
                on_delete: expand_fk_action(&c.fk_del_action),
                on_update: expand_fk_action(&c.fk_upd_action),
            });
        }
        _ => {}
    }
    Ok(())
}

fn column_list_from_nodes(keys: &[Node]) -> Vec<String> {
    keys.iter().filter_map(node_column_name).collect()
}

fn fk_columns_from_nodes(attrs: &[Node]) -> Vec<String> {
    attrs.iter().filter_map(node_column_name).collect()
}

fn node_column_name(n: &Node) -> Option<String> {
    let ne = n.node.as_ref()?;
    match ne {
        PgNode::ColumnRef(cr) => {
            let mut last: Option<String> = None;
            for f in &cr.fields {
                if let Some(PgNode::String(s)) = f.node.as_ref() {
                    last = Some(s.sval.to_lowercase());
                }
            }
            last
        }
        PgNode::String(s) => Some(s.sval.to_lowercase()),
        _ => None,
    }
}

fn expand_fk_action(code: &str) -> String {
    let c = code.trim();
    if c.contains(' ') {
        return c.to_uppercase();
    }
    match c {
        "" | "a" => "NO ACTION".into(),
        "r" => "RESTRICT".into(),
        "c" => "CASCADE".into(),
        "n" => "SET NULL".into(),
        "d" => "SET DEFAULT".into(),
        _ => c.to_uppercase(),
    }
}
