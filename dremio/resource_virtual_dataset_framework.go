package dremio

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

// virtualDatasetResourceModel mirrors the SDKv2 dremio_virtual_dataset
// schema exactly - no resource shows up in any real state, but keeping the
// contract identical means no config needs to change to adopt the new
// binary.
type virtualDatasetResourceModel struct {
	ID         types.String `tfsdk:"id"`
	ParentID   types.String `tfsdk:"parent_id"`
	Name       types.String `tfsdk:"name"`
	Sql        types.String `tfsdk:"sql"`
	SqlContext types.List   `tfsdk:"sql_context"`
	Fields     types.List   `tfsdk:"fields"`
	Path       types.List   `tfsdk:"path"`
	QueryPath  types.String `tfsdk:"query_path"`
}

type datasetFieldModel struct {
	Name types.String `tfsdk:"name"`
	Type types.String `tfsdk:"type"`
}

var datasetFieldAttrTypes = map[string]attr.Type{
	"name": types.StringType,
	"type": types.StringType,
}

type virtualDatasetResource struct {
	client *dapi.Client
}

var (
	_ resource.Resource                = &virtualDatasetResource{}
	_ resource.ResourceWithConfigure   = &virtualDatasetResource{}
	_ resource.ResourceWithImportState = &virtualDatasetResource{}
)

func NewVirtualDatasetResource() resource.Resource {
	return &virtualDatasetResource{}
}

func (r *virtualDatasetResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_dataset"
}

func (r *virtualDatasetResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *virtualDatasetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *virtualDatasetResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"parent_id": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"sql": schema.StringAttribute{
				Required: true,
			},
			"sql_context": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
			},
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
		},
	}
}

func (r *virtualDatasetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan virtualDatasetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parent, err := r.client.GetCatalogEntityById(plan.ParentID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to resolve Dremio virtual dataset parent", err.Error())
		return
	}

	var sqlContext []string
	if !plan.SqlContext.IsNull() && !plan.SqlContext.IsUnknown() {
		resp.Diagnostics.Append(plan.SqlContext.ElementsAs(ctx, &sqlContext, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	inputPath := append(append([]string{}, parent.Path...), plan.Name.ValueString())

	created, err := r.client.NewVirtualDataset(&dapi.NewVirtualDatasetSpec{
		Path:       inputPath,
		Sql:        plan.Sql.ValueString(),
		SqlContext: sqlContext,
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Dremio virtual dataset", err.Error())
		return
	}

	found, diags := r.readInto(ctx, created.Id, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Virtual dataset disappeared right after creation", created.Id)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *virtualDatasetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state virtualDatasetResourceModel
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

func (r *virtualDatasetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan virtualDatasetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state virtualDatasetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var sqlContext []string
	if !plan.SqlContext.IsNull() && !plan.SqlContext.IsUnknown() {
		resp.Diagnostics.Append(plan.SqlContext.ElementsAs(ctx, &sqlContext, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	_, err := r.client.UpdateVirtualDataset(state.ID.ValueString(), &dapi.UpdateVirtualDatasetSpec{
		Sql:        plan.Sql.ValueString(),
		SqlContext: sqlContext,
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to update Dremio virtual dataset", err.Error())
		return
	}

	plan.ID = state.ID
	found, diags := r.readInto(ctx, state.ID.ValueString(), &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Virtual dataset disappeared right after update", state.ID.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *virtualDatasetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state virtualDatasetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteCatalogItem(state.ID.ValueString())
	if err != nil && !isNotFoundError(err) {
		resp.Diagnostics.AddError("Unable to delete Dremio virtual dataset", err.Error())
	}
}

// readInto fetches the virtual dataset by id and overlays the result onto
// model. Returns found=false on a 404 (deleted out of band).
//
// parent_id/name aren't part of the API response (Dremio only returns the
// full path), so they're derived from the returned path by resolving the
// parent's id via GetCatalogEntityByPath. This matters most for `tofu
// import`: the SDKv2 resource's Read never set them, which left them empty
// after import and forced a spurious destroy+recreate on the very next plan
// (both are RequiresReplace) - dangerous for a resource whose main
// point is bringing hand-built, already-in-use datasets under management.
func (r *virtualDatasetResource) readInto(ctx context.Context, id string, model *virtualDatasetResourceModel) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	vds, err := r.client.GetVirtualDataset(id)
	if isNotFoundError(err) {
		return false, diags
	}
	if err != nil {
		diags.AddError("Unable to read Dremio virtual dataset", err.Error())
		return false, diags
	}

	model.ID = types.StringValue(id)
	model.Sql = types.StringValue(vds.Sql)

	if len(vds.Path) < 2 {
		diags.AddError("Unexpected Dremio virtual dataset path", fmt.Sprintf("path %v has fewer than 2 elements, can't determine a parent", vds.Path))
		return true, diags
	}
	model.Name = types.StringValue(vds.Path[len(vds.Path)-1])
	parent, err := r.client.GetCatalogEntityByPath(vds.Path[:len(vds.Path)-1])
	if err != nil {
		diags.AddError("Unable to resolve Dremio virtual dataset parent", err.Error())
		return true, diags
	}
	model.ParentID = types.StringValue(parent.Id)

	sqlContext, d := stringsToOptionalList(ctx, vds.SqlContext)
	diags.Append(d...)
	model.SqlContext = sqlContext

	fields, d := datasetFieldsToList(ctx, vds.Fields)
	diags.Append(d...)
	model.Fields = fields

	pathList, d := types.ListValueFrom(ctx, types.StringType, vds.Path)
	diags.Append(d...)
	model.Path = pathList

	model.QueryPath = types.StringValue(getQueryPath(vds.Path))

	return true, diags
}

// stringsToOptionalList converts an API string slice into a types.List for
// an Optional (not Computed) attribute. An empty/nil slice maps to a null
// list rather than an empty one - Dremio omits sql_context from the JSON
// response entirely when it's unset (SqlContext has omitempty), and a
// non-null value here for a config that never set it would make Terraform
// report "produced inconsistent result after apply".
func stringsToOptionalList(ctx context.Context, items []string) (types.List, diag.Diagnostics) {
	if len(items) == 0 {
		return types.ListNull(types.StringType), nil
	}
	return types.ListValueFrom(ctx, types.StringType, items)
}

// datasetFieldsToList converts the API's dataset field list (name + type)
// into the fields attribute's nested-object list.
func datasetFieldsToList(ctx context.Context, apiFields []dapi.DatasetField) (types.List, diag.Diagnostics) {
	fieldModels := make([]datasetFieldModel, len(apiFields))
	for i, f := range apiFields {
		fieldModels[i] = datasetFieldModel{
			Name: types.StringValue(f.Name),
			Type: types.StringValue(f.Type.Name),
		}
	}
	return types.ListValueFrom(ctx, types.ObjectType{AttrTypes: datasetFieldAttrTypes}, fieldModels)
}
