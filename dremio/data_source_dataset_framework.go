package dremio

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

// datasetLookupRetries/datasetLookupRetryDelay bound how long Read() will
// retry a 404 before giving up. Needed because a metadata-impacting
// dremio_source update triggers Dremio's dataset rediscovery
// (SourceRefreshOption.BACKGROUND_DATASETS_CREATION) asynchronously - right
// after that update commits, a dataset looked up by path can still 404 for a
// few seconds while it's being rediscovered under a new id. This is exactly
// the data source a dremio_raw_reflection/dremio_aggr_reflection's dataset_id
// should come from (instead of a hardcoded id) so that re-running
// `tofu apply` after an acknowledged metadata-impacting change recreates the
// reflection against the dataset's new id automatically.
const (
	datasetLookupRetries    = 10
	datasetLookupRetryDelay = time.Second
)

type datasetDataSourceModel struct {
	Path types.List   `tfsdk:"path"`
	ID   types.String `tfsdk:"id"`
}

type datasetDataSource struct {
	client *dapi.Client
}

var (
	_ datasource.DataSource              = &datasetDataSource{}
	_ datasource.DataSourceWithConfigure = &datasetDataSource{}
)

func NewDatasetDataSource() datasource.DataSource {
	return &datasetDataSource{}
}

func (d *datasetDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dataset"
}

func (d *datasetDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *datasetDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a dataset's current catalog id by its path. Reference " +
			"dremio_raw_reflection/dremio_aggr_reflection's dataset_id through this data " +
			"source rather than a hardcoded id: a metadata-impacting dremio_source change " +
			"deletes and rediscovers every dataset under that source with a brand new id, " +
			"and a hardcoded dataset_id has no way to follow that - this data source re-reads " +
			"the path on every apply, so a reflection that references it self-heals on the " +
			"next `tofu apply` instead of being permanently orphaned. IMPORTANT: the " +
			"reflection resource also needs `lifecycle { create_before_destroy = true }`. " +
			"Without it, Terraform's default destroy-then-create order destroys the old " +
			"reflection as soon as dataset_id's value becomes unknown (any time the upstream " +
			"dremio_source is changing) - even if that source update then fails or is blocked " +
			"(e.g. by dremio_source's own metadata-impact guard), permanently losing a " +
			"reflection that never actually needed to move. create_before_destroy makes " +
			"Terraform create the new reflection first and only destroy the old one once that " +
			"succeeds, confirmed live: without it, a blocked source update still destroyed " +
			"the reflection; with it, the reflection survived the same blocked update intact.",
		Attributes: map[string]schema.Attribute{
			"path": schema.ListAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "Catalog path, e.g. [\"mysource\", \"myschema\", \"mytable\"].",
			},
			"id": schema.StringAttribute{
				Computed: true,
			},
		},
	}
}

func (d *datasetDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model datasetDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var path []string
	resp.Diagnostics.Append(model.Path.ElementsAs(ctx, &path, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var entity *dapi.CatalogEntity
	var err error
	for attempt := 0; ; attempt++ {
		entity, err = d.client.GetCatalogEntityByPath(path)
		if err == nil || !isNotFoundError(err) || attempt >= datasetLookupRetries {
			break
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Unable to read Dremio dataset", ctx.Err().Error())
			return
		case <-time.After(datasetLookupRetryDelay):
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Dremio dataset", fmt.Sprintf(
			"path %v: %s (retried %d times - if a dremio_source on this path was just "+
				"updated, rediscovery may still be in progress server-side)", path, err.Error(), datasetLookupRetries))
		return
	}

	model.ID = types.StringValue(entity.Id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
