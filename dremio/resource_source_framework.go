package dremio

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

var gcsAuthModes = []string{"AUTO", "SERVICE_ACCOUNT_KEYS", "OAUTH2_TOKEN"}

// nessieAuthTypes are NessieAuthType values (com.dremio.exec.catalog.conf.NessieAuthType).
var nessieAuthTypes = []string{"NONE", "BEARER", "OAUTH2"}

// nessieCredentialTypes are AWSAuthenticationType values (com.dremio.exec.catalog.conf.AWSAuthenticationType).
// NESSIE sources always carry this field even when storage_provider is GOOGLE (it's
// inherited from AbstractDataplanePluginConfig, which is shared by AWS/Azure/Google
// dataplane sources) - modeled purely for lossless roundtrip, not used for GOOGLE.
var nessieCredentialTypes = []string{"ACCESS_KEY", "EC2_METADATA", "NONE", "AWS_PROFILE"}

// nessieStorageProviders is intentionally restricted to GOOGLE: NessiePluginConfig
// supports AWS/AZURE/GOOGLE, but every Nessie source in this Dremio instance uses
// GOOGLE, and the AWS/Azure field sets are unmodeled here. A source configured for
// AWS or Azure would fail this validator at plan time rather than silently losing
// its storage settings on the first apply.
var nessieStorageProviders = []string{"GOOGLE"}

// sourceTypes lists every value sourceConfigToAPIConfig/applyAPIConfigToModel
// actually implement. Keep in sync with those functions.
var sourceTypes = []string{"NAS", "MSSQL", "GCS", "NESSIE", "MYSQL", "POSTGRES"}

// sourceResourceModel is the dremio_source Go-side state model. Attribute
// names/types must stay exactly as they were under the old SDKv2 schema
// (see git history), since this resource already has real state and a
// mismatch would force a state upgrade instead of reading cleanly.
type sourceResourceModel struct {
	ID                    types.String        `tfsdk:"id"`
	Name                  types.String        `tfsdk:"name"`
	Type                  types.String        `tfsdk:"type"`
	Description           types.String        `tfsdk:"description"`
	Path                  types.List          `tfsdk:"path"`
	AuthTTLMs             types.Int64         `tfsdk:"auth_ttl_ms"`
	DatasetRefreshAfterMs types.Int64         `tfsdk:"dataset_refresh_after_ms"`
	DatasetExpireAfterMs  types.Int64         `tfsdk:"dataset_expire_after_ms"`
	NamesRefreshMs        types.Int64         `tfsdk:"names_refresh_ms"`
	UpdateMode            types.String        `tfsdk:"update_mode"`
	AccRefreshPeriodMs    types.Int64         `tfsdk:"acc_refresh_period_ms"`
	AccGracePeriodMs      types.Int64         `tfsdk:"acc_grace_period_ms"`
	AccNeverExpire        types.Bool          `tfsdk:"acc_never_expire"`
	AccNeverRefresh       types.Bool          `tfsdk:"acc_never_refresh"`
	Config                []sourceConfigModel `tfsdk:"config"`
	SecureConfig          []secureConfigModel `tfsdk:"secure_config"`
}

type sourceConfigModel struct {
	MountPath                  types.String `tfsdk:"mount_path"`
	Username                   types.String `tfsdk:"username"`
	Hostname                   types.String `tfsdk:"hostname"`
	Port                       types.String `tfsdk:"port"`
	AuthenticationType         types.String `tfsdk:"authentication_type"`
	FetchSize                  types.Int64  `tfsdk:"fetch_size"`
	Database                   types.String `tfsdk:"database"`
	ShowOnlyConnectionDatabase types.Bool   `tfsdk:"show_only_connection_database"`
	ProjectID                  types.String `tfsdk:"project_id"`
	AuthMode                   types.String `tfsdk:"auth_mode"`
	RootPath                   types.String `tfsdk:"root_path"`
	BucketWhitelist            types.List   `tfsdk:"bucket_whitelist"`
	AsyncEnabled               types.Bool   `tfsdk:"async_enabled"`
	CachingEnable              types.Bool   `tfsdk:"caching_enable"`
	CachePercent               types.Int64  `tfsdk:"cache_percent"`
	ClientEmail                types.String `tfsdk:"client_email"`
	ClientID                   types.String `tfsdk:"client_id"`
	PrivateKeyID               types.String `tfsdk:"private_key_id"`
	NessieEndpoint             types.String `tfsdk:"nessie_endpoint"`
	NessieAuthType             types.String `tfsdk:"nessie_auth_type"`
	Secure                     types.Bool   `tfsdk:"secure"`
	StorageProvider            types.String `tfsdk:"storage_provider"`
	CredentialType             types.String `tfsdk:"credential_type"`
	// JDBC-family fields (MYSQL/POSTGRES/MSSQL), shared to varying degrees -
	// see sourceConfigToAPIConfig/applyAPIConfigToModel for which type uses
	// which.
	MaxIdleConns             types.Int64  `tfsdk:"max_idle_conns"`
	IdleTimeSec              types.Int64  `tfsdk:"idle_time_sec"`
	QueryTimeoutSec          types.Int64  `tfsdk:"query_timeout_sec"`
	UseSsl                   types.Bool   `tfsdk:"use_ssl"`
	NetWriteTimeout          types.Int64  `tfsdk:"net_write_timeout"`
	EncryptionValidationMode types.String `tfsdk:"encryption_validation_mode"`
	EnableServerVerification types.Bool   `tfsdk:"enable_server_verification"`
	UserImpersonation        types.Bool   `tfsdk:"user_impersonation"`
}

type secureConfigModel struct {
	Password          types.String `tfsdk:"password"`
	PrivateKey        types.String `tfsdk:"private_key"`
	NessieAccessToken types.String `tfsdk:"nessie_access_token"`
}

type sourceResource struct {
	client *dapi.Client
}

var (
	_ resource.Resource                = &sourceResource{}
	_ resource.ResourceWithConfigure   = &sourceResource{}
	_ resource.ResourceWithImportState = &sourceResource{}
)

func NewSourceResource() resource.Resource {
	return &sourceResource{}
}

func (r *sourceResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_source"
}

func (r *sourceResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *sourceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *sourceResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"type": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.OneOf(sourceTypes...)},
			},
			"description": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(""),
			},
			"path": schema.ListAttribute{
				Computed:      true,
				ElementType:   types.StringType,
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
			"auth_ttl_ms": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(3600000),
			},
			"dataset_refresh_after_ms": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(3600000),
			},
			"dataset_expire_after_ms": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(3600000),
			},
			"names_refresh_ms": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(3600000),
			},
			"update_mode": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString("PREFETCH_QUERIED"),
			},
			"acc_refresh_period_ms": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(10800000),
			},
			"acc_grace_period_ms": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(32400000),
			},
			"acc_never_expire": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"acc_never_refresh": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
		},
		Blocks: map[string]schema.Block{
			"config": schema.ListNestedBlock{
				Validators: []validator.List{listvalidator.SizeAtLeast(1), listvalidator.SizeAtMost(1)},
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						// NAS/MSSQL-only fields are plain Optional (no Computed/Default)
						// for the same reason as the Nessie-only fields further down:
						// a Computed+Default field plans a value for every OTHER
						// type's resources too, purely because their state predates
						// the field. Neither NAS nor MSSQL has any real state today,
						// so there's no Default convenience worth keeping here.
						"mount_path":          schema.StringAttribute{Optional: true},
						"username":            schema.StringAttribute{Optional: true},
						"hostname":            schema.StringAttribute{Optional: true},
						"port":                schema.StringAttribute{Optional: true},
						"authentication_type": schema.StringAttribute{Optional: true},
						"fetch_size":          schema.Int64Attribute{Optional: true},
						"database":            schema.StringAttribute{Optional: true},
						"show_only_connection_database": schema.BoolAttribute{
							Optional: true,
						},
						"project_id": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("")},
						"auth_mode": schema.StringAttribute{
							Optional: true, Computed: true, Default: stringdefault.StaticString("AUTO"),
							Validators: []validator.String{stringvalidator.OneOf(gcsAuthModes...)},
						},
						"root_path": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("/")},
						"bucket_whitelist": schema.ListAttribute{
							Optional:    true,
							ElementType: types.StringType,
						},
						"async_enabled":  schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
						"caching_enable": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
						"cache_percent": schema.Int64Attribute{
							Optional: true, Computed: true, Default: int64default.StaticInt64(70),
							Validators: []validator.Int64{int64validator.Between(1, 100)},
						},
						"client_email":   schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("")},
						"client_id":      schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("")},
						"private_key_id": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("")},
						// Nessie-only fields are plain Optional (no Computed/Default):
						// config is one shared block across all source types, and a
						// Computed+Default field plans a value for every OTHER type's
						// resources too (e.g. the GCS "bucket" source) the first time
						// it's added, purely because their state predates the field.
						// Plain Optional stays null for types that never set it, so it
						// produces no diff; NESSIE resources set these explicitly.
						"nessie_endpoint": schema.StringAttribute{Optional: true},
						"nessie_auth_type": schema.StringAttribute{
							Optional:   true,
							Validators: []validator.String{stringvalidator.OneOf(nessieAuthTypes...)},
						},
						"secure": schema.BoolAttribute{Optional: true},
						"storage_provider": schema.StringAttribute{
							Optional:   true,
							Validators: []validator.String{stringvalidator.OneOf(nessieStorageProviders...)},
						},
						"credential_type": schema.StringAttribute{
							Optional:   true,
							Validators: []validator.String{stringvalidator.OneOf(nessieCredentialTypes...)},
						},
						// JDBC-family fields (MYSQL/POSTGRES/MSSQL) are plain Optional for
						// the same reason as the Nessie-only fields above: no real state
						// predates them for any type, and a shared Computed+Default would
						// plan a spurious value on every OTHER type's resources. There's no
						// enum validator on authentication_type or
						// encryption_validation_mode: unlike GCS/NESSIE, these are closed
						// Enterprise connectors with no public Java class to confirm the
						// full set of legal values against - only "MASTER" and
						// "CERTIFICATE_AND_HOSTNAME_VALIDATION" have actually been observed.
						"max_idle_conns":             schema.Int64Attribute{Optional: true},
						"idle_time_sec":              schema.Int64Attribute{Optional: true},
						"query_timeout_sec":          schema.Int64Attribute{Optional: true},
						"use_ssl":                    schema.BoolAttribute{Optional: true},
						"net_write_timeout":          schema.Int64Attribute{Optional: true},
						"encryption_validation_mode": schema.StringAttribute{Optional: true},
						"enable_server_verification": schema.BoolAttribute{Optional: true},
						"user_impersonation":         schema.BoolAttribute{Optional: true},
					},
				},
			},
			"secure_config": schema.ListNestedBlock{
				Validators: []validator.List{listvalidator.SizeAtMost(1)},
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"password":            schema.StringAttribute{Optional: true, Sensitive: true},
						"private_key":         schema.StringAttribute{Optional: true, Sensitive: true},
						"nessie_access_token": schema.StringAttribute{Optional: true, Sensitive: true},
					},
				},
			},
		},
	}
}

func (r *sourceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sourceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sType := plan.Type.ValueString()
	apiConfig, diags := sourceConfigToAPIConfig(ctx, sType, configOf(plan), secureConfigOf(plan))
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.NewSource(&dapi.NewSourceSpec{
		Name:                        plan.Name.ValueString(),
		Description:                 plan.Description.ValueString(),
		Type:                        sType,
		Config:                      apiConfig,
		MetadataPolicy:              metadataPolicyOf(plan),
		AccelerationRefreshPeriodMs: int(plan.AccRefreshPeriodMs.ValueInt64()),
		AccelerationGracePeriodMs:   int(plan.AccGracePeriodMs.ValueInt64()),
		AccelerationNeverExpire:     plan.AccNeverExpire.ValueBool(),
		AccelerationNeverRefresh:    plan.AccNeverRefresh.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Dremio source", err.Error())
		return
	}

	found, diags := r.readInto(ctx, created.Id, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Source disappeared right after creation", created.Id)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *sourceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sourceResourceModel
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

func (r *sourceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan sourceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state sourceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sType := plan.Type.ValueString()
	apiConfig, diags := sourceConfigToAPIConfig(ctx, sType, configOf(plan), secureConfigOf(plan))
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.UpdateSource(state.ID.ValueString(), &dapi.UpdateSourceSpec{
		Description:                 plan.Description.ValueString(),
		Config:                      apiConfig,
		MetadataPolicy:              metadataPolicyOf(plan),
		AccelerationRefreshPeriodMs: int(plan.AccRefreshPeriodMs.ValueInt64()),
		AccelerationGracePeriodMs:   int(plan.AccGracePeriodMs.ValueInt64()),
		AccelerationNeverExpire:     plan.AccNeverExpire.ValueBool(),
		AccelerationNeverRefresh:    plan.AccNeverRefresh.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to update Dremio source", err.Error())
		return
	}

	plan.ID = state.ID
	found, diags := r.readInto(ctx, state.ID.ValueString(), &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Source disappeared right after update", state.ID.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *sourceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sourceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteCatalogItem(state.ID.ValueString())
	if err != nil && !isNotFoundError(err) {
		resp.Diagnostics.AddError("Unable to delete Dremio source", err.Error())
	}
}

// readInto fetches the source by id and overlays the result onto model,
// which the caller must have already populated with the prior known
// config/secure_config (Create passes the plan, Read/Update pass the prior
// state) - config fields Dremio doesn't return for the type in question,
// and secure_config entirely (Dremio never echoes secrets back), are left
// exactly as they already were, matching the old SDKv2 provider's
// partial-Set behavior. Returns found=false on a 404.
func (r *sourceResource) readInto(ctx context.Context, id string, model *sourceResourceModel) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	source, err := r.client.GetSource(id)
	if isNotFoundError(err) {
		return false, diags
	}
	if err != nil {
		diags.AddError("Unable to read Dremio source", err.Error())
		return false, diags
	}

	model.ID = types.StringValue(id)
	model.Name = types.StringValue(source.Name)
	model.Description = types.StringValue(source.Description)
	model.Type = types.StringValue(source.Type)

	pathList, d := types.ListValueFrom(ctx, types.StringType, source.Path)
	diags.Append(d...)
	model.Path = pathList

	config := configOf(*model)
	if apiConfig, ok := source.Config.(map[string]interface{}); ok {
		diags.Append(applyAPIConfigToModel(source.Type, apiConfig, &config)...)
	}
	model.Config = []sourceConfigModel{config}

	if source.MetadataPolicy != nil {
		model.AuthTTLMs = types.Int64Value(int64(source.MetadataPolicy.AuthTTLMs))
		model.DatasetRefreshAfterMs = types.Int64Value(int64(source.MetadataPolicy.DatasetRefreshAfterMs))
		model.DatasetExpireAfterMs = types.Int64Value(int64(source.MetadataPolicy.DatasetExpireAfterMs))
		model.NamesRefreshMs = types.Int64Value(int64(source.MetadataPolicy.NamesRefreshMs))
		model.UpdateMode = types.StringValue(source.MetadataPolicy.DatasetUpdateMode)
	}
	model.AccRefreshPeriodMs = types.Int64Value(int64(source.AccelerationRefreshPeriodMs))
	model.AccGracePeriodMs = types.Int64Value(int64(source.AccelerationGracePeriodMs))
	model.AccNeverExpire = types.BoolValue(source.AccelerationNeverExpire)
	model.AccNeverRefresh = types.BoolValue(source.AccelerationNeverRefresh)

	return true, diags
}

func configOf(m sourceResourceModel) sourceConfigModel {
	if len(m.Config) > 0 {
		return m.Config[0]
	}
	return sourceConfigModel{}
}

func secureConfigOf(m sourceResourceModel) secureConfigModel {
	if len(m.SecureConfig) > 0 {
		return m.SecureConfig[0]
	}
	return secureConfigModel{}
}

func metadataPolicyOf(m sourceResourceModel) *dapi.SourceMetadataPolicy {
	return &dapi.SourceMetadataPolicy{
		AuthTTLMs:             int(m.AuthTTLMs.ValueInt64()),
		DatasetRefreshAfterMs: int(m.DatasetRefreshAfterMs.ValueInt64()),
		DatasetExpireAfterMs:  int(m.DatasetExpireAfterMs.ValueInt64()),
		NamesRefreshMs:        int(m.NamesRefreshMs.ValueInt64()),
		DatasetUpdateMode:     m.UpdateMode.ValueString(),
	}
}

// sourceConfigToAPIConfig is the Framework equivalent of the old SDKv2
// getSourceConfig: it still has to branch on the source type by hand
// (Framework has no typed-union attribute), just with typed fields instead
// of map[string]interface{}.
func sourceConfigToAPIConfig(ctx context.Context, sType string, config sourceConfigModel, secure secureConfigModel) (map[string]interface{}, diag.Diagnostics) {
	var diags diag.Diagnostics

	switch sType {
	case "NAS":
		return map[string]interface{}{
			"path": config.MountPath.ValueString(),
		}, diags
	case "MSSQL":
		return map[string]interface{}{
			"username":                   config.Username.ValueString(),
			"password":                   secure.Password.ValueString(),
			"hostname":                   config.Hostname.ValueString(),
			"port":                       config.Port.ValueString(),
			"authenticationType":         config.AuthenticationType.ValueString(),
			"fetchSize":                  config.FetchSize.ValueInt64(),
			"database":                   config.Database.ValueString(),
			"showOnlyConnectionDatabase": config.ShowOnlyConnectionDatabase.ValueBool(),
			"useSsl":                     config.UseSsl.ValueBool(),
			"enableServerVerification":   config.EnableServerVerification.ValueBool(),
			"maxIdleConns":               config.MaxIdleConns.ValueInt64(),
			"idleTimeSec":                config.IdleTimeSec.ValueInt64(),
			"queryTimeoutSec":            config.QueryTimeoutSec.ValueInt64(),
			"userImpersonation":          config.UserImpersonation.ValueBool(),
		}, diags
	case "MYSQL":
		return map[string]interface{}{
			"hostname":           config.Hostname.ValueString(),
			"port":               config.Port.ValueString(),
			"database":           config.Database.ValueString(),
			"username":           config.Username.ValueString(),
			"password":           secure.Password.ValueString(),
			"authenticationType": config.AuthenticationType.ValueString(),
			"fetchSize":          config.FetchSize.ValueInt64(),
			"netWriteTimeout":    config.NetWriteTimeout.ValueInt64(),
			"maxIdleConns":       config.MaxIdleConns.ValueInt64(),
			"idleTimeSec":        config.IdleTimeSec.ValueInt64(),
			"queryTimeoutSec":    config.QueryTimeoutSec.ValueInt64(),
		}, diags
	case "POSTGRES":
		return map[string]interface{}{
			"hostname":                 config.Hostname.ValueString(),
			"port":                     config.Port.ValueString(),
			"databaseName":             config.Database.ValueString(),
			"username":                 config.Username.ValueString(),
			"password":                 secure.Password.ValueString(),
			"authenticationType":       config.AuthenticationType.ValueString(),
			"fetchSize":                config.FetchSize.ValueInt64(),
			"useSsl":                   config.UseSsl.ValueBool(),
			"encryptionValidationMode": config.EncryptionValidationMode.ValueString(),
			"maxIdleConns":             config.MaxIdleConns.ValueInt64(),
			"idleTimeSec":              config.IdleTimeSec.ValueInt64(),
			"queryTimeoutSec":          config.QueryTimeoutSec.ValueInt64(),
		}, diags
	case "GCS":
		var bucketWhitelist []string
		diags.Append(config.BucketWhitelist.ElementsAs(ctx, &bucketWhitelist, false)...)
		return map[string]interface{}{
			"projectId":       config.ProjectID.ValueString(),
			"authMode":        config.AuthMode.ValueString(),
			"rootPath":        config.RootPath.ValueString(),
			"bucketWhitelist": bucketWhitelist,
			"asyncEnabled":    config.AsyncEnabled.ValueBool(),
			"cachingEnable":   config.CachingEnable.ValueBool(),
			"cachePercent":    config.CachePercent.ValueInt64(),
			"clientEmail":     config.ClientEmail.ValueString(),
			"clientId":        config.ClientID.ValueString(),
			"privateKeyId":    config.PrivateKeyID.ValueString(),
			"privateKey":      secure.PrivateKey.ValueString(),
		}, diags
	case "NESSIE":
		// Google-storage fields reuse the same JSON keys as GCS's Nessie
		// counterparts have a different name (see the mapping table in the
		// migration plan / commit message): googleProjectId, googleRootPath,
		// googleAuthenticationType (same GCSAuthType enum as GCS's authMode),
		// isCachingEnabled, maxCacheSpacePct, googlePrivateKeyId/Email/Id,
		// googlePrivateKey. asyncEnabled is the one field GCS and NESSIE
		// happen to share verbatim.
		return map[string]interface{}{
			"asyncEnabled":             config.AsyncEnabled.ValueBool(),
			"isCachingEnabled":         config.CachingEnable.ValueBool(),
			"maxCacheSpacePct":         config.CachePercent.ValueInt64(),
			"storageProvider":          config.StorageProvider.ValueString(),
			"googleProjectId":          config.ProjectID.ValueString(),
			"googleAuthenticationType": config.AuthMode.ValueString(),
			"googleRootPath":           config.RootPath.ValueString(),
			"googlePrivateKeyId":       config.PrivateKeyID.ValueString(),
			"googleClientEmail":        config.ClientEmail.ValueString(),
			"googleClientId":           config.ClientID.ValueString(),
			"googlePrivateKey":         secure.PrivateKey.ValueString(),
			"nessieEndpoint":           config.NessieEndpoint.ValueString(),
			"nessieAuthType":           config.NessieAuthType.ValueString(),
			"secure":                   config.Secure.ValueBool(),
			"credentialType":           config.CredentialType.ValueString(),
			"nessieAccessToken":        secure.NessieAccessToken.ValueString(),
		}, diags
	}

	diags.AddError("Unsupported source type", fmt.Sprintf("type %q must be one of: %s", sType, strings.Join(sourceTypes, ", ")))
	return nil, diags
}

// applyAPIConfigToModel is the reverse of sourceConfigToAPIConfig: the
// Framework equivalent of the old SDKv2 readSourceConfig. It only overwrites
// the fields relevant to sType, leaving the rest of model exactly as the
// caller set it (mirroring SDKv2's d.Set-only-what-changed semantics).
func applyAPIConfigToModel(sType string, apiConfig map[string]interface{}, model *sourceConfigModel) diag.Diagnostics {
	var diags diag.Diagnostics

	// bucket_whitelist is GCS-only. For every other type it must still start
	// as a validly-typed null list (not the Go zero value a freshly
	// imported/created model has), or Framework fails to serialize state
	// with a "MISSING TYPE" error. The GCS case below unconditionally
	// overwrites this with the real value, so resetting it first is safe.
	model.BucketWhitelist = types.ListNull(types.StringType)

	switch sType {
	case "NAS":
		model.MountPath = types.StringValue(getString(apiConfig, "path"))
	case "MSSQL":
		model.Username = types.StringValue(getString(apiConfig, "username"))
		model.Hostname = types.StringValue(getString(apiConfig, "hostname"))
		model.Port = types.StringValue(getString(apiConfig, "port"))
		model.AuthenticationType = types.StringValue(getString(apiConfig, "authenticationType"))
		model.FetchSize = types.Int64Value(int64(getFloat64(apiConfig, "fetchSize")))
		model.Database = types.StringValue(getString(apiConfig, "database"))
		model.ShowOnlyConnectionDatabase = types.BoolValue(getBool(apiConfig, "showOnlyConnectionDatabase"))
		model.UseSsl = types.BoolValue(getBool(apiConfig, "useSsl"))
		model.EnableServerVerification = types.BoolValue(getBool(apiConfig, "enableServerVerification"))
		model.MaxIdleConns = types.Int64Value(int64(getFloat64(apiConfig, "maxIdleConns")))
		model.IdleTimeSec = types.Int64Value(int64(getFloat64(apiConfig, "idleTimeSec")))
		model.QueryTimeoutSec = types.Int64Value(int64(getFloat64(apiConfig, "queryTimeoutSec")))
		model.UserImpersonation = types.BoolValue(getBool(apiConfig, "userImpersonation"))
	case "MYSQL":
		model.Hostname = types.StringValue(getString(apiConfig, "hostname"))
		model.Port = types.StringValue(getString(apiConfig, "port"))
		model.Database = types.StringValue(getString(apiConfig, "database"))
		model.Username = types.StringValue(getString(apiConfig, "username"))
		model.AuthenticationType = types.StringValue(getString(apiConfig, "authenticationType"))
		model.FetchSize = types.Int64Value(int64(getFloat64(apiConfig, "fetchSize")))
		model.NetWriteTimeout = types.Int64Value(int64(getFloat64(apiConfig, "netWriteTimeout")))
		model.MaxIdleConns = types.Int64Value(int64(getFloat64(apiConfig, "maxIdleConns")))
		model.IdleTimeSec = types.Int64Value(int64(getFloat64(apiConfig, "idleTimeSec")))
		model.QueryTimeoutSec = types.Int64Value(int64(getFloat64(apiConfig, "queryTimeoutSec")))
	case "POSTGRES":
		model.Hostname = types.StringValue(getString(apiConfig, "hostname"))
		model.Port = types.StringValue(getString(apiConfig, "port"))
		model.Database = types.StringValue(getString(apiConfig, "databaseName"))
		model.Username = types.StringValue(getString(apiConfig, "username"))
		model.AuthenticationType = types.StringValue(getString(apiConfig, "authenticationType"))
		model.FetchSize = types.Int64Value(int64(getFloat64(apiConfig, "fetchSize")))
		model.UseSsl = types.BoolValue(getBool(apiConfig, "useSsl"))
		model.EncryptionValidationMode = types.StringValue(getString(apiConfig, "encryptionValidationMode"))
		model.MaxIdleConns = types.Int64Value(int64(getFloat64(apiConfig, "maxIdleConns")))
		model.IdleTimeSec = types.Int64Value(int64(getFloat64(apiConfig, "idleTimeSec")))
		model.QueryTimeoutSec = types.Int64Value(int64(getFloat64(apiConfig, "queryTimeoutSec")))
	case "GCS":
		var whitelist []string
		if raw, ok := apiConfig["bucketWhitelist"].([]interface{}); ok {
			for _, v := range raw {
				if s, ok := v.(string); ok {
					whitelist = append(whitelist, s)
				}
			}
		}
		listVal, d := types.ListValueFrom(context.Background(), types.StringType, whitelist)
		diags.Append(d...)

		model.ProjectID = types.StringValue(getString(apiConfig, "projectId"))
		model.AuthMode = types.StringValue(getString(apiConfig, "authMode"))
		model.RootPath = types.StringValue(getString(apiConfig, "rootPath"))
		model.BucketWhitelist = listVal
		model.AsyncEnabled = types.BoolValue(getBool(apiConfig, "asyncEnabled"))
		model.CachingEnable = types.BoolValue(getBool(apiConfig, "cachingEnable"))
		model.CachePercent = types.Int64Value(int64(getFloat64(apiConfig, "cachePercent")))
		model.ClientEmail = types.StringValue(getString(apiConfig, "clientEmail"))
		model.ClientID = types.StringValue(getString(apiConfig, "clientId"))
		model.PrivateKeyID = types.StringValue(getString(apiConfig, "privateKeyId"))
		// privateKey is a secret field; Dremio never returns it on read, so
		// it is intentionally left untouched here, same as password for
		// MSSQL above.
	case "NESSIE":
		model.AsyncEnabled = types.BoolValue(getBool(apiConfig, "asyncEnabled"))
		model.CachingEnable = types.BoolValue(getBool(apiConfig, "isCachingEnabled"))
		model.CachePercent = types.Int64Value(int64(getFloat64(apiConfig, "maxCacheSpacePct")))
		model.StorageProvider = types.StringValue(getString(apiConfig, "storageProvider"))
		model.ProjectID = types.StringValue(getString(apiConfig, "googleProjectId"))
		model.AuthMode = types.StringValue(getString(apiConfig, "googleAuthenticationType"))
		model.RootPath = types.StringValue(getString(apiConfig, "googleRootPath"))
		// googlePrivateKeyId/googleClientEmail/googleClientId are null (and
		// thus absent from the JSON entirely) when auth_mode is AUTO - only
		// SERVICE_ACCOUNT_KEYS populates them - hence the safe getters here.
		model.PrivateKeyID = types.StringValue(getString(apiConfig, "googlePrivateKeyId"))
		model.ClientEmail = types.StringValue(getString(apiConfig, "googleClientEmail"))
		model.ClientID = types.StringValue(getString(apiConfig, "googleClientId"))
		model.NessieEndpoint = types.StringValue(getString(apiConfig, "nessieEndpoint"))
		model.NessieAuthType = types.StringValue(getString(apiConfig, "nessieAuthType"))
		model.Secure = types.BoolValue(getBool(apiConfig, "secure"))
		model.CredentialType = types.StringValue(getString(apiConfig, "credentialType"))
		// googlePrivateKey/nessieAccessToken are secret fields; Dremio never
		// returns them on read, so left untouched here, same as elsewhere.
	}

	return diags
}

// getString/getBool/getFloat64 safely read a Dremio source-config field that
// may be entirely absent from the JSON (Dremio omits fields whose Java value
// is null, e.g. GCS service-account fields when auth_mode is AUTO) rather
// than present with a zero value - a plain type assertion would panic.
func getString(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func getBool(m map[string]interface{}, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func getFloat64(m map[string]interface{}, key string) float64 {
	v, _ := m[key].(float64)
	return v
}
