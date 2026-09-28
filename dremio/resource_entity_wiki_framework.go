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

// entityWikiResourceModel mirrors the SDKv2 dremio_entity_wiki schema
// exactly - no resource shows up in any real state, but keeping the
// contract identical means no config needs to change to adopt the new
// binary.
type entityWikiResourceModel struct {
	ID       types.String `tfsdk:"id"`
	EntityID types.String `tfsdk:"entity_id"`
	Text     types.String `tfsdk:"text"`
}

type entityWikiResource struct {
	client *dapi.Client
}

var (
	_ resource.Resource                = &entityWikiResource{}
	_ resource.ResourceWithConfigure   = &entityWikiResource{}
	_ resource.ResourceWithImportState = &entityWikiResource{}
)

func NewEntityWikiResource() resource.Resource {
	return &entityWikiResource{}
}

func (r *entityWikiResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_entity_wiki"
}

func (r *entityWikiResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *entityWikiResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *entityWikiResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
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
			"text": schema.StringAttribute{
				Required: true,
			},
		},
	}
}

func (r *entityWikiResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan entityWikiResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	entityID := plan.EntityID.ValueString()

	// The entity may already have a wiki (e.g. set through the UI) with a
	// version the API needs for its optimistic-concurrency check; a fresh
	// entity with none yet has no version to send, matching the SDKv2
	// resource's "assume no wiki exists" fallback (version 0).
	version := 0
	if existing, err := r.client.GetEntityWiki(entityID); err == nil {
		version = existing.Version
	}

	if err := r.client.SetEntityWiki(entityID, plan.Text.ValueString(), version); err != nil {
		resp.Diagnostics.AddError("Unable to set Dremio entity wiki", err.Error())
		return
	}

	plan.ID = types.StringValue(entityID)
	found, diags := r.readInto(ctx, entityID, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Entity disappeared right after setting wiki", entityID)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *entityWikiResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state entityWikiResourceModel
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

func (r *entityWikiResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan entityWikiResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state entityWikiResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	entityID := state.ID.ValueString()
	existing, err := r.client.GetEntityWiki(entityID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read current Dremio entity wiki", err.Error())
		return
	}

	if err := r.client.SetEntityWiki(entityID, plan.Text.ValueString(), existing.Version); err != nil {
		resp.Diagnostics.AddError("Unable to update Dremio entity wiki", err.Error())
		return
	}

	plan.ID = state.ID
	found, diags := r.readInto(ctx, entityID, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Entity disappeared right after updating wiki", entityID)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete clears the entity's wiki text rather than removing anything -
// there's no separate "wiki object" in Dremio to delete, just text on the
// entity itself, so this matches the SDKv2 resource's behavior. If the
// entity is already gone, that's fine: nothing to clear.
func (r *entityWikiResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state entityWikiResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	entityID := state.ID.ValueString()
	existing, err := r.client.GetEntityWiki(entityID)
	if err != nil {
		return
	}
	if err := r.client.SetEntityWiki(entityID, "", existing.Version); err != nil {
		resp.Diagnostics.AddError("Unable to clear Dremio entity wiki", err.Error())
	}
}

// readInto fetches the entity's wiki and overlays the result onto model.
// Returns found=false on a 404 (entity deleted out of band).
//
// The SDKv2 resource had a real bug here: GetEntityWiki returns a WikiBody
// struct ({Text, Version}), but its Read did `d.Set("text", text)` with the
// whole struct instead of `text.Text` - the variable was misleadingly named
// `text`. Fixed here by using the struct's Text field explicitly.
func (r *entityWikiResource) readInto(ctx context.Context, entityID string, model *entityWikiResourceModel) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	wikiBody, err := r.client.GetEntityWiki(entityID)
	if isNotFoundError(err) {
		return false, diags
	}
	if err != nil {
		diags.AddError("Unable to read Dremio entity wiki", err.Error())
		return false, diags
	}

	model.ID = types.StringValue(entityID)
	model.EntityID = types.StringValue(entityID)
	model.Text = types.StringValue(wikiBody.Text)

	return true, diags
}
