package dremio

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

func TestListToReflectionFields(t *testing.T) {
	l := mustStringList(t, []string{"a", "b"})
	got, diags := listToReflectionFields(context.Background(), l)
	if diags.HasError() {
		t.Fatalf("listToReflectionFields returned diagnostics: %v", diags)
	}
	want := []dapi.ReflectionField{{Name: "a"}, {Name: "b"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestListToReflectionFields_null(t *testing.T) {
	got, diags := listToReflectionFields(context.Background(), types.ListNull(types.StringType))
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestReflectionFieldsToList_roundtrip(t *testing.T) {
	fields := []dapi.ReflectionField{{Name: "x"}, {Name: "y"}}
	l, diags := reflectionFieldsToList(context.Background(), fields)
	if diags.HasError() {
		t.Fatalf("reflectionFieldsToList returned diagnostics: %v", diags)
	}
	var names []string
	if d := l.ElementsAs(context.Background(), &names, false); d.HasError() {
		t.Fatalf("reading back list: %v", d)
	}
	if len(names) != 2 || names[0] != "x" || names[1] != "y" {
		t.Errorf("got %v, want [x y]", names)
	}
}

func TestReflectionFieldsToList_empty(t *testing.T) {
	l, diags := reflectionFieldsToList(context.Background(), nil)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !l.IsNull() {
		t.Errorf("expected a null list for zero fields, got %v", l)
	}
}
