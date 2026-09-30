package dremio

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

// summaryDataSourceModel mirrors the SDKv2 dremio_summary schema exactly -
// this data source has no state to preserve (data sources never persist
// anything, they're re-read on every plan), but keeping the contract
// identical means no config needs to change to adopt the new binary.
type summaryDataSourceModel struct {
	ID      types.String `tfsdk:"id"`
	Summary types.List   `tfsdk:"summary"`
}

type summaryEntryModel struct {
	ID   types.String `tfsdk:"id"`
	Type types.String `tfsdk:"type"`
}

var summaryEntryAttrTypes = map[string]attr.Type{
	"id":   types.StringType,
	"type": types.StringType,
}

type summaryDataSource struct {
	client *dapi.Client
}

var (
	_ datasource.DataSource              = &summaryDataSource{}
	_ datasource.DataSourceWithConfigure = &summaryDataSource{}
)

func NewSummaryDataSource() datasource.DataSource {
	return &summaryDataSource{}
}

func (d *summaryDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_summary"
}

func (d *summaryDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *summaryDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"summary": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.StringAttribute{Computed: true},
						"type": schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

// Read fetches the root catalog summary. Like the SDKv2 resource, id is
// always the current unix timestamp: this data source has no natural
// identity, so this keeps it "always changed" and re-read on every plan
// rather than looking stale/cached.
func (d *summaryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	summary, err := d.client.GetRootCatalogSummary()
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Dremio root catalog summary", err.Error())
		return
	}

	entries := make([]summaryEntryModel, len(summary))
	for i, x := range summary {
		entries[i] = summaryEntryModel{
			ID:   types.StringValue(x.Id),
			Type: types.StringValue(x.Type),
		}
	}
	summaryList, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: summaryEntryAttrTypes}, entries)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	model := summaryDataSourceModel{
		ID:      types.StringValue(strconv.FormatInt(time.Now().Unix(), 10)),
		Summary: summaryList,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
