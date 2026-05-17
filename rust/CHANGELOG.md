# Changelog

All notable changes to the Rust crates in this directory are documented here.

## Unreleased

### Fixed (behavior change)

- **`load_from_dir`**: If any `.sql` file fails to parse (via `parse_ddl` in `schemify/src/load.rs`), loading now returns **`Err`** immediately with the file name and underlying parse error. Previously, failing files were **silently skipped**, which could yield an empty or partial declarative model and misleading diffs/plans.
- **`load_allow_drop_table_defs`** / **`extract_drop_table_block_defs`**: A completed `-- DROP TABLE … (` … `-- );` block whose synthetic `CREATE TABLE` fails to parse now returns **`Err`** instead of skipping that block.

**Compatibility:** Pipelines or repos that accidentally depended on broken `.sql` files being ignored will now **fail loudly** (intended).
