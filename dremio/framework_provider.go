package dremio

import (
	"context"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

// frameworkProvider is muxed alongside the legacy SDKv2 provider (see
// main.go) so resources can move to terraform-plugin-framework one at a
// time. Its provider-level schema below must stay identical to the SDKv2
// one in dremio/provider.go (same attributes, types, required/sensitive
// flags) - the mux server requires both to match.
type frameworkProvider struct{}

// NewFrameworkProvider constructs the Framework half of the muxed provider.
func NewFrameworkProvider() provider.Provider {
	return &frameworkProvider{}
}

func (p *frameworkProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "dremio"
}

func (p *frameworkProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"dremio_url": schema.StringAttribute{
				Required: true,
			},
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
			},
			"username": schema.StringAttribute{
				Optional: true,
			},
			"password": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
			},
		},
	}
}

type frameworkProviderModel struct {
	DremioURL types.String `tfsdk:"dremio_url"`
	APIKey    types.String `tfsdk:"api_key"`
	Username  types.String `tfsdk:"username"`
	Password  types.String `tfsdk:"password"`
}

func (p *frameworkProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data frameworkProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config := dapi.Config{
		ApiKey:   data.APIKey.ValueString(),
		Username: data.Username.ValueString(),
		Password: data.Password.ValueString(),
		Client: &http.Client{
			Transport: &retryTransport{
				base:       http.DefaultTransport,
				maxRetries: 3,
				baseDelay:  500 * time.Millisecond,
			},
		},
	}
	client, err := dapi.NewClient(data.DremioURL.ValueString(), config)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Dremio client", err.Error())
		return
	}

	resp.ResourceData = client
	resp.DataSourceData = client
}

// Resources: dremio_source is the only resource migrated off SDKv2 (see the
// approved migration plan) - it's the only one that shows up in any real
// state we found (`tofu state list` against tofu/dremio-dev has nothing
// else), so the other 9 SDKv2 resources stay on SDKv2 under the mux
// indefinitely rather than as a "temporary" bridge.
func (p *frameworkProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewSourceResource,
	}
}

func (p *frameworkProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}
