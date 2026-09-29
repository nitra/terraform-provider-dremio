package dremio

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

// promotedDatasetResourceModel mirrors the SDKv2 dremio_promoted_dataset
// schema exactly - no resource shows up in any real state, but keeping the
// contract identical means no config needs to change to adopt the new
// binary.
//
// Unlike dremio_physical_dataset, this resource does create something:
// Create finds the raw, not-yet-typed file/table at source_id+relative_path
// and promotes it into a typed physical dataset with an explicit format
// (CSV/Excel parsing settings). Delete un-promotes it via DeleteCatalogItem,
// matching the SDKv2 resource.
type promotedDatasetResourceModel struct {
	ID                      types.String `tfsdk:"id"`
	SourceID                types.String `tfsdk:"source_id"`
	RelativePath            types.List   `tfsdk:"relative_path"`
	Type                    types.String `tfsdk:"type"`
	FieldDelimiter          types.String `tfsdk:"field_delimiter"`
	LineDelimiter           types.String `tfsdk:"line_delimiter"`
	Quote                   types.String `tfsdk:"quote"`
	Comment                 types.String `tfsdk:"comment"`
	Escape                  types.String `tfsdk:"escape"`
	SkipFirstLine           types.Bool   `tfsdk:"skip_first_line"`
	ExtractHeader           types.Bool   `tfsdk:"extract_header"`
	TrimHeader              types.Bool   `tfsdk:"trim_header"`
	AutoGenerateColumnNames types.Bool   `tfsdk:"auto_generate_column_names"`
	SheetName               types.String `tfsdk:"sheet_name"`
	HasMergedCells          types.Bool   `tfsdk:"has_merged_cells"`
	AccRefreshPeriodMs      types.Int64  `tfsdk:"acc_refresh_period_ms"`
	AccGracePeriodMs        types.Int64  `tfsdk:"acc_grace_period_ms"`
	AccMethod               types.String `tfsdk:"acc_method"`
	AccRefreshField         types.String `tfsdk:"acc_refresh_field"`
	AccNeverExpire          types.Bool   `tfsdk:"acc_never_expire"`
	AccNeverRefresh         types.Bool   `tfsdk:"acc_never_refresh"`
	Fields                  types.List   `tfsdk:"fields"`
	Path                    types.List   `tfsdk:"path"`
	QueryPath               types.String `tfsdk:"query_path"`
}

type promotedDatasetResource struct {
	client *dapi.Client
}

var (
	_ resource.Resource                = &promotedDatasetResource{}
	_ resource.ResourceWithConfigure   = &promotedDatasetResource{}
	_ resource.ResourceWithImportState = &promotedDatasetResource{}
)

func NewPromotedDatasetResource() resource.Resource {
	return &promotedDatasetResource{}
}

func (r *promotedDatasetResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_promoted_dataset"
}

func (r *promotedDatasetResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*dapi.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected resource configure type", fmt.Sprintf("expected *dapi.Client, got %T", req.ProviderData))
		return
	}
	r.client = client
}

func (r *promotedDatasetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *promotedDatasetResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"type": schema.StringAttribute{
			Required: true,
		},
		// field_delimiter/line_delimiter/quote/comment/escape/sheet_name are
		// Optional+Computed with NO Default: verified live that Dremio
		// server-fills real defaults for a Text format when these are left
		// unset (e.g. quote -> `"`, comment -> `#`, line_delimiter ->
		// `\r\n`), not empty strings. A static Default("") here made
		// Terraform plan a known "" and then error with "Provider produced
		// inconsistent result after apply" once Dremio's real default came
		// back non-empty. Leaving them Computed with no Default makes their
		// planned value unknown-until-apply when unset, which is what lets
		// the provider report whatever Dremio actually filled in.
		"field_delimiter": schema.StringAttribute{
			Optional: true, Computed: true,
		},
		"line_delimiter": schema.StringAttribute{
			Optional: true, Computed: true,
		},
		"quote": schema.StringAttribute{
			Optional: true, Computed: true,
		},
		"comment": schema.StringAttribute{
			Optional: true, Computed: true,
		},
		"escape": schema.StringAttribute{
			Optional: true, Computed: true,
		},
		"skip_first_line": schema.BoolAttribute{
			Optional: true, Computed: true, Default: booldefault.StaticBool(false),
		},
		"extract_header": schema.BoolAttribute{
			Optional: true, Computed: true, Default: booldefault.StaticBool(false),
		},
		"trim_header": schema.BoolAttribute{
			Optional: true, Computed: true, Default: booldefault.StaticBool(false),
		},
		"auto_generate_column_names": schema.BoolAttribute{
			Optional: true, Computed: true, Default: booldefault.StaticBool(false),
		},
		"sheet_name": schema.StringAttribute{
			Optional: true, Computed: true,
		},
		"has_merged_cells": schema.BoolAttribute{
			Optional: true, Computed: true, Default: booldefault.StaticBool(false),
		},
	}
	for k, v := range physicalDatasetAccelerationAttributes() {
		attrs[k] = v
	}
	for k, v := range datasetComputedAttributes() {
		attrs[k] = v
	}
	resp.Schema = schema.Schema{Attributes: attrs}
}

func (r *promotedDatasetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan promotedDatasetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	absolutePath, diags := resolvePhysicalDatasetAbsolutePath(ctx, r.client, plan.SourceID.ValueString(), plan.RelativePath)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	target, err := r.client.GetCatalogEntityByPath(absolutePath)
	if err != nil {
		resp.Diagnostics.AddError("Unable to find Dremio promoted dataset target", err.Error())
		return
	}

	created, err := r.client.NewPhysicalDataset(target.Id, &dapi.NewPhysicalDatasetSpec{
		Path:                      target.Path,
		Format:                    physicalDatasetFormatToAPI(formatValuesFromModel(&plan)),
		AccelerationRefreshPolicy: accelerationPolicyToAPI(accelerationValuesFromModel(&plan)),
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to promote Dremio dataset", err.Error())
		return
	}

	found, rdiags := r.readInto(ctx, created.Id, &plan)
	resp.Diagnostics.Append(rdiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Promoted dataset disappeared right after creation", created.Id)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *promotedDatasetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state promotedDatasetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	found, diags := r.readInto(ctx, state.ID.ValueString(), &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *promotedDatasetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan promotedDatasetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state promotedDatasetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.UpdatePhysicalDataset(state.ID.ValueString(), &dapi.UpdatePhysicalDatasetSpec{
		Format:                    physicalDatasetFormatToAPI(formatValuesFromModel(&plan)),
		AccelerationRefreshPolicy: accelerationPolicyToAPI(accelerationValuesFromModel(&plan)),
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to update Dremio promoted dataset", err.Error())
		return
	}

	plan.ID = state.ID
	found, diags := r.readInto(ctx, state.ID.ValueString(), &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Promoted dataset disappeared right after update", state.ID.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete un-promotes the dataset (reverting the underlying file/table to
// its raw, not-yet-typed state) rather than deleting the file itself -
// matches the SDKv2 resource's DeleteCatalogItem call.
func (r *promotedDatasetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state promotedDatasetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteCatalogItem(state.ID.ValueString())
	if err != nil && !isNotFoundError(err) {
		resp.Diagnostics.AddError("Unable to delete Dremio promoted dataset", err.Error())
	}
}

func formatValuesFromModel(model *promotedDatasetResourceModel) physicalDatasetFormatValues {
	return physicalDatasetFormatValues{
		Type:                    model.Type.ValueString(),
		FieldDelimiter:          model.FieldDelimiter.ValueString(),
		LineDelimiter:           model.LineDelimiter.ValueString(),
		Quote:                   model.Quote.ValueString(),
		Comment:                 model.Comment.ValueString(),
		Escape:                  model.Escape.ValueString(),
		SkipFirstLine:           model.SkipFirstLine.ValueBool(),
		ExtractHeader:           model.ExtractHeader.ValueBool(),
		TrimHeader:              model.TrimHeader.ValueBool(),
		AutoGenerateColumnNames: model.AutoGenerateColumnNames.ValueBool(),
		SheetName:               model.SheetName.ValueString(),
		HasMergedCells:          model.HasMergedCells.ValueBool(),
	}
}

func accelerationValuesFromModel(model *promotedDatasetResourceModel) accelerationPolicyValues {
	return accelerationPolicyValues{
		RefreshPeriodMs: model.AccRefreshPeriodMs.ValueInt64(),
		GracePeriodMs:   model.AccGracePeriodMs.ValueInt64(),
		Method:          model.AccMethod.ValueString(),
		RefreshField:    model.AccRefreshField.ValueString(),
		NeverExpire:     model.AccNeverExpire.ValueBool(),
		NeverRefresh:    model.AccNeverRefresh.ValueBool(),
	}
}

// readInto fetches the promoted dataset by id and overlays the result onto
// model. Returns found=false on a 404 (deleted/un-promoted out of band).
func (r *promotedDatasetResource) readInto(ctx context.Context, id string, model *promotedDatasetResourceModel) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	pds, err := r.client.GetPhysicalDataset(id)
	if isNotFoundError(err) {
		return false, diags
	}
	if err != nil {
		diags.AddError("Unable to read Dremio promoted dataset", err.Error())
		return false, diags
	}

	model.ID = types.StringValue(id)

	sourceID, relativePath, d := deriveSourceIDAndRelativePath(ctx, r.client, pds.Path)
	diags.Append(d...)
	if diags.HasError() {
		return true, diags
	}
	model.SourceID = types.StringValue(sourceID)
	model.RelativePath = relativePath

	format := physicalDatasetFormatFromAPI(pds.Format)
	model.Type = types.StringValue(format.Type)
	model.FieldDelimiter = types.StringValue(format.FieldDelimiter)
	model.LineDelimiter = types.StringValue(format.LineDelimiter)
	model.Quote = types.StringValue(format.Quote)
	model.Comment = types.StringValue(format.Comment)
	model.Escape = types.StringValue(format.Escape)
	model.SkipFirstLine = types.BoolValue(format.SkipFirstLine)
	model.ExtractHeader = types.BoolValue(format.ExtractHeader)
	model.TrimHeader = types.BoolValue(format.TrimHeader)
	model.AutoGenerateColumnNames = types.BoolValue(format.AutoGenerateColumnNames)
	model.SheetName = types.StringValue(format.SheetName)
	model.HasMergedCells = types.BoolValue(format.HasMergedCells)

	policy := accelerationPolicyFromAPI(pds.AccelerationRefreshPolicy)
	model.AccRefreshPeriodMs = types.Int64Value(policy.RefreshPeriodMs)
	model.AccGracePeriodMs = types.Int64Value(policy.GracePeriodMs)
	model.AccMethod = types.StringValue(policy.Method)
	model.AccRefreshField = types.StringValue(policy.RefreshField)
	model.AccNeverExpire = types.BoolValue(policy.NeverExpire)
	model.AccNeverRefresh = types.BoolValue(policy.NeverRefresh)

	fields, d := datasetFieldsToList(ctx, pds.Fields)
	diags.Append(d...)
	model.Fields = fields

	pathList, d := types.ListValueFrom(ctx, types.StringType, pds.Path)
	diags.Append(d...)
	model.Path = pathList

	model.QueryPath = types.StringValue(getQueryPath(pds.Path))

	return true, diags
}
