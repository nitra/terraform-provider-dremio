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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

// rawReflectionResourceModel has no legacy SDKv2 state to stay compatible
// with (dremio_raw_reflection was never used in any real state - see the
// migration plan), so unlike dremio_source this is a fresh design: plain
// list attributes instead of a block, no shared-schema-across-types
// concerns.
type rawReflectionResourceModel struct {
	ID                            types.String `tfsdk:"id"`
	DatasetID                     types.String `tfsdk:"dataset_id"`
	Name                          types.String `tfsdk:"name"`
	Enabled                       types.Bool   `tfsdk:"enabled"`
	DisplayFields                 types.List   `tfsdk:"display_fields"`
	DistributionFields            types.List   `tfsdk:"distribution_fields"`
	PartitionFields               types.List   `tfsdk:"partition_fields"`
	SortFields                    types.List   `tfsdk:"sort_fields"`
	PartitionDistributionStrategy types.String `tfsdk:"partition_distribution_strategy"`
}

type rawReflectionResource struct {
	client *dapi.Client
}

var (
	_ resource.Resource                = &rawReflectionResource{}
	_ resource.ResourceWithConfigure   = &rawReflectionResource{}
	_ resource.ResourceWithImportState = &rawReflectionResource{}
)

func NewRawReflectionResource() resource.Resource {
	return &rawReflectionResource{}
}

func (r *rawReflectionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_raw_reflection"
}

func (r *rawReflectionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *rawReflectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *rawReflectionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	stringListAttr := func() schema.Attribute {
		return schema.ListAttribute{Optional: true, ElementType: types.StringType}
	}

	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"dataset_id": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Description: "Prefer referencing the dremio_dataset data source over a " +
					"hardcoded id (see its docs for why, and the required " +
					"lifecycle { create_before_destroy = true } on this resource).",
			},
			"name": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString("Raw"),
			},
			"enabled": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},
			"display_fields": schema.ListAttribute{
				Required:    true,
				ElementType: types.StringType,
			},
			"distribution_fields": stringListAttr(),
			"partition_fields":    stringListAttr(),
			"sort_fields":         stringListAttr(),
			"partition_distribution_strategy": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString("CONSOLIDATED"),
			},
		},
	}
}

func (r *rawReflectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan rawReflectionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	spec, diags := reflectionSpecFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.NewRawReflection(plan.DatasetID.ValueString(), spec)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Dremio raw reflection", err.Error())
		return
	}

	found, diags := r.readInto(ctx, created.Id, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Reflection disappeared right after creation", created.Id)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *rawReflectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state rawReflectionResourceModel
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

func (r *rawReflectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan rawReflectionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state rawReflectionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	spec, diags := reflectionSpecFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.UpdateRawReflection(state.ID.ValueString(), spec)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update Dremio raw reflection", err.Error())
		return
	}

	plan.ID = state.ID
	found, diags := r.readInto(ctx, state.ID.ValueString(), &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Reflection disappeared right after update", state.ID.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *rawReflectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state rawReflectionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteReflection(state.ID.ValueString())
	if err != nil && !isNotFoundError(err) {
		resp.Diagnostics.AddError("Unable to delete Dremio raw reflection", err.Error())
	}
}

// readInto fetches the reflection by id and overlays the result onto model.
// Returns found=false on a 404 (deleted out of band).
func (r *rawReflectionResource) readInto(ctx context.Context, id string, model *rawReflectionResourceModel) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	res, err := r.client.GetRawReflection(id)
	if isNotFoundError(err) {
		return false, diags
	}
	if err != nil {
		diags.AddError("Unable to read Dremio raw reflection", err.Error())
		return false, diags
	}

	model.ID = types.StringValue(id)
	model.DatasetID = types.StringValue(res.DatasetId)
	model.Name = types.StringValue(res.Name)
	model.Enabled = types.BoolValue(res.Enabled)
	model.PartitionDistributionStrategy = types.StringValue(res.PartitionDistributionStrategy)

	displayFields, d := reflectionFieldsToList(ctx, res.DisplayFields)
	diags.Append(d...)
	model.DisplayFields = displayFields

	distributionFields, d := reflectionFieldsToList(ctx, res.DistributionFields)
	diags.Append(d...)
	model.DistributionFields = distributionFields

	partitionFields, d := reflectionFieldsToList(ctx, res.PartitionFields)
	diags.Append(d...)
	model.PartitionFields = partitionFields

	sortFields, d := reflectionFieldsToList(ctx, res.SortFields)
	diags.Append(d...)
	model.SortFields = sortFields

	return true, diags
}

// reflectionSpecFromModel builds the API spec from a plan/config model.
func reflectionSpecFromModel(ctx context.Context, model rawReflectionResourceModel) (*dapi.RawReflectionSpec, diag.Diagnostics) {
	var diags diag.Diagnostics

	displayFields, d := listToReflectionFields(ctx, model.DisplayFields)
	diags.Append(d...)
	distributionFields, d := listToReflectionFields(ctx, model.DistributionFields)
	diags.Append(d...)
	partitionFields, d := listToReflectionFields(ctx, model.PartitionFields)
	diags.Append(d...)
	sortFields, d := listToReflectionFields(ctx, model.SortFields)
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}

	return &dapi.RawReflectionSpec{
		Name:                          model.Name.ValueString(),
		Enabled:                       model.Enabled.ValueBool(),
		DisplayFields:                 displayFields,
		DistributionFields:            distributionFields,
		PartitionFields:               partitionFields,
		SortFields:                    sortFields,
		PartitionDistributionStrategy: model.PartitionDistributionStrategy.ValueString(),
	}, diags
}

// listToReflectionFields converts a types.List of field names (may be null,
// e.g. distribution_fields left unset) into the API client's field slice.
func listToReflectionFields(ctx context.Context, l types.List) ([]dapi.ReflectionField, diag.Diagnostics) {
	if l.IsNull() || l.IsUnknown() {
		return nil, nil
	}
	var names []string
	diags := l.ElementsAs(ctx, &names, false)
	fields := make([]dapi.ReflectionField, len(names))
	for i, n := range names {
		fields[i] = dapi.ReflectionField{Name: n}
	}
	return fields, diags
}

// reflectionFieldsToList is the reverse of listToReflectionFields. An empty
// (but non-nil Go) slice from the API still produces a null list here when
// there are zero fields, matching how Dremio omits empty field lists from
// the JSON response entirely (e.g. distributionFields/partitionFields/
// sortFields for a reflection that only sets displayFields).
func reflectionFieldsToList(ctx context.Context, fields []dapi.ReflectionField) (types.List, diag.Diagnostics) {
	if len(fields) == 0 {
		return types.ListNull(types.StringType), nil
	}
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	return types.ListValueFrom(ctx, types.StringType, names)
}
