package db

import "testing"

func TestNormalizeInfoSchemaDefault_StripsTextCast(t *testing.T) {
	got := normalizeInfoSchemaDefault("'init'::text")
	if got != "'init'" {
		t.Errorf("got %q, want %q", got, "'init'")
	}
}

func TestNormalizeInfoSchemaDefault_StripsCharacterVaryingCast(t *testing.T) {
	got := normalizeInfoSchemaDefault("'init'::character varying")
	if got != "'init'" {
		t.Errorf("got %q, want %q", got, "'init'")
	}
}

func TestNormalizeInfoSchemaDefault_StripsParameterizedCast(t *testing.T) {
	got := normalizeInfoSchemaDefault("'0.00'::numeric(10,2)")
	if got != "'0.00'" {
		t.Errorf("got %q, want %q", got, "'0.00'")
	}
}

func TestNormalizeInfoSchemaDefault_NoCastUnchanged(t *testing.T) {
	got := normalizeInfoSchemaDefault("42")
	if got != "42" {
		t.Errorf("got %q, want %q", got, "42")
	}
}

func TestNormalizeInfoSchemaDefault_Empty(t *testing.T) {
	got := normalizeInfoSchemaDefault("")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
