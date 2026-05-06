pub fn predicted_unique_constraint_name(table_name: &str, columns: &[String]) -> String {
    format!("{}_{}_key", table_name, columns.join("_"))
}

pub fn predicted_foreign_key_constraint_name(table_name: &str, columns: &[String]) -> String {
    format!("{}_{}_fkey", table_name, columns.join("_"))
}
