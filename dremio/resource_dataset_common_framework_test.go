package dremio

import (
	"testing"

	dapi "github.com/saltxwater/go-dremio-api-client"
)

func TestAccelerationPolicyToAPI(t *testing.T) {
	v := accelerationPolicyValues{
		RefreshPeriodMs: 10800000,
		GracePeriodMs:   32400000,
		Method:          "INCREMENTAL",
		RefreshField:    "updated_at",
		NeverExpire:     true,
		NeverRefresh:    false,
	}
	got := accelerationPolicyToAPI(v)
	want := &dapi.DatasetAccelerationRefreshPolicy{
		RefreshPeriodMs: 10800000,
		GracePeriodMs:   32400000,
		Method:          "INCREMENTAL",
		RefreshField:    "updated_at",
		NeverExpire:     true,
		NeverRefresh:    false,
	}
	if *got != *want {
		t.Errorf("got %+v, want %+v", *got, *want)
	}
}

func TestAccelerationPolicyFromAPI_nil(t *testing.T) {
	got := accelerationPolicyFromAPI(nil)
	want := accelerationPolicyValues{}
	if got != want {
		t.Errorf("got %+v, want zero value %+v", got, want)
	}
}

func TestAccelerationPolicyFromAPI_roundtrip(t *testing.T) {
	policy := &dapi.DatasetAccelerationRefreshPolicy{
		RefreshPeriodMs: 1000,
		GracePeriodMs:   2000,
		Method:          "FULL",
		RefreshField:    "id",
		NeverExpire:     false,
		NeverRefresh:    true,
	}
	got := accelerationPolicyFromAPI(policy)
	want := accelerationPolicyValues{
		RefreshPeriodMs: 1000,
		GracePeriodMs:   2000,
		Method:          "FULL",
		RefreshField:    "id",
		NeverExpire:     false,
		NeverRefresh:    true,
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestPhysicalDatasetFormatToAPI(t *testing.T) {
	v := physicalDatasetFormatValues{
		Type:                    "Text",
		FieldDelimiter:          ",",
		SkipFirstLine:           true,
		ExtractHeader:           true,
		AutoGenerateColumnNames: false,
	}
	got := physicalDatasetFormatToAPI(v)
	if got.Type != "Text" || got.FieldDelimiter != "," || !got.SkipFirstLine || !got.ExtractHeader || got.AutoGenerateColumnNames {
		t.Errorf("got %+v", *got)
	}
}

func TestPhysicalDatasetFormatFromAPI_nil(t *testing.T) {
	got := physicalDatasetFormatFromAPI(nil)
	want := physicalDatasetFormatValues{}
	if got != want {
		t.Errorf("got %+v, want zero value %+v", got, want)
	}
}

func TestPhysicalDatasetFormatFromAPI_roundtrip(t *testing.T) {
	format := &dapi.PhysicalDatasetFormat{
		Type:           "Excel",
		SheetName:      "Sheet1",
		HasMergedCells: true,
	}
	got := physicalDatasetFormatFromAPI(format)
	want := physicalDatasetFormatValues{
		Type:           "Excel",
		SheetName:      "Sheet1",
		HasMergedCells: true,
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
