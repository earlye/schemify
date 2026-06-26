//! Drift block parsing (ported from Go internal/schema/drift.go).

use regex::Regex;
use std::collections::HashMap;
use std::sync::OnceLock;

use crate::{
    error::{Error, Result},
    load::parse_ddl,
    schema::{DriftBlock, DriftGroup, DriftPolicy, DriftScope},
};

static DRIFT_OPEN_RE: OnceLock<Regex> = OnceLock::new();
static DRIFT_CLOSE_RE: OnceLock<Regex> = OnceLock::new();

fn drift_open_re() -> &'static Regex {
    DRIFT_OPEN_RE.get_or_init(|| {
        Regex::new(r"(?i)^\s*--\s*DRIFT\s+(\w+)\s+(DROP|DEPRECATED)\s*\(\s*$").unwrap()
    })
}

fn drift_close_re() -> &'static Regex {
    DRIFT_CLOSE_RE.get_or_init(|| Regex::new(r"^\s*--\s*\)\s*;?\s*$").unwrap())
}

pub fn strip_comment_prefix(line: &str) -> String {
    match line.find("--") {
        None => line.to_string(),
        Some(pos) => {
            let before = &line[..pos];
            let after = &line[pos + 2..];
            let stripped_after = after.strip_prefix(' ').unwrap_or(after);
            format!("{}{}", before, stripped_after)
        }
    }
}

pub fn net_paren_depth(line: &str) -> i32 {
    let mut depth: i32 = 0;
    let mut in_single = false;
    let mut in_double = false;
    for ch in line.chars() {
        match ch {
            '\'' if !in_double => {
                in_single = !in_single;
            }
            '"' if !in_single => {
                in_double = !in_double;
            }
            '(' if !in_single && !in_double => {
                depth += 1;
            }
            ')' if !in_single && !in_double => {
                depth -= 1;
            }
            _ => {}
        }
    }
    depth
}

pub fn extract_drift_blocks(
    raw_sql: &str,
    table_schema: &str,
    table_name: &str,
) -> Result<Vec<DriftBlock>> {
    let scope = if !table_schema.is_empty() || !table_name.is_empty() {
        DriftScope::Table
    } else {
        DriftScope::File
    };

    let mut blocks = Vec::new();
    let mut seen_ids: HashMap<String, DriftPolicy> = HashMap::new();

    let mut in_block = false;
    let mut current_id = String::new();
    let mut current_policy = DriftPolicy::Drop;
    let mut body_lines: Vec<String> = Vec::new();
    let mut depth: i32 = 0;

    for line in raw_sql.lines() {
        if !in_block {
            let Some(caps) = drift_open_re().captures(line) else {
                continue;
            };
            let id = caps.get(1).unwrap().as_str().to_string();
            let policy_str = caps.get(2).unwrap().as_str().to_uppercase();
            let policy = if policy_str == "DROP" {
                DriftPolicy::Drop
            } else {
                DriftPolicy::Deprecated
            };
            if let Some(existing) = seen_ids.get(&id) {
                if *existing != policy {
                    return Err(Error::LoadSchema(format!(
                        "DRIFT block {:?} has conflicting policies: {:?} and {:?}",
                        id, existing, policy
                    )));
                }
            }
            seen_ids.insert(id.clone(), policy.clone());
            in_block = true;
            current_id = id;
            current_policy = policy;
            body_lines = Vec::new();
            depth = 1;
        } else {
            let stripped = strip_comment_prefix(line);
            let is_closing = drift_close_re().is_match(line);
            let net = net_paren_depth(&stripped);
            let new_depth = depth + net;
            if new_depth == 0 {
                if is_closing {
                    blocks.push(DriftBlock {
                        id: current_id.clone(),
                        policy: current_policy.clone(),
                        raw_body: body_lines.join("\n"),
                        scope: scope.clone(),
                        table_schema: table_schema.to_string(),
                        table_name: table_name.to_string(),
                        anticipated_table: None,
                        anticipated_indexes: Vec::new(),
                    });
                    in_block = false;
                    continue;
                }
                return Err(Error::LoadSchema(format!(
                    "DRIFT block {:?}: depth reached 0 before closing -- )",
                    current_id
                )));
            }
            if new_depth < 0 {
                return Err(Error::LoadSchema(format!(
                    "DRIFT block {:?}: negative paren depth",
                    current_id
                )));
            }
            body_lines.push(stripped);
            depth = new_depth;
        }
    }

    if in_block {
        return Err(Error::LoadSchema(format!(
            "DRIFT block {:?}: unclosed block (missing -- ))",
            current_id
        )));
    }

    Ok(blocks)
}

fn trim_trailing_comma(body: &str) -> String {
    let mut owned: Vec<String> = body.lines().map(|l| l.to_string()).collect();
    for i in (0..owned.len()).rev() {
        if !owned[i].trim().is_empty() {
            let trimmed = owned[i].trim_end_matches([' ', '\t', ',']);
            owned[i] = trimmed.to_string();
            break;
        }
    }
    owned.join("\n")
}

pub fn build_anticipated_drift(block: &mut DriftBlock) -> Result<()> {
    match block.scope {
        DriftScope::Table => {
            let body = trim_trailing_comma(&block.raw_body);
            let synthetic_sql = format!("CREATE TABLE __drift_probe__ (\n{}\n);", body);
            let (tables, _, _) = parse_ddl(&synthetic_sql).map_err(|e| {
                Error::LoadSchema(format!(
                    "buildAnticipatedDrift: parse table-scope body for block {:?}: {}",
                    block.id, e
                ))
            })?;
            if !tables.is_empty() {
                block.anticipated_table = Some(tables.into_iter().next().unwrap());
            }
        }
        DriftScope::File => {
            let (tables, indexes, _) = parse_ddl(&block.raw_body).map_err(|e| {
                Error::LoadSchema(format!(
                    "buildAnticipatedDrift: parse file-scope body for block {:?}: {}",
                    block.id, e
                ))
            })?;
            if !tables.is_empty() {
                block.anticipated_table = Some(tables.into_iter().next().unwrap());
            }
            block.anticipated_indexes = indexes.into_iter().collect();
        }
    }
    Ok(())
}

pub fn merge_drift_groups(all_blocks: Vec<DriftBlock>) -> Result<HashMap<String, DriftGroup>> {
    let mut groups: HashMap<String, DriftGroup> = HashMap::new();
    for b in all_blocks {
        let id = b.id.clone();
        let g = groups.entry(id.clone()).or_insert_with(|| DriftGroup {
            id: id.clone(),
            policy: b.policy.clone(),
            ..Default::default()
        });
        if g.policy != b.policy {
            return Err(Error::LoadSchema(format!(
                "DRIFT group {:?} has conflicting policies: {:?} and {:?}",
                id, g.policy, b.policy
            )));
        }
        if let Some(t) = b.anticipated_table {
            g.anticipated_columns.extend(t.columns);
            g.anticipated_unique_keys.extend(t.unique_keys);
            g.anticipated_foreign_keys.extend(t.foreign_keys);
        }
        g.anticipated_indexes.extend(b.anticipated_indexes);
    }
    Ok(groups)
}
