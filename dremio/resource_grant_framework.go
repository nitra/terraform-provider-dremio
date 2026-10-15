package dremio

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

type grantModel struct {
	ID          types.String `tfsdk:"id"`
	ObjectType  types.String `tfsdk:"object_type"`
	ObjectID    types.String `tfsdk:"object_id"`
	GranteeType types.String `tfsdk:"grantee_type"`
	GranteeID   types.String `tfsdk:"grantee_id"`
	Privilege   types.String `tfsdk:"privilege"`
	Tag         types.String `tfsdk:"tag"`
}

type grantResource struct{ roleResource }

var (
	_ resource.Resource                = &grantResource{}
	_ resource.ResourceWithConfigure   = &grantResource{}
	_ resource.ResourceWithImportState = &grantResource{}
)

func NewGrantResource() resource.Resource { return &grantResource{} }
func (r *grantResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_grant"
}
func (r *grantResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{Description: "One explicit concrete privilege in the experimental OSS RBAC fork. Requires active ADMIN or exact-object MANAGE_GRANTS and policy OCC. Uses stable object/principal IDs; never adopts by name. Delete revokes only this privilege, not other or inherited access. ALL, bulk scopes and ownership are outside this resource. Existing grants require import.", Attributes: map[string]schema.Attribute{
		"id":           schema.StringAttribute{Computed: true, Description: "Canonical base64url without padding of compact JSON [object_type, object_id, grantee_type, grantee_id, privilege].", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"object_type":  schema.StringAttribute{Required: true, Description: "PROJECT (SYSTEM), SOURCE, SPACE, FOLDER, PDS, VDS, FUNCTION, VERSIONED_PDS or VERSIONED_VDS. Server validates the live target on create.", PlanModifiers: replace},
		"object_id":    schema.StringAttribute{Required: true, Description: "Stable namespace ID, SYSTEM for PROJECT, or the server-resolved base64url versioned dataset ID. Object recreation requires a new explicit grant.", PlanModifiers: replace},
		"grantee_type": schema.StringAttribute{Required: true, Description: "USER or ROLE.", PlanModifiers: replace, Validators: []validator.String{stringvalidator.OneOf("USER", "ROLE")}},
		"grantee_id":   schema.StringAttribute{Required: true, Description: "Stable user UID or role ID, never display name.", PlanModifiers: replace},
		"privilege":    schema.StringAttribute{Required: true, Description: "One existing Dremio privilege enum name, validated against the object type on the server. ALL is unsupported.", PlanModifiers: replace, Validators: []validator.String{stringvalidator.NoneOf("ALL")}},
		"tag":          schema.StringAttribute{Computed: true, Description: "Observed global policy OCC version."},
	}}
}

func (m grantModel) grant() dapi.RBACGrant {
	return dapi.RBACGrant{ObjectType: m.ObjectType.ValueString(), ObjectID: m.ObjectID.ValueString(), GranteeType: m.GranteeType.ValueString(), GranteeID: m.GranteeID.ValueString(), Privilege: m.Privilege.ValueString()}
}
func (m *grantModel) read(g *dapi.RBACGrant) {
	id, _ := g.Key()
	m.ID = types.StringValue(id)
	m.ObjectType = types.StringValue(g.ObjectType)
	m.ObjectID = types.StringValue(g.ObjectID)
	m.GranteeType = types.StringValue(g.GranteeType)
	m.GranteeID = types.StringValue(g.GranteeID)
	m.Privilege = types.StringValue(g.Privilege)
	m.Tag = types.StringValue(g.Tag)
}
func (r *grantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	g, err := dapi.ParseGrantID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid grant import ID", err.Error())
		return
	}
	var m grantModel
	m.read(g)
	m.Tag = types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *grantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.client.BeginRBACMutation()()
	var plan grantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g := plan.grant()
	if _, err := g.Key(); err != nil {
		resp.Diagnostics.AddError("Invalid grant", err.Error())
		return
	}
	_, err := r.client.GetRBACGrant(g)
	if err == nil {
		resp.Diagnostics.AddError("Grant already exists", "Import the canonical stable-ID key instead of adopting existing access.")
		return
	}
	if !isRBACNotFoundError(err) {
		resp.Diagnostics.AddError("Unable to read grant", err.Error())
		return
	}
	tag, err := r.client.GetRBACGrantAuthority(g)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read policy version", err.Error())
		return
	}
	result, err := r.client.PutRBACGrant(g, tag)
	if err != nil {
		resp.Diagnostics.AddError("Unable to grant privilege", err.Error())
		return
	}
	plan.read(result)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *grantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state grantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetRBACGrant(state.grant())
	if isRBACNotFoundError(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read grant", err.Error())
		return
	}
	state.read(g)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *grantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state grantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.grant() != state.grant() {
		resp.Diagnostics.AddError("Grant identity requires replacement", "Changing target, principal or privilege requires replacement.")
		return
	}
	g, err := r.client.GetRBACGrant(state.grant())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read grant", err.Error())
		return
	}
	plan.read(g)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *grantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	defer r.client.BeginRBACMutation()()
	var state grantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetRBACGrant(state.grant())
	if isRBACNotFoundError(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read grant", err.Error())
		return
	}
	if err := r.client.DeleteRBACGrant(*g, g.Tag); err != nil {
		resp.Diagnostics.AddError("Unable to revoke privilege", err.Error())
	}
}
