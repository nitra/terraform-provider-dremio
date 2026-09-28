package dremio

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

func TestListToDimensionFields(t *testing.T) {
	dim := mustStringList(t, []string{"a", "b"})
	td := mustStringList(t, []string{"c"})
	got, diags := listToDimensionFields(context.Background(), dim, td)
	if diags.HasError() {
		t.Fatalf("listToDimensionFields returned diagnostics: %v", diags)
	}
	want := []dapi.ReflectionFieldWithGranularity{
		{ReflectionField: dapi.ReflectionField{Name: "a"}, Granularity: "NORMAL"},
		{ReflectionField: dapi.ReflectionField{Name: "b"}, Granularity: "NORMAL"},
		{ReflectionField: dapi.ReflectionField{Name: "c"}, Granularity: "DATE"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestListToDimensionFields_null(t *testing.T) {
	got, diags := listToDimensionFields(context.Background(), types.ListNull(types.StringType), types.ListNull(types.StringType))
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestDimensionFieldsToLists_roundtrip(t *testing.T) {
	fields := []dapi.ReflectionFieldWithGranularity{
		{ReflectionField: dapi.ReflectionField{Name: "a"}, Granularity: "NORMAL"},
		{ReflectionField: dapi.ReflectionField{Name: "c"}, Granularity: "DATE"},
	}
	dimList, tdList, diags := dimensionFieldsToLists(context.Background(), fields)
	if diags.HasError() {
		t.Fatalf("dimensionFieldsToLists returned diagnostics: %v", diags)
	}
	var dimNames, tdNames []string
	if d := dimList.ElementsAs(context.Background(), &dimNames, false); d.HasError() {
		t.Fatalf("reading back dimList: %v", d)
	}
	if d := tdList.ElementsAs(context.Background(), &tdNames, false); d.HasError() {
		t.Fatalf("reading back tdList: %v", d)
	}
	if len(dimNames) != 1 || dimNames[0] != "a" {
		t.Errorf("got dimNames %v, want [a]", dimNames)
	}
	if len(tdNames) != 1 || tdNames[0] != "c" {
		t.Errorf("got tdNames %v, want [c]", tdNames)
	}
}

func TestDimensionFieldsToLists_empty(t *testing.T) {
	dimList, tdList, diags := dimensionFieldsToLists(context.Background(), nil)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !dimList.IsNull() || !tdList.IsNull() {
		t.Errorf("expected both lists null, got dim=%v td=%v", dimList, tdList)
	}
}

func TestListToMeasureFields(t *testing.T) {
	l := mustStringList(t, []string{"revenue"})
	got, diags := listToMeasureFields(context.Background(), l)
	if diags.HasError() {
		t.Fatalf("listToMeasureFields returned diagnostics: %v", diags)
	}
	want := []dapi.ReflectionMeasureField{
		{ReflectionField: dapi.ReflectionField{Name: "revenue"}, MeasureTypeList: []string{"SUM"}},
	}
	if len(got) != 1 || got[0].Name != want[0].Name || got[0].MeasureTypeList[0] != want[0].MeasureTypeList[0] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMeasureFieldsToList_filtersNonSum(t *testing.T) {
	fields := []dapi.ReflectionMeasureField{
		{ReflectionField: dapi.ReflectionField{Name: "revenue"}, MeasureTypeList: []string{"SUM"}},
		{ReflectionField: dapi.ReflectionField{Name: "count_only"}, MeasureTypeList: []string{"COUNT"}},
	}
	l, diags := measureFieldsToList(context.Background(), fields)
	if diags.HasError() {
		t.Fatalf("measureFieldsToList returned diagnostics: %v", diags)
	}
	var names []string
	if d := l.ElementsAs(context.Background(), &names, false); d.HasError() {
		t.Fatalf("reading back list: %v", d)
	}
	if len(names) != 1 || names[0] != "revenue" {
		t.Errorf("got %v, want [revenue]", names)
	}
}

func TestMeasureFieldsToList_empty(t *testing.T) {
	l, diags := measureFieldsToList(context.Background(), nil)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !l.IsNull() {
		t.Errorf("expected a null list for zero fields, got %v", l)
	}
}

func TestAggrReflectionSpecFromModel(t *testing.T) {
	model := aggrReflectionResourceModel{
		Name:                          types.StringValue("test_agg"),
		Enabled:                       types.BoolValue(true),
		DimensionFields:               mustStringList(t, []string{"region"}),
		TimestampDateDimensionFields:  mustStringList(t, []string{"order_date"}),
		MeasureFieldsSum:              mustStringList(t, []string{"revenue"}),
		PartitionDistributionStrategy: types.StringValue("CONSOLIDATED"),
	}
	spec, diags := aggrReflectionSpecFromModel(context.Background(), model)
	if diags.HasError() {
		t.Fatalf("aggrReflectionSpecFromModel returned diagnostics: %v", diags)
	}
	if spec.Name != "test_agg" {
		t.Errorf("got name %q, want test_agg", spec.Name)
	}
	if len(spec.DimensionFields) != 2 {
		t.Fatalf("got %d dimension fields, want 2", len(spec.DimensionFields))
	}
	if len(spec.MeasureFields) != 1 || spec.MeasureFields[0].Name != "revenue" {
		t.Errorf("got measure fields %v, want [revenue]", spec.MeasureFields)
	}
}
