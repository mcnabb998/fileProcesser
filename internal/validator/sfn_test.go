package validator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateASL_SchemaErrorNoCauses(t *testing.T) {
	// This test attempts to trigger the fallback error path in ValidateASL
	// by providing a minimal but invalid ASL definition that should fail schema validation
	// and (depending on the schema) may not populate Causes.
	f := filepath.Join(t.TempDir(), "invalid2.asl.json")
	if err := os.WriteFile(f, []byte(`{"States":{}}`), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	errs := ValidateASL(f)
	if len(errs) == 0 {
		t.Error("expected schema validation error, got none")
	}
}

func TestDiscoverASL(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.asl.json")
	f2 := filepath.Join(dir, "b.asl.yaml")
	f3 := filepath.Join(dir, "c.txt")
	if err := os.WriteFile(f1, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if err := os.WriteFile(f2, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if err := os.WriteFile(f3, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	files, err := DiscoverASL([]string{dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 asl files, got %d", len(files))
	}
}

func TestValidateASL_ReadError(t *testing.T) {
	errs := ValidateASL("/nonexistent/file.asl.json")
	if len(errs) != 1 || errs[0] == nil || errs[0].Error()[:4] != "read" {
		t.Errorf("expected read error, got %v", errs)
	}
}

func TestValidateASL_DecodeError(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bad.asl.json")
	if err := os.WriteFile(f, []byte("notjson"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	errs := ValidateASL(f)
	if len(errs) != 1 || errs[0] == nil || errs[0].Error()[:6] != "decode" {
		t.Errorf("expected decode error, got %v", errs)
	}
}

func TestValidateASL_SchemaError(t *testing.T) {
	// This should fail schema validation: missing required fields for a Step Function definition
	f := filepath.Join(t.TempDir(), "invalid.asl.json")
	if err := os.WriteFile(f, []byte(`{"foo": "bar"}`), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	errs := ValidateASL(f)
	if len(errs) == 0 {
		t.Error("expected schema validation error, got none")
	}
}
