package dremio

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
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

// secureConfigPasswordPattern restricts secure_config.password to Dremio's
// CredentialsProvider URI schemes (env:VARNAME, file:///path/to/secret) instead of a
// literal value, so a plaintext password can never be accidentally committed to
// Terraform config/state. Both schemes are confirmed to work against this Dremio
// deployment's JDBC sources (com.dremio.services.credentials.EnvCredentialsProvider /
// FileCredentialsProvider).
var secureConfigPasswordPattern = regexp.MustCompile(`^(env|file):`)

// metadataImpactGuardEnvVar is the escape hatch for guardAgainstMetadataImpact:
// set it (to any non-empty value) in the environment running `tofu apply` to
// proceed with a metadata-impacting source change anyway. Deliberately not a
// schema attribute/lifecycle flag - anything written into .tf/state is a
// standing switch that's easy to forget enabled, silently waving through a
// later, unintended impacting change. An env var only affects the one
// `tofu apply` process it's set for and leaves no trace in config or state.
const metadataImpactGuardEnvVar = "DREMIO_ALLOW_METADATA_IMPACTING_CHANGE"

// decideMetadataImpactGuard is the pure decision inside guardAgainstMetadataImpact,
// factored out so it's unit-testable without HTTP. changedFields names which
// config/secure_config attributes differ between the prior and new config, for
// the error message only - the block/proceed decision itself always comes from
// impacting (Dremio's own answer, or a forced true when that couldn't be
// determined - see guardAgainstMetadataImpact).
func decideMetadataImpactGuard(impacting bool, allowed bool, sourceName string, changedFields []string) (block bool, message string) {
	if !impacting || allowed {
		return false, ""
	}
	sorted := append([]string(nil), changedFields...)
	sort.Strings(sorted)
	fields := "its configuration"
	if len(sorted) > 0 {
		fields = strings.Join(sorted, ", ")
	}
	return true, fmt.Sprintf(
		"Source %q: this change touches %s, which Dremio treats as metadata-impacting "+
			"(confirmed live against POST /apiv2/sources/isMetadataImpacting - the same check "+
			"behind the UI's \"Warning\" dialog). Applying it deletes and rediscovers every "+
			"dataset under this source, silently dropping any reflections, formats and "+
			"permissions attached to them - with no warning at the API level. "+
			"If reflections for this source are declared as dremio_raw_reflection/"+
			"dremio_aggr_reflection resources that reference a dremio_dataset data source by "+
			"path (not a hardcoded dataset_id), re-running `tofu apply` right after this one "+
			"will recreate them automatically; anything that exists only in the Dremio UI will "+
			"not come back on its own. To proceed anyway, set %s=1 in the environment running "+
			"`tofu apply`, for this one run.",
		sourceName, fields, metadataImpactGuardEnvVar,
	)
}

// diffAPIConfigFields lists the apiConfig keys whose value differs between
// old and new, for guardAgainstMetadataImpact's error message. "password" is
// excluded: Dremio never echoes it back on Read, so the "old" side is always
// blank regardless of the real stored value, making any diff on it
// meaningless (the guard's block/proceed decision never depends on this list
// either way - it only comes from Dremio's own isMetadataImpacting answer).
func diffAPIConfigFields(old, new map[string]interface{}) []string {
	seen := map[string]bool{}
	var changed []string
	for k, v := range new {
		if k == "password" {
			continue
		}
		seen[k] = true
		if ov, ok := old[k]; !ok || !reflect.DeepEqual(ov, v) {
			changed = append(changed, k)
		}
	}
	for k := range old {
		if k == "password" || seen[k] {
			continue
		}
		changed = append(changed, k)
	}
	return changed
}

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
						// Type-specific fields across this whole block (NAS/MSSQL-only
						// here, Nessie-only and JDBC-family further down) are
						// Optional+Computed with NO static Default. Two failure modes
						// ruled this out, both found live:
						//   1. Plain Optional (no Computed at all) crashes with
						//      "Provider produced inconsistent result after apply" the
						//      moment a caller leaves the field unset AND Dremio's API
						//      returns a concrete value for it (even a zero value like
						//      queryTimeoutSec=0) - applyAPIConfigToModel unconditionally
						//      writes a known value, which conflicts with the null a
						//      non-Computed Optional attribute is required to keep.
						//      Confirmed live creating a throwaway MYSQL source without
						//      setting fetch_size/idle_time_sec/etc.
						//   2. A static Default (Computed+Default, like GCS's fields
						//      below) plans a concrete value for every OTHER type's
						//      resources too, the first time the field is added -
						//      confirmed live as a "+cache_percent=70" style ghost diff
						//      on the real c/h/sw_central sources when MYSQL/POSTGRES
						//      shipped, even though those types never read that field.
						// Computed with no Default avoids both: any real value Dremio
						// returns is acceptable (no crash), and for types that don't
						// use the field, the one-time diff after adding it is an honest
						// "(known after apply)" rather than a misleading concrete value.
						"mount_path":          schema.StringAttribute{Optional: true, Computed: true},
						"username":            schema.StringAttribute{Optional: true, Computed: true},
						"hostname":            schema.StringAttribute{Optional: true, Computed: true},
						"port":                schema.StringAttribute{Optional: true, Computed: true},
						"authentication_type": schema.StringAttribute{Optional: true, Computed: true},
						"fetch_size":          schema.Int64Attribute{Optional: true, Computed: true},
						"database":            schema.StringAttribute{Optional: true, Computed: true},
						"show_only_connection_database": schema.BoolAttribute{
							Optional: true, Computed: true,
						},
						// These values are defaults only for GCS. They cannot have schema
						// defaults because config is shared with JDBC sources: Dremio does
						// not return them for JDBC, causing a perpetual update after import.
						"project_id": schema.StringAttribute{Optional: true, Computed: true},
						"auth_mode": schema.StringAttribute{
							Optional: true, Computed: true,
							Validators: []validator.String{stringvalidator.OneOf(gcsAuthModes...)},
						},
						"root_path": schema.StringAttribute{Optional: true, Computed: true},
						"bucket_whitelist": schema.ListAttribute{
							Optional:    true,
							ElementType: types.StringType,
						},
						"async_enabled":  schema.BoolAttribute{Optional: true, Computed: true},
						"caching_enable": schema.BoolAttribute{Optional: true, Computed: true},
						"cache_percent": schema.Int64Attribute{
							Optional: true, Computed: true,
							Validators: []validator.Int64{int64validator.Between(1, 100)},
						},
						"client_email":   schema.StringAttribute{Optional: true, Computed: true},
						"client_id":      schema.StringAttribute{Optional: true, Computed: true},
						"private_key_id": schema.StringAttribute{Optional: true, Computed: true},
						// Nessie-only fields: Optional+Computed, no Default - see the
						// comment above mount_path etc. for why.
						"nessie_endpoint": schema.StringAttribute{Optional: true, Computed: true},
						"nessie_auth_type": schema.StringAttribute{
							Optional: true, Computed: true,
							Validators: []validator.String{stringvalidator.OneOf(nessieAuthTypes...)},
						},
						"secure": schema.BoolAttribute{Optional: true, Computed: true},
						"storage_provider": schema.StringAttribute{
							Optional: true, Computed: true,
							Validators: []validator.String{stringvalidator.OneOf(nessieStorageProviders...)},
						},
						"credential_type": schema.StringAttribute{
							Optional: true, Computed: true,
							Validators: []validator.String{stringvalidator.OneOf(nessieCredentialTypes...)},
						},
						// JDBC-family fields (MYSQL/POSTGRES/MSSQL): Optional+Computed, no
						// Default - see the comment above mount_path etc. for why. There's
						// no enum validator on authentication_type or
						// encryption_validation_mode: unlike GCS/NESSIE, these are closed
						// Enterprise connectors with no public Java class to confirm the
						// full set of legal values against - only "MASTER" and
						// "CERTIFICATE_AND_HOSTNAME_VALIDATION" have actually been observed.
						"max_idle_conns":             schema.Int64Attribute{Optional: true, Computed: true},
						"idle_time_sec":              schema.Int64Attribute{Optional: true, Computed: true},
						"query_timeout_sec":          schema.Int64Attribute{Optional: true, Computed: true},
						"use_ssl":                    schema.BoolAttribute{Optional: true, Computed: true},
						"net_write_timeout":          schema.Int64Attribute{Optional: true, Computed: true},
						"encryption_validation_mode": schema.StringAttribute{Optional: true, Computed: true},
						"enable_server_verification": schema.BoolAttribute{Optional: true, Computed: true},
						"user_impersonation":         schema.BoolAttribute{Optional: true, Computed: true},
					},
				},
			},
			"secure_config": schema.ListNestedBlock{
				Validators: []validator.List{listvalidator.SizeAtMost(1)},
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"password": schema.StringAttribute{
							Optional:  true,
							Sensitive: true,
							MarkdownDescription: "Must be a Dremio credential provider URI, not a literal password: " +
								"`env:VARNAME` (reads an environment variable on the Dremio server process) or " +
								"`file:///path/to/secret` (reads a file on the Dremio server). This keeps plaintext " +
								"passwords out of Terraform config and state.",
							Validators: []validator.String{
								stringvalidator.RegexMatches(
									secureConfigPasswordPattern,
									"must be a Dremio credential provider URI (env:VARNAME or file:///path/to/secret), not a literal password - see https://docs.dremio.com for CredentialsProvider details",
								),
							},
						},
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

	diags = r.guardAgainstMetadataImpact(ctx, state, apiConfig)
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

// guardAgainstMetadataImpact asks Dremio itself - via the same
// POST /apiv2/sources/isMetadataImpacting check its own UI "Warning" dialog
// is driven by - whether updating to newConfig would be a metadata-impacting
// change for this source, and unless metadataImpactGuardEnvVar is set,
// blocks the apply instead of silently letting Dremio delete and rediscover
// every dataset under the source (live-verified against dev Dremio 26.0.5:
// this happens unconditionally and without warning at the API level, only
// the UI asks first). If the check itself can't be completed (older/newer
// Dremio without this endpoint, network error), the change is treated as
// impacting rather than assumed safe - the same env var also covers that
// case, so an operator isn't stuck if the check endpoint is ever unavailable.
func (r *sourceResource) guardAgainstMetadataImpact(ctx context.Context, state sourceResourceModel, newConfig map[string]interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	allowed := os.Getenv(metadataImpactGuardEnvVar) != ""
	sourceName := state.Name.ValueString()

	sourceUI, err := r.client.GetSourceUI(sourceName)
	if err != nil {
		if allowed {
			return diags
		}
		diags.AddError("Metadata-impacting source change blocked", fmt.Sprintf(
			"Could not verify whether this change to source %q is metadata-impacting "+
				"(GET /apiv2/source/%s failed: %s). Treating it as impacting to be safe: "+
				"applying it may delete and rediscover every dataset under this source, "+
				"dropping reflections/formats/permissions with no further warning. "+
				"Set %s=1 to proceed anyway.", sourceName, sourceName, err.Error(), metadataImpactGuardEnvVar))
		return diags
	}

	oldConfig, oldDiags := sourceConfigToAPIConfig(ctx, state.Type.ValueString(), configOf(state), secureConfigOf(state))
	diags.Append(oldDiags...)
	if diags.HasError() {
		return diags
	}

	mutated := make(map[string]interface{}, len(sourceUI))
	for k, v := range sourceUI {
		mutated[k] = v
	}
	mutated["config"] = newConfig

	impacting, err := r.client.IsSourceConfigMetadataImpacting(mutated)
	if err != nil {
		if allowed {
			return diags
		}
		diags.AddError("Metadata-impacting source change blocked", fmt.Sprintf(
			"Could not verify whether this change to source %q is metadata-impacting "+
				"(POST /apiv2/sources/isMetadataImpacting failed: %s). Treating it as impacting "+
				"to be safe: applying it may delete and rediscover every dataset under this "+
				"source, dropping reflections/formats/permissions with no further warning. "+
				"Set %s=1 to proceed anyway.", sourceName, err.Error(), metadataImpactGuardEnvVar))
		return diags
	}

	block, message := decideMetadataImpactGuard(impacting, allowed, sourceName, diffAPIConfigFields(oldConfig, newConfig))
	if block {
		diags.AddError("Metadata-impacting source change blocked", message)
	}
	return diags
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

func stringOrDefault(value types.String, fallback string) string {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueString()
}

func boolOrDefault(value types.Bool, fallback bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueBool()
}

func int64OrDefault(value types.Int64, fallback int64) int64 {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueInt64()
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
			"projectId":       stringOrDefault(config.ProjectID, ""),
			"authMode":        stringOrDefault(config.AuthMode, "AUTO"),
			"rootPath":        stringOrDefault(config.RootPath, "/"),
			"bucketWhitelist": bucketWhitelist,
			"asyncEnabled":    boolOrDefault(config.AsyncEnabled, true),
			"cachingEnable":   boolOrDefault(config.CachingEnable, true),
			"cachePercent":    int64OrDefault(config.CachePercent, 70),
			"clientEmail":     stringOrDefault(config.ClientEmail, ""),
			"clientId":        stringOrDefault(config.ClientID, ""),
			"privateKeyId":    stringOrDefault(config.PrivateKeyID, ""),
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

	// Every Optional+Computed-without-Default field (everything below) needs
	// the same treatment, for a different reason: unlike GCS/NESSIE's
	// Computed+Default fields (project_id, auth_mode, etc. - Terraform
	// resolves those to a known Default at plan time even when a type never
	// sets them, so they're never a problem here), these fields plan as
	// *unknown* when the config leaves them unset, and if the current
	// type's case below doesn't touch them, that unknown value would still
	// be sitting in the final state - Framework hard-errors on that with
	// "Provider returned invalid result object after apply". Confirmed live
	// creating a throwaway MYSQL source: fields NESSIE/MSSQL-only fields
	// like `secure`/`use_ssl` stayed unknown and crashed apply. Resetting
	// them all to a known null first, then letting each case below
	// overwrite only the ones it actually uses, fixes it the same way
	// bucket_whitelist already worked above.
	model.MountPath = types.StringNull()
	model.Username = types.StringNull()
	model.Hostname = types.StringNull()
	model.Port = types.StringNull()
	model.AuthenticationType = types.StringNull()
	model.FetchSize = types.Int64Null()
	model.Database = types.StringNull()
	model.ShowOnlyConnectionDatabase = types.BoolNull()
	model.NessieEndpoint = types.StringNull()
	model.NessieAuthType = types.StringNull()
	model.Secure = types.BoolNull()
	model.StorageProvider = types.StringNull()
	model.CredentialType = types.StringNull()
	model.MaxIdleConns = types.Int64Null()
	model.IdleTimeSec = types.Int64Null()
	model.QueryTimeoutSec = types.Int64Null()
	model.UseSsl = types.BoolNull()
	model.NetWriteTimeout = types.Int64Null()
	model.EncryptionValidationMode = types.StringNull()
	model.EnableServerVerification = types.BoolNull()
	model.UserImpersonation = types.BoolNull()

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
