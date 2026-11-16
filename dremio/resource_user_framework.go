package dremio

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

type userResourceModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	FirstName         types.String `tfsdk:"first_name"`
	LastName          types.String `tfsdk:"last_name"`
	Email             types.String `tfsdk:"email"`
	InitialPasswordWO types.String `tfsdk:"initial_password_wo"`
	Tag               types.String `tfsdk:"tag"`
	Active            types.Bool   `tfsdk:"active"`
}

type userResource struct{ client *dapi.Client }

var (
	_ resource.Resource                = &userResource{}
	_ resource.ResourceWithConfigure   = &userResource{}
	_ resource.ResourceWithImportState = &userResource{}
)

// NewUserResource manages local Dremio user profiles and can provision a new
// account with a write-only initial password.
func NewUserResource() resource.Resource { return &userResource{} }

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Profile of a local Dremio Community Edition user. New users require a write-only initial password; imported users never require or expose a password. Account deletion and permissions are deliberately outside this resource's scope. Enterprise and externally provisioned users are rejected.",
		Attributes: map[string]schema.Attribute{
			"id":                  schema.StringAttribute{Computed: true, Description: "Stable user UUID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":                schema.StringAttribute{Required: true, Description: "Existing username. Renaming users is not supported.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"first_name":          schema.StringAttribute{Optional: true, Computed: true, Description: "First name; omitted values retain the existing profile.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"last_name":           schema.StringAttribute{Optional: true, Computed: true, Description: "Last name; omitted values retain the existing profile.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"email":               schema.StringAttribute{Optional: true, Computed: true, Description: "Email; omitted values retain the existing profile.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"initial_password_wo": schema.StringAttribute{Optional: true, Sensitive: true, WriteOnly: true, Description: "Initial local-user password. Required only when creating a user; write-only values never enter the plan or state and cannot rotate an existing password."},
			"tag":                 schema.StringAttribute{Computed: true, Description: "Version used for optimistic concurrency."},
			"active":              schema.BoolAttribute{Computed: true, Description: "Current account status; never changed by this resource."},
		},
	}
}

func (r *userResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config userResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	// OpenTofu intentionally removes write-only values from the plan. Read the
	// password from configuration so it is available to the create request but
	// is still absent from both the saved plan and state.
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.InitialPasswordWO.IsNull() || config.InitialPasswordWO.IsUnknown() || config.InitialPasswordWO.ValueString() == "" {
		resp.Diagnostics.AddError("Initial password required", "Set initial_password_wo from an ephemeral secret value when creating a local Dremio user.")
		return
	}
	created, err := r.client.CreateLocalUser(dapi.User{Name: plan.Name.ValueString(), FirstName: plan.FirstName.ValueString(), LastName: plan.LastName.ValueString(), Email: plan.Email.ValueString()}, config.InitialPasswordWO.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Dremio user", err.Error())
		return
	}
	plan.InitialPasswordWO = types.StringNull()
	plan.read(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	user, err := r.client.GetUser(state.ID.ValueString())
	if isNotFoundError(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Dremio user", err.Error())
		return
	}
	state.read(user)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state userResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Name.Equal(state.Name) {
		resp.Diagnostics.AddError("User rename is not supported", "Create a distinct account or import the existing account under its current name.")
		return
	}
	user := dapi.User{ID: state.ID.ValueString(), Name: plan.Name.ValueString(), FirstName: plan.FirstName.ValueString(), LastName: plan.LastName.ValueString(), Email: plan.Email.ValueString(), Tag: state.Tag.ValueString()}
	if err := r.client.UpdateUserProfile(user); err != nil {
		resp.Diagnostics.AddError("Unable to update Dremio user", err.Error())
		return
	}
	updated, err := r.client.GetUser(user.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read updated Dremio user", err.Error())
		return
	}
	plan.InitialPasswordWO = types.StringNull()
	plan.read(updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *userResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddError("Dremio user deletion is not supported", "This resource never deletes an account. Disable it through the identity-management workflow, then remove it from OpenTofu state without destroying the user.")
}

// read copies only public profile metadata into state.
func (m *userResourceModel) read(user *dapi.User) {
	m.ID = types.StringValue(user.ID)
	m.Name = types.StringValue(user.Name)
	m.FirstName = types.StringValue(user.FirstName)
	m.LastName = types.StringValue(user.LastName)
	m.Email = types.StringValue(user.Email)
	m.Tag = types.StringValue(user.Tag)
	m.Active = types.BoolValue(user.Active)
}
