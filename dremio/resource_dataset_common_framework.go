package dremio

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

// Shared schema pieces and pure conversion helpers for dremio_physical_dataset
// and dremio_promoted_dataset - both "adopt an existing file/table at a path"
// resources rather than creating anything new, and both share the same
// acceleration-refresh-policy attributes. Mirrors the SDKv2
// makePhysicalDatasetSchema/makeDatasetSchema helpers in utils_datasets.go.

// physicalDatasetAccelerationAttributes returns the schema attributes for
// locating the target (source_id/relative_path, both RequiresReplace - the
// SDKv2 resource also treated both as ForceNew) and configuring its
// acceleration refresh policy.
func physicalDatasetAccelerationAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"source_id": schema.StringAttribute{
			Required:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"relative_path": schema.ListAttribute{
			Required:      true,
			ElementType:   types.StringType,
			PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplace()},
		},
		"acc_refresh_period_ms": schema.Int64Attribute{
			Optional: true,
			Computed: true,
			Default:  int64default.StaticInt64(10800000),
		},
		"acc_grace_period_ms": schema.Int64Attribute{
			Optional: true,
			Computed: true,
			Default:  int64default.StaticInt64(32400000),
		},
		"acc_method": schema.StringAttribute{
			Optional:   true,
			Computed:   true,
			Default:    stringdefault.StaticString("FULL"),
			Validators: []validator.String{stringvalidator.OneOf("FULL", "INCREMENTAL")},
		},
		"acc_refresh_field": schema.StringAttribute{
			Optional: true,
			Computed: true,
			Default:  stringdefault.StaticString(""),
		},
		"acc_never_expire": schema.BoolAttribute{
			Optional: true,
			Computed: true,
			Default:  booldefault.StaticBool(false),
		},
		"acc_never_refresh": schema.BoolAttribute{
			Optional: true,
			Computed: true,
			Default:  booldefault.StaticBool(false),
		},
	}
}

// datasetComputedAttributes returns fields/path/query_path, the read-only
// attributes shared by every dataset resource in this provider.
func datasetComputedAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"fields": schema.ListNestedAttribute{
			Computed:  true,
			Sensitive: true,
			NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{Computed: true},
					"type": schema.StringAttribute{Computed: true},
				},
			},
		},
		"path": schema.ListAttribute{
			Computed:    true,
			ElementType: types.StringType,
		},
		"query_path": schema.StringAttribute{
			Computed: true,
		},
	}
}

// resolvePhysicalDatasetAbsolutePath resolves source_id + relative_path into
// the absolute catalog path, mirroring the SDKv2 getPhysicalDatasetAbsolutePath/
// getAbsolutePath helpers.
func resolvePhysicalDatasetAbsolutePath(ctx context.Context, client *dapi.Client, sourceID string, relativePath types.List) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics

	source, err := client.GetCatalogEntityById(sourceID)
	if err != nil {
		diags.AddError("Unable to resolve Dremio physical dataset source", err.Error())
		return nil, diags
	}

	var relElems []string
	diags.Append(relativePath.ElementsAs(ctx, &relElems, false)...)
	if diags.HasError() {
		return nil, diags
	}

	return append(append([]string{}, source.Path...), relElems...), diags
}

// deriveSourceIDAndRelativePath is the reverse of
// resolvePhysicalDatasetAbsolutePath: given a dataset's absolute path as
// returned by the API, it resolves source_id/relative_path. The first path
// element is always the source's own name (a physical/promoted dataset
// always lives directly under a source, however deep the folder nesting
// below it), so relative_path is simply everything after it.
//
// Without this, Read never set source_id/relative_path (matching the SDKv2
// resource, which had the same gap), which left them empty after `tofu
// import` and forced a spurious destroy+recreate on the very next plan
// (both are RequiresReplace) - the same bug found and fixed for
// dremio_virtual_dataset's parent_id/name, confirmed live here too.
func deriveSourceIDAndRelativePath(ctx context.Context, client *dapi.Client, path []string) (string, types.List, diag.Diagnostics) {
	var diags diag.Diagnostics

	if len(path) < 2 {
		diags.AddError("Unexpected Dremio dataset path", fmt.Sprintf("path %v has fewer than 2 elements, can't determine a source and relative path", path))
		return "", types.ListNull(types.StringType), diags
	}

	source, err := client.GetCatalogEntityByPath(path[:1])
	if err != nil {
		diags.AddError("Unable to resolve Dremio dataset source", err.Error())
		return "", types.ListNull(types.StringType), diags
	}

	relativePath, d := types.ListValueFrom(ctx, types.StringType, path[1:])
	diags.Append(d...)
	return source.Id, relativePath, diags
}

// accelerationPolicyValues is the plain-Go mirror of the acc_* schema
// attributes, used to keep the API conversion functions free of Framework
// types (and therefore easy to unit test).
type accelerationPolicyValues struct {
	RefreshPeriodMs int64
	GracePeriodMs   int64
	Method          string
	RefreshField    string
	NeverExpire     bool
	NeverRefresh    bool
}

func accelerationPolicyToAPI(v accelerationPolicyValues) *dapi.DatasetAccelerationRefreshPolicy {
	return &dapi.DatasetAccelerationRefreshPolicy{
		RefreshPeriodMs: int(v.RefreshPeriodMs),
		GracePeriodMs:   int(v.GracePeriodMs),
		Method:          v.Method,
		RefreshField:    v.RefreshField,
		NeverExpire:     v.NeverExpire,
		NeverRefresh:    v.NeverRefresh,
	}
}

// accelerationPolicyFromAPI is the reverse of accelerationPolicyToAPI. A nil
// policy (Dremio never configured one) maps to the same zero values the
// SDKv2 resource's readPhysicalDatasetRefreshPolicy fell back to.
func accelerationPolicyFromAPI(policy *dapi.DatasetAccelerationRefreshPolicy) accelerationPolicyValues {
	if policy == nil {
		return accelerationPolicyValues{}
	}
	return accelerationPolicyValues{
		RefreshPeriodMs: int64(policy.RefreshPeriodMs),
		GracePeriodMs:   int64(policy.GracePeriodMs),
		Method:          policy.Method,
		RefreshField:    policy.RefreshField,
		NeverExpire:     policy.NeverExpire,
		NeverRefresh:    policy.NeverRefresh,
	}
}

// physicalDatasetFormatValues is the plain-Go mirror of the CSV/Excel format
// attributes on dremio_promoted_dataset.
type physicalDatasetFormatValues struct {
	Type                    string
	FieldDelimiter          string
	LineDelimiter           string
	Quote                   string
	Comment                 string
	Escape                  string
	SkipFirstLine           bool
	ExtractHeader           bool
	TrimHeader              bool
	AutoGenerateColumnNames bool
	SheetName               string
	HasMergedCells          bool
}

func physicalDatasetFormatToAPI(v physicalDatasetFormatValues) *dapi.PhysicalDatasetFormat {
	return &dapi.PhysicalDatasetFormat{
		Type:                    v.Type,
		FieldDelimiter:          v.FieldDelimiter,
		LineDelimiter:           v.LineDelimiter,
		Quote:                   v.Quote,
		Comment:                 v.Comment,
		Escape:                  v.Escape,
		SkipFirstLine:           v.SkipFirstLine,
		ExtractHeader:           v.ExtractHeader,
		TrimHeader:              v.TrimHeader,
		AutoGenerateColumnNames: v.AutoGenerateColumnNames,
		SheetName:               v.SheetName,
		HasMergedCells:          v.HasMergedCells,
	}
}

// physicalDatasetFormatFromAPI is the reverse of physicalDatasetFormatToAPI.
// A nil format maps to the same zero values the SDKv2 resource's
// readPhysicalDatasetFormat fell back to.
func physicalDatasetFormatFromAPI(format *dapi.PhysicalDatasetFormat) physicalDatasetFormatValues {
	if format == nil {
		return physicalDatasetFormatValues{}
	}
	return physicalDatasetFormatValues{
		Type:                    format.Type,
		FieldDelimiter:          format.FieldDelimiter,
		LineDelimiter:           format.LineDelimiter,
		Quote:                   format.Quote,
		Comment:                 format.Comment,
		Escape:                  format.Escape,
		SkipFirstLine:           format.SkipFirstLine,
		ExtractHeader:           format.ExtractHeader,
		TrimHeader:              format.TrimHeader,
		AutoGenerateColumnNames: format.AutoGenerateColumnNames,
		SheetName:               format.SheetName,
		HasMergedCells:          format.HasMergedCells,
	}
}
