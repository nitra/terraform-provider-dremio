package dremio

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

type roleResourceModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
	Tag  types.String `tfsdk:"tag"`
}

type roleResource struct{ client *dapi.Client }

var (
	_ resource.Resource                = &roleResource{}
	_ resource.ResourceWithConfigure   = &roleResource{}
	_ resource.ResourceWithImportState = &roleResource{}
)

func NewRoleResource() resource.Resource { return &roleResource{} }

func (r *roleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *roleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Role in the experimental Dremio OSS RBAC fork. Requires initialized OSS management API and an active ADMIN caller. Not compatible with the Enterprise Roles API. System roles may be imported for observation but cannot be created or destroyed. Deletion refuses memberships, grants and owned objects; remove or transfer them explicitly first.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.StringAttribute{Computed: true, Description: "Stable server-generated role ID; import by this ID, not by name.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{Required: true, Description: "Role name. Rename is unsupported and requires replacement.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"tag":  schema.StringAttribute{Computed: true, Description: "Observed global policy OCC version; not an Enterprise role version."},
		},
	}
}

func (r *roleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*dapi.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected configure type", fmt.Sprintf("expected *dapi.Client, got %T", req.ProviderData))
		return
	}
	r.client = client
}

func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError("Role ID required", "Import by stable role ID, not by role name.")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.client.BeginRBACMutation()()
	var plan roleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	roles, err := r.client.GetRBACRoles()
	if err != nil {
		resp.Diagnostics.AddError("Unable to read OSS RBAC policy", err.Error())
		return
	}
	for _, name := range roles.Roles {
		if name == plan.Name.ValueString() {
			resp.Diagnostics.AddError("Role already exists", "Import its stable ID instead of adopting an existing role by name.")
			return
		}
	}
	role, err := r.client.CreateRBACRole(plan.Name.ValueString(), roles.Tag)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create OSS RBAC role", err.Error())
		return
	}
	plan.read(role)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	role, err := r.client.GetRBACRole(state.ID.ValueString())
	if isRBACNotFoundError(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read OSS RBAC role", err.Error())
		return
	}
	state.read(role)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state roleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Name.Equal(state.Name) {
		resp.Diagnostics.AddError("Role rename requires replacement", "No role rename endpoint is supported.")
		return
	}
	role, err := r.client.GetRBACRole(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read OSS RBAC role", err.Error())
		return
	}
	if role.Name != plan.Name.ValueString() {
		resp.Diagnostics.AddError("Role identity changed", "Refresh state before retrying.")
		return
	}
	plan.read(role)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	defer r.client.BeginRBACMutation()()
	var state roleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	role, err := r.client.GetRBACRole(state.ID.ValueString())
	if isRBACNotFoundError(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read OSS RBAC role", err.Error())
		return
	}
	if role.Name != state.Name.ValueString() {
		resp.Diagnostics.AddError("Role identity changed", "Refusing deletion; refresh state first.")
		return
	}
	if err := r.client.DeleteRBACRole(role.ID, role.Tag); err != nil {
		resp.Diagnostics.AddError("Unable to delete OSS RBAC role", err.Error())
	}
}

func (m *roleResourceModel) read(role *dapi.RBACRole) {
	m.ID = types.StringValue(role.ID)
	m.Name = types.StringValue(role.Name)
	m.Tag = types.StringValue(role.Tag)
}

// Do not mistake text inside a 403/409 response body for the response status.
func isRBACNotFoundError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "status: 404,")
}
