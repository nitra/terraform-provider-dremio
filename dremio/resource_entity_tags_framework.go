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

// entityTagsResourceModel mirrors the SDKv2 dremio_entity_tags schema
// exactly - no resource shows up in any real state, but keeping the
// contract identical means no config needs to change to adopt the new
// binary.
type entityTagsResourceModel struct {
	ID       types.String `tfsdk:"id"`
	EntityID types.String `tfsdk:"entity_id"`
	Tags     types.List   `tfsdk:"tags"`
}

type entityTagsResource struct {
	client *dapi.Client
}

var (
	_ resource.Resource                = &entityTagsResource{}
	_ resource.ResourceWithConfigure   = &entityTagsResource{}
	_ resource.ResourceWithImportState = &entityTagsResource{}
)

func NewEntityTagsResource() resource.Resource {
	return &entityTagsResource{}
}

func (r *entityTagsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_entity_tags"
}

func (r *entityTagsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *entityTagsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *entityTagsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"entity_id": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"tags": schema.ListAttribute{
				Required:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *entityTagsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan entityTagsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	entityID := plan.EntityID.ValueString()
	var tags []string
	resp.Diagnostics.Append(plan.Tags.ElementsAs(ctx, &tags, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The entity may already have tags (e.g. set through the UI) with a
	// version the API needs for its optimistic-concurrency check; a fresh
	// entity with none yet has no version to send, matching the SDKv2
	// resource's "assume no tags exist" fallback.
	version := ""
	if existing, err := r.client.GetEntityTags(entityID); err == nil {
		version = existing.Version
	}

	if err := r.client.SetEntityTags(entityID, tags, version); err != nil {
		resp.Diagnostics.AddError("Unable to set Dremio entity tags", err.Error())
		return
	}

	plan.ID = types.StringValue(entityID)
	found, diags := r.readInto(ctx, entityID, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Entity disappeared right after tagging", entityID)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *entityTagsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state entityTagsResourceModel
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

func (r *entityTagsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan entityTagsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state entityTagsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	entityID := state.ID.ValueString()
	var tags []string
	resp.Diagnostics.Append(plan.Tags.ElementsAs(ctx, &tags, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	existing, err := r.client.GetEntityTags(entityID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read current Dremio entity tags", err.Error())
		return
	}

	if err := r.client.SetEntityTags(entityID, tags, existing.Version); err != nil {
		resp.Diagnostics.AddError("Unable to update Dremio entity tags", err.Error())
		return
	}

	plan.ID = state.ID
	found, diags := r.readInto(ctx, entityID, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Entity disappeared right after updating tags", entityID)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete clears the entity's tags rather than removing anything - there's
// no separate "tags object" in Dremio to delete, just a list on the entity
// itself, so this matches the SDKv2 resource's behavior. If the entity is
// already gone, that's fine: nothing to clear.
func (r *entityTagsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state entityTagsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	entityID := state.ID.ValueString()
	existing, err := r.client.GetEntityTags(entityID)
	if err != nil {
		return
	}
	if err := r.client.SetEntityTags(entityID, []string{}, existing.Version); err != nil {
		resp.Diagnostics.AddError("Unable to clear Dremio entity tags", err.Error())
	}
}

// readInto fetches the entity's tags and overlays the result onto model.
// Returns found=false on a 404 (entity deleted out of band).
func (r *entityTagsResource) readInto(ctx context.Context, entityID string, model *entityTagsResourceModel) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	tagsBody, err := r.client.GetEntityTags(entityID)
	if isNotFoundError(err) {
		return false, diags
	}
	if err != nil {
		diags.AddError("Unable to read Dremio entity tags", err.Error())
		return false, diags
	}

	model.ID = types.StringValue(entityID)
	model.EntityID = types.StringValue(entityID)

	tags, d := types.ListValueFrom(ctx, types.StringType, tagsBody.Tags)
	diags.Append(d...)
	model.Tags = tags

	return true, diags
}
