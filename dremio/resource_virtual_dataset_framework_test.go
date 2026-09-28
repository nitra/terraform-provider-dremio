package dremio

import (
	"context"
	"testing"

	dapi "github.com/saltxwater/go-dremio-api-client"
)

func TestStringsToOptionalList_empty(t *testing.T) {
	l, diags := stringsToOptionalList(context.Background(), nil)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !l.IsNull() {
		t.Errorf("expected a null list for zero items, got %v", l)
	}
}

func TestStringsToOptionalList_roundtrip(t *testing.T) {
	l, diags := stringsToOptionalList(context.Background(), []string{"a", "b"})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	var names []string
	if d := l.ElementsAs(context.Background(), &names, false); d.HasError() {
		t.Fatalf("reading back list: %v", d)
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("got %v, want [a b]", names)
	}
}

func TestDatasetFieldsToList_empty(t *testing.T) {
	l, diags := datasetFieldsToList(context.Background(), nil)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	var out []datasetFieldModel
	if d := l.ElementsAs(context.Background(), &out, false); d.HasError() {
		t.Fatalf("reading back list: %v", d)
	}
	if len(out) != 0 {
		t.Errorf("got %v, want empty", out)
	}
}

func TestDatasetFieldsToList_roundtrip(t *testing.T) {
	apiFields := []dapi.DatasetField{
		{Name: "id", Type: dapi.DatasetFieldType{Name: "BIGINT"}},
		{Name: "name", Type: dapi.DatasetFieldType{Name: "VARCHAR"}},
	}
	l, diags := datasetFieldsToList(context.Background(), apiFields)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	var out []datasetFieldModel
	if d := l.ElementsAs(context.Background(), &out, false); d.HasError() {
		t.Fatalf("reading back list: %v", d)
	}
	if len(out) != 2 {
		t.Fatalf("got %d fields, want 2", len(out))
	}
	if out[0].Name.ValueString() != "id" || out[0].Type.ValueString() != "BIGINT" {
		t.Errorf("field 0: got %+v", out[0])
	}
	if out[1].Name.ValueString() != "name" || out[1].Type.ValueString() != "VARCHAR" {
		t.Errorf("field 1: got %+v", out[1])
	}
}
