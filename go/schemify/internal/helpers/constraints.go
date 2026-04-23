package helpers

import "strings"

func PredictedUniqueConstraintName(tableName string, columns []string) string {
	return tableName + "_" + strings.Join(columns, "_") + "_key"
}

func PredictedForeignKeyConstraintName(tableName string, columns []string) string {
	return tableName + "_" + strings.Join(columns, "_") + "_fkey"
}
