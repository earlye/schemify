//! Tests for pure normalization helpers in `schema`.

use schemify::schema::normalize_info_schema_default;

#[test]
fn normalize_info_schema_default_strips_text_cast() {
    assert_eq!(normalize_info_schema_default("'init'::text"), "'init'");
}

#[test]
fn normalize_info_schema_default_strips_character_varying_cast() {
    assert_eq!(
        normalize_info_schema_default("'init'::character varying"),
        "'init'"
    );
}

#[test]
fn normalize_info_schema_default_strips_parameterized_cast() {
    assert_eq!(
        normalize_info_schema_default("'0.00'::numeric(10,2)"),
        "'0.00'"
    );
}

#[test]
fn normalize_info_schema_default_no_cast_unchanged() {
    assert_eq!(normalize_info_schema_default("42"), "42");
}

#[test]
fn normalize_info_schema_default_empty() {
    assert_eq!(normalize_info_schema_default(""), "");
}
