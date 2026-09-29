package dremio

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

// physicalDatasetResourceModel mirrors the SDKv2 dremio_physical_dataset
// schema exactly - no resource shows up in any real state, but keeping the
// contract identical means no config needs to change to adopt the new
// binary.
//
// This resource doesn't create or delete anything in Dremio: it adopts an
// already-existing physical file/table at source_id+relative_path (Create
// resolves it by path and takes over its id) and only ever configures its
// acceleration refresh policy. Delete clears that policy rather than
// removing the entity - matching the SDKv2 resource, which called
// UpdatePhysicalDataset with an empty spec, never DeleteCatalogItem.
type physicalDatasetResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	SourceID           types.String `tfsdk:"source_id"`
	RelativePath       types.List   `tfsdk:"relative_path"`
	AccRefreshPeriodMs types.Int64  `tfsdk:"acc_refresh_period_ms"`
	AccGracePeriodMs   types.Int64  `tfsdk:"acc_grace_period_ms"`
	AccMethod          types.String `tfsdk:"acc_method"`
	AccRefreshField    types.String `tfsdk:"acc_refresh_field"`
	AccNeverExpire     types.Bool   `tfsdk:"acc_never_expire"`
	AccNeverRefresh    types.Bool   `tfsdk:"acc_never_refresh"`
	Fields             types.List   `tfsdk:"fields"`
	Path               types.List   `tfsdk:"path"`
	QueryPath          types.String `tfsdk:"query_path"`
}

type physicalDatasetResource struct {
	client *dapi.Client
}

var (
	_ resource.Resource                = &physicalDatasetResource{}
	_ resource.ResourceWithConfigure   = &physicalDatasetResource{}
	_ resource.ResourceWithImportState = &physicalDatasetResource{}
)

func NewPhysicalDatasetResource() resource.Resource {
	return &physicalDatasetResource{}
}

func (r *physicalDatasetResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_physical_dataset"
}

func (r *physicalDatasetResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *physicalDatasetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *physicalDatasetResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
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

func (r *physicalDatasetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan physicalDatasetResourceModel
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
		resp.Diagnostics.AddError("Unable to find Dremio physical dataset target", err.Error())
		return
	}

	r.applyAcceleration(ctx, target.Id, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *physicalDatasetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state physicalDatasetResourceModel
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

func (r *physicalDatasetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan physicalDatasetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state physicalDatasetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = state.ID
	r.applyAcceleration(ctx, state.ID.ValueString(), &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete clears the acceleration policy rather than removing anything -
// matches the SDKv2 resource: this is an adopted file/table, not something
// Terraform created, so there's nothing to delete.
func (r *physicalDatasetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state physicalDatasetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.UpdatePhysicalDataset(state.ID.ValueString(), &dapi.UpdatePhysicalDatasetSpec{})
	if err != nil && !isNotFoundError(err) {
		resp.Diagnostics.AddError("Unable to clear Dremio physical dataset acceleration policy", err.Error())
	}
}

// applyAcceleration pushes the plan's acceleration policy to Dremio and
// refreshes model from the result. Shared by Create and Update, mirroring
// how the SDKv2 resource's Create just called through to Update.
func (r *physicalDatasetResource) applyAcceleration(ctx context.Context, id string, model *physicalDatasetResourceModel, diags *diag.Diagnostics) {
	policy := accelerationPolicyToAPI(accelerationPolicyValues{
		RefreshPeriodMs: model.AccRefreshPeriodMs.ValueInt64(),
		GracePeriodMs:   model.AccGracePeriodMs.ValueInt64(),
		Method:          model.AccMethod.ValueString(),
		RefreshField:    model.AccRefreshField.ValueString(),
		NeverExpire:     model.AccNeverExpire.ValueBool(),
		NeverRefresh:    model.AccNeverRefresh.ValueBool(),
	})

	_, err := r.client.UpdatePhysicalDataset(id, &dapi.UpdatePhysicalDatasetSpec{AccelerationRefreshPolicy: policy})
	if err != nil {
		diags.AddError("Unable to configure Dremio physical dataset", err.Error())
		return
	}

	found, d := r.readInto(ctx, id, model)
	diags.Append(d...)
	if !diags.HasError() && !found {
		diags.AddError("Physical dataset disappeared right after configuring", id)
	}
}

// readInto fetches the physical dataset by id and overlays the result onto
// model. Returns found=false on a 404 (deleted out of band).
func (r *physicalDatasetResource) readInto(ctx context.Context, id string, model *physicalDatasetResourceModel) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	pds, err := r.client.GetPhysicalDataset(id)
	if isNotFoundError(err) {
		return false, diags
	}
	if err != nil {
		diags.AddError("Unable to read Dremio physical dataset", err.Error())
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
