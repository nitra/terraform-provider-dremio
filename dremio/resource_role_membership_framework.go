package dremio

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

type membershipModel struct {
	ID         types.String `tfsdk:"id"`
	RoleID     types.String `tfsdk:"role_id"`
	MemberType types.String `tfsdk:"member_type"`
	MemberID   types.String `tfsdk:"member_id"`
	Tag        types.String `tfsdk:"tag"`
}

// Reuse the existing client configuration; no second authentication/session store.
type membershipResource struct{ roleResource }

var (
	_ resource.Resource                = &membershipResource{}
	_ resource.ResourceWithConfigure   = &membershipResource{}
	_ resource.ResourceWithImportState = &membershipResource{}
)

func NewRoleMembershipResource() resource.Resource { return &membershipResource{} }

func (r *membershipResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role_membership"
}

func (r *membershipResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{Description: "One explicit role assignment in the experimental OSS RBAC fork. Uses stable IDs, not names. An active ADMIN is required. PUBLIC is implicit and cannot be managed. Server rejects role cycles and removal of the last active ADMIN. Existing assignments must be imported; grants and other memberships are never replaced.", Attributes: map[string]schema.Attribute{
		"id":          schema.StringAttribute{Computed: true, Description: "Canonical base64url (without padding) of the compact JSON array [role_id, member_type, member_id]. Import by this key.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"role_id":     schema.StringAttribute{Required: true, Description: "Stable ID of the role being granted.", PlanModifiers: replace},
		"member_type": schema.StringAttribute{Required: true, Description: "USER or ROLE. For ROLE, the member role receives privileges of role_id.", PlanModifiers: replace, Validators: []validator.String{stringvalidator.OneOf("USER", "ROLE")}},
		"member_id":   schema.StringAttribute{Required: true, Description: "Stable user UID or member role ID; never a display name.", PlanModifiers: replace},
		"tag":         schema.StringAttribute{Computed: true, Description: "Observed global policy OCC version."},
	}}
}

func (r *membershipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	m, err := dapi.ParseMembershipID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid membership import ID", err.Error())
		return
	}
	model := membershipModel{ID: types.StringValue(req.ID), RoleID: types.StringValue(m.RoleID), MemberType: types.StringValue(m.MemberType), MemberID: types.StringValue(m.MemberID), Tag: types.StringNull()}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *membershipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.client.BeginRBACMutation()()
	var plan membershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	roleID, memberType, memberID := plan.RoleID.ValueString(), plan.MemberType.ValueString(), plan.MemberID.ValueString()
	if _, err := dapi.MembershipID(roleID, memberType, memberID); err != nil {
		resp.Diagnostics.AddError("Invalid membership", err.Error())
		return
	}
	_, err := r.client.GetRBACMembership(roleID, memberType, memberID)
	if err == nil {
		resp.Diagnostics.AddError("Membership already exists", "Import the canonical stable-ID key instead of adopting an existing assignment.")
		return
	}
	if !isRBACNotFoundError(err) {
		resp.Diagnostics.AddError("Unable to read membership", err.Error())
		return
	}
	roles, err := r.client.GetRBACRoles()
	if err != nil {
		resp.Diagnostics.AddError("Unable to read policy version", err.Error())
		return
	}
	m, err := r.client.PutRBACMembership(roleID, memberType, memberID, roles.Tag)
	if err != nil {
		resp.Diagnostics.AddError("Unable to assign role", err.Error())
		return
	}
	plan.read(m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *membershipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state membershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.GetRBACMembership(state.RoleID.ValueString(), state.MemberType.ValueString(), state.MemberID.ValueString())
	if isRBACNotFoundError(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read membership", err.Error())
		return
	}
	state.read(m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *membershipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state membershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.RoleID.Equal(state.RoleID) || !plan.MemberType.Equal(state.MemberType) || !plan.MemberID.Equal(state.MemberID) {
		resp.Diagnostics.AddError("Membership identity requires replacement", "Refresh state before changing a stable-ID assignment.")
		return
	}
	m, err := r.client.GetRBACMembership(state.RoleID.ValueString(), state.MemberType.ValueString(), state.MemberID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read membership", err.Error())
		return
	}
	plan.read(m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *membershipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	defer r.client.BeginRBACMutation()()
	var state membershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.GetRBACMembership(state.RoleID.ValueString(), state.MemberType.ValueString(), state.MemberID.ValueString())
	if isRBACNotFoundError(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read membership", err.Error())
		return
	}
	if err := r.client.DeleteRBACMembership(m.RoleID, m.MemberType, m.MemberID, m.Tag); err != nil {
		resp.Diagnostics.AddError("Unable to revoke role assignment", err.Error())
	}
}

func (m *membershipModel) read(value *dapi.RBACMembership) {
	id, _ := dapi.MembershipID(value.RoleID, value.MemberType, value.MemberID)
	m.ID = types.StringValue(id)
	m.RoleID = types.StringValue(value.RoleID)
	m.MemberType = types.StringValue(value.MemberType)
	m.MemberID = types.StringValue(value.MemberID)
	m.Tag = types.StringValue(value.Tag)
}
