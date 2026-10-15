package dremio

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

type versionedObjectDataSourceModel struct {
	ObjectType      types.String `tfsdk:"object_type"`
	Path            types.List   `tfsdk:"path"`
	ReferenceType   types.String `tfsdk:"reference_type"`
	ReferenceValue  types.String `tfsdk:"reference_value"`
	GrantObjectType types.String `tfsdk:"grant_object_type"`
	ID              types.String `tfsdk:"id"`
}

type versionedObjectDataSource struct{ client *dapi.Client }

var (
	_ datasource.DataSource              = &versionedObjectDataSource{}
	_ datasource.DataSourceWithConfigure = &versionedObjectDataSource{}
)

func NewVersionedObjectDataSource() datasource.DataSource { return &versionedObjectDataSource{} }

func (d *versionedObjectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_versioned_object"
}

func (d *versionedObjectDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*dapi.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected data source configure type", fmt.Sprintf("expected *dapi.Client, got %T", req.ProviderData))
		return
	}
	d.client = client
}

func (d *versionedObjectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Resolves a live Nessie table or view to the stable, reference-scoped RBAC object ID. Re-read on each plan; source replacement or content recreation changes the ID.",
		Attributes: map[string]schema.Attribute{
			"object_type":       schema.StringAttribute{Required: true, Description: "PDS for an Iceberg table or VDS for an Iceberg view.", Validators: []validator.String{stringvalidator.OneOf("PDS", "VDS")}},
			"path":              schema.ListAttribute{Required: true, ElementType: types.StringType, Description: "Catalog path components including the source name."},
			"reference_type":    schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("BRANCH", "TAG", "COMMIT", "REFERENCE")}},
			"reference_value":   schema.StringAttribute{Required: true},
			"grant_object_type": schema.StringAttribute{Computed: true, Description: "VERSIONED_PDS or VERSIONED_VDS for dremio_grant/dremio_ownership."},
			"id":                schema.StringAttribute{Computed: true, Description: "Live stable RBAC object ID. Changes when this content is recreated."},
		},
	}
}

func (d *versionedObjectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model versionedObjectDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var path []string
	resp.Diagnostics.Append(model.Path.ElementsAs(ctx, &path, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	object, err := d.client.ResolveRBACVersionedObject(
		model.ObjectType.ValueString(), path, model.ReferenceType.ValueString(), model.ReferenceValue.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to resolve versioned RBAC object", err.Error())
		return
	}
	model.GrantObjectType = types.StringValue(object.ObjectType)
	model.ID = types.StringValue(object.ObjectID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
