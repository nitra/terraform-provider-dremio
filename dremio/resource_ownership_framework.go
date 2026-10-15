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

type ownershipModel struct {
	ID         types.String `tfsdk:"id"`
	ObjectType types.String `tfsdk:"object_type"`
	ObjectID   types.String `tfsdk:"object_id"`
	OwnerType  types.String `tfsdk:"owner_type"`
	OwnerID    types.String `tfsdk:"owner_id"`
	Tag        types.String `tfsdk:"tag"`
}
type ownershipResource struct{ roleResource }

var (
	_ resource.Resource                = &ownershipResource{}
	_ resource.ResourceWithConfigure   = &ownershipResource{}
	_ resource.ResourceWithImportState = &ownershipResource{}
)

func NewOwnershipResource() resource.Resource { return &ownershipResource{} }
func (r *ownershipResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ownership"
}
func (r *ownershipResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{Description: "Durable ownership in the experimental OSS RBAC fork. Requires active ADMIN and policy OCC. Existing ownership must be imported (including newly created roles and VDS). Owner changes transfer in place. Destroy is intentionally refused for live targets: transfer or explicitly forget state to stop managing; cleanup only succeeds after the namespace ID is proven deleted and never removes grants. Use lifecycle prevent_destroy for live ownership. SYSTEM and bootstrap are unsupported.", Attributes: map[string]schema.Attribute{
		"id":          schema.StringAttribute{Computed: true, Description: "Canonical base64url without padding of compact JSON [object_type, object_id].", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"object_type": schema.StringAttribute{Required: true, Description: "ROLE, SOURCE, SPACE, FOLDER, PDS, VDS, FUNCTION, VERSIONED_PDS or VERSIONED_VDS.", PlanModifiers: replace, Validators: []validator.String{stringvalidator.OneOf("ROLE", "SOURCE", "SPACE", "FOLDER", "PDS", "VDS", "FUNCTION", "VERSIONED_PDS", "VERSIONED_VDS")}},
		"object_id":   schema.StringAttribute{Required: true, Description: "Stable target ID; never a name.", PlanModifiers: replace},
		"owner_type":  schema.StringAttribute{Required: true, Description: "USER or ROLE; changing it transfers ownership in place.", Validators: []validator.String{stringvalidator.OneOf("USER", "ROLE")}},
		"owner_id":    schema.StringAttribute{Required: true, Description: "Active user UID or existing role ID; changing it transfers ownership in place."},
		"tag":         schema.StringAttribute{Computed: true, Description: "Observed global policy OCC version."},
	}}
}
func (m ownershipModel) ownership() dapi.RBACOwnership {
	return dapi.RBACOwnership{ObjectType: m.ObjectType.ValueString(), ObjectID: m.ObjectID.ValueString(), OwnerType: m.OwnerType.ValueString(), OwnerID: m.OwnerID.ValueString()}
}
func (m *ownershipModel) read(o *dapi.RBACOwnership) {
	id, _ := o.Key()
	m.ID = types.StringValue(id)
	m.ObjectType = types.StringValue(o.ObjectType)
	m.ObjectID = types.StringValue(o.ObjectID)
	m.OwnerType = types.StringValue(o.OwnerType)
	m.OwnerID = types.StringValue(o.OwnerID)
	m.Tag = types.StringValue(o.Tag)
}
func (r *ownershipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	o, err := dapi.ParseOwnershipID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ownership import ID", err.Error())
		return
	}
	var m ownershipModel
	m.read(o)
	m.OwnerType = types.StringNull()
	m.OwnerID = types.StringNull()
	m.Tag = types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *ownershipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.client.BeginRBACMutation()()
	var plan ownershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	o := plan.ownership()
	_, err := r.client.GetRBACOwnership(o)
	if err == nil {
		resp.Diagnostics.AddError("Ownership already exists", "Import the stable target key before transferring existing ownership.")
		return
	}
	if !isRBACNotFoundError(err) {
		resp.Diagnostics.AddError("Unable to read ownership", err.Error())
		return
	}
	roles, err := r.client.GetRBACRoles()
	if err != nil {
		resp.Diagnostics.AddError("Unable to read policy version", err.Error())
		return
	}
	result, err := r.client.PutRBACOwnership(o, roles.Tag)
	if err != nil {
		resp.Diagnostics.AddError("Unable to assign ownership", err.Error())
		return
	}
	plan.read(result)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *ownershipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ownershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	o, err := r.client.GetRBACOwnership(state.ownership())
	if isRBACNotFoundError(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read ownership", err.Error())
		return
	}
	state.read(o)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *ownershipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.client.BeginRBACMutation()()
	var plan, state ownershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desired := plan.ownership()
	previous := state.ownership()
	if desired.ObjectType != previous.ObjectType || desired.ObjectID != previous.ObjectID {
		resp.Diagnostics.AddError("Ownership target requires replacement", "A target change cannot transfer ownership between objects.")
		return
	}
	current, err := r.client.GetRBACOwnership(previous)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read ownership", err.Error())
		return
	}
	if current.OwnerType != desired.OwnerType || current.OwnerID != desired.OwnerID {
		current, err = r.client.PutRBACOwnership(desired, current.Tag)
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to transfer ownership", err.Error())
		return
	}
	plan.read(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *ownershipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	defer r.client.BeginRBACMutation()()
	var state ownershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	o, err := r.client.GetRBACOwnership(state.ownership())
	if isRBACNotFoundError(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read ownership", err.Error())
		return
	}
	// Never delete a concurrently transferred owner's record, even if the target is gone.
	if o.OwnerType != state.OwnerType.ValueString() || o.OwnerID != state.OwnerID.ValueString() {
		resp.Diagnostics.AddError("Ownership changed", "Refresh before cleanup; the observed owner differs from state.")
		return
	}
	if err := r.client.DeleteRBACOwnership(*o, o.Tag); err != nil {
		resp.Diagnostics.AddError("Unable to clean ownership", "Live targets retain their owner. Transfer or explicitly forget state; deleted namespace targets allow exact cleanup. "+err.Error())
	}
}
