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

// aggrReflectionResourceModel mirrors the SDKv2 dremio_aggr_reflection schema
// exactly (same field names/required-ness/defaults) - no real state uses this
// resource, but keeping the contract identical means no data source needs to
// change anything to adopt the new binary.
type aggrReflectionResourceModel struct {
	ID                            types.String `tfsdk:"id"`
	DatasetID                     types.String `tfsdk:"dataset_id"`
	Name                          types.String `tfsdk:"name"`
	Enabled                       types.Bool   `tfsdk:"enabled"`
	DimensionFields               types.List   `tfsdk:"dimension_fields"`
	TimestampDateDimensionFields  types.List   `tfsdk:"timestamp_date_dimension_fields"`
	MeasureFieldsSum              types.List   `tfsdk:"measure_fields_sum"`
	DistributionFields            types.List   `tfsdk:"distribution_fields"`
	PartitionFields               types.List   `tfsdk:"partition_fields"`
	SortFields                    types.List   `tfsdk:"sort_fields"`
	PartitionDistributionStrategy types.String `tfsdk:"partition_distribution_strategy"`
}

type aggrReflectionResource struct {
	client *dapi.Client
}

var (
	_ resource.Resource                = &aggrReflectionResource{}
	_ resource.ResourceWithConfigure   = &aggrReflectionResource{}
	_ resource.ResourceWithImportState = &aggrReflectionResource{}
)

func NewAggrReflectionResource() resource.Resource {
	return &aggrReflectionResource{}
}

func (r *aggrReflectionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_aggr_reflection"
}

func (r *aggrReflectionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *aggrReflectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *aggrReflectionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
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
			},
			"name": schema.StringAttribute{
				Required: true,
			},
			"enabled": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},
			"dimension_fields": schema.ListAttribute{
				Required:    true,
				ElementType: types.StringType,
			},
			"timestamp_date_dimension_fields": stringListAttr(),
			"measure_fields_sum":              stringListAttr(),
			"distribution_fields":             stringListAttr(),
			"partition_fields":                stringListAttr(),
			"sort_fields":                     stringListAttr(),
			"partition_distribution_strategy": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString("CONSOLIDATED"),
			},
		},
	}
}

func (r *aggrReflectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan aggrReflectionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	spec, diags := aggrReflectionSpecFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.NewAggregationReflection(plan.DatasetID.ValueString(), spec)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Dremio aggregation reflection", err.Error())
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

func (r *aggrReflectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state aggrReflectionResourceModel
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

func (r *aggrReflectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan aggrReflectionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state aggrReflectionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	spec, diags := aggrReflectionSpecFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.UpdateAggregationReflection(state.ID.ValueString(), spec)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update Dremio aggregation reflection", err.Error())
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

func (r *aggrReflectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state aggrReflectionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteReflection(state.ID.ValueString())
	if err != nil && !isNotFoundError(err) {
		resp.Diagnostics.AddError("Unable to delete Dremio aggregation reflection", err.Error())
	}
}

// readInto fetches the reflection by id and overlays the result onto model.
// Returns found=false on a 404 (deleted out of band).
func (r *aggrReflectionResource) readInto(ctx context.Context, id string, model *aggrReflectionResourceModel) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	res, err := r.client.GetAggregationReflection(id)
	if isNotFoundError(err) {
		return false, diags
	}
	if err != nil {
		diags.AddError("Unable to read Dremio aggregation reflection", err.Error())
		return false, diags
	}

	model.ID = types.StringValue(id)
	model.DatasetID = types.StringValue(res.DatasetId)
	model.Name = types.StringValue(res.Name)
	model.Enabled = types.BoolValue(res.Enabled)
	model.PartitionDistributionStrategy = types.StringValue(res.PartitionDistributionStrategy)

	dimensionFields, timestampDateDimensionFields, d := dimensionFieldsToLists(ctx, res.DimensionFields)
	diags.Append(d...)
	model.DimensionFields = dimensionFields
	model.TimestampDateDimensionFields = timestampDateDimensionFields

	measureFieldsSum, d := measureFieldsToList(ctx, res.MeasureFields)
	diags.Append(d...)
	model.MeasureFieldsSum = measureFieldsSum

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

// aggrReflectionSpecFromModel builds the API spec from a plan/config model.
func aggrReflectionSpecFromModel(ctx context.Context, model aggrReflectionResourceModel) (*dapi.AggregationReflectionSpec, diag.Diagnostics) {
	var diags diag.Diagnostics

	dimensionFields, d := listToDimensionFields(ctx, model.DimensionFields, model.TimestampDateDimensionFields)
	diags.Append(d...)
	measureFields, d := listToMeasureFields(ctx, model.MeasureFieldsSum)
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

	return &dapi.AggregationReflectionSpec{
		Name:                          model.Name.ValueString(),
		Enabled:                       model.Enabled.ValueBool(),
		DimensionFields:               dimensionFields,
		MeasureFields:                 measureFields,
		DistributionFields:            distributionFields,
		PartitionFields:               partitionFields,
		SortFields:                    sortFields,
		PartitionDistributionStrategy: model.PartitionDistributionStrategy.ValueString(),
	}, diags
}

// listToDimensionFields merges dimension_fields (NORMAL granularity) and
// timestamp_date_dimension_fields (DATE granularity) into the API's single
// dimensionFields list, mirroring the SDKv2 getDimensionFields split.
func listToDimensionFields(ctx context.Context, dimList, tdList types.List) ([]dapi.ReflectionFieldWithGranularity, diag.Diagnostics) {
	var diags diag.Diagnostics

	var dimNames []string
	if !dimList.IsNull() && !dimList.IsUnknown() {
		diags.Append(dimList.ElementsAs(ctx, &dimNames, false)...)
	}
	var tdNames []string
	if !tdList.IsNull() && !tdList.IsUnknown() {
		diags.Append(tdList.ElementsAs(ctx, &tdNames, false)...)
	}
	if diags.HasError() {
		return nil, diags
	}

	fields := make([]dapi.ReflectionFieldWithGranularity, 0, len(dimNames)+len(tdNames))
	for _, name := range dimNames {
		fields = append(fields, dapi.ReflectionFieldWithGranularity{
			ReflectionField: dapi.ReflectionField{Name: name},
			Granularity:     "NORMAL",
		})
	}
	for _, name := range tdNames {
		fields = append(fields, dapi.ReflectionFieldWithGranularity{
			ReflectionField: dapi.ReflectionField{Name: name},
			Granularity:     "DATE",
		})
	}
	return fields, diags
}

// dimensionFieldsToLists is the reverse of listToDimensionFields, splitting
// by granularity back into the two separate terraform attributes. A zero
// count on either side produces a null list, matching how Dremio omits
// empty field lists from the JSON response.
func dimensionFieldsToLists(ctx context.Context, fields []dapi.ReflectionFieldWithGranularity) (types.List, types.List, diag.Diagnostics) {
	var diags diag.Diagnostics

	var dimNames, tdNames []string
	for _, f := range fields {
		switch f.Granularity {
		case "DATE":
			tdNames = append(tdNames, f.Name)
		default:
			dimNames = append(dimNames, f.Name)
		}
	}

	dimList := types.ListNull(types.StringType)
	if len(dimNames) > 0 {
		var d diag.Diagnostics
		dimList, d = types.ListValueFrom(ctx, types.StringType, dimNames)
		diags.Append(d...)
	}
	tdList := types.ListNull(types.StringType)
	if len(tdNames) > 0 {
		var d diag.Diagnostics
		tdList, d = types.ListValueFrom(ctx, types.StringType, tdNames)
		diags.Append(d...)
	}
	return dimList, tdList, diags
}

// listToMeasureFields converts measure_fields_sum into the API's measure
// field list, always tagged with the SUM measure type (the only one this
// resource has ever exposed - mirrors the SDKv2 getMeasureFields).
func listToMeasureFields(ctx context.Context, l types.List) ([]dapi.ReflectionMeasureField, diag.Diagnostics) {
	if l.IsNull() || l.IsUnknown() {
		return nil, nil
	}
	var names []string
	diags := l.ElementsAs(ctx, &names, false)
	fields := make([]dapi.ReflectionMeasureField, len(names))
	for i, n := range names {
		fields[i] = dapi.ReflectionMeasureField{
			ReflectionField: dapi.ReflectionField{Name: n},
			MeasureTypeList: []string{"SUM"},
		}
	}
	return fields, diags
}

// measureFieldsToList is the reverse of listToMeasureFields: only fields
// whose measureTypeList contains SUM are surfaced (mirrors setMeasureFields).
func measureFieldsToList(ctx context.Context, fields []dapi.ReflectionMeasureField) (types.List, diag.Diagnostics) {
	var names []string
	for _, f := range fields {
		for _, m := range f.MeasureTypeList {
			if m == "SUM" {
				names = append(names, f.Name)
			}
		}
	}
	if len(names) == 0 {
		return types.ListNull(types.StringType), nil
	}
	return types.ListValueFrom(ctx, types.StringType, names)
}
