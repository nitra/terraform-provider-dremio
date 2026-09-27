package dremio

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func mustStringList(t *testing.T, values []string) types.List {
	t.Helper()
	l, diags := types.ListValueFrom(context.Background(), types.StringType, values)
	if diags.HasError() {
		t.Fatalf("building string list: %v", diags)
	}
	return l
}

func TestSourceConfigToAPIConfig_GCS(t *testing.T) {
	config := sourceConfigModel{
		ProjectID:       types.StringValue("abie-ua"),
		AuthMode:        types.StringValue("AUTO"),
		RootPath:        types.StringValue("/"),
		BucketWhitelist: mustStringList(t, []string{"tempo-7d"}),
		AsyncEnabled:    types.BoolValue(true),
		CachingEnable:   types.BoolValue(true),
		CachePercent:    types.Int64Value(70),
		ClientEmail:     types.StringValue(""),
		ClientID:        types.StringValue(""),
		PrivateKeyID:    types.StringValue(""),
	}

	got, diags := sourceConfigToAPIConfig(context.Background(), "GCS", config, secureConfigModel{})
	if diags.HasError() {
		t.Fatalf("sourceConfigToAPIConfig returned diagnostics: %v", diags)
	}

	want := map[string]interface{}{
		"projectId":       "abie-ua",
		"authMode":        "AUTO",
		"rootPath":        "/",
		"bucketWhitelist": []string{"tempo-7d"},
		"asyncEnabled":    true,
		"cachingEnable":   true,
		"cachePercent":    int64(70),
		"clientEmail":     "",
		"clientId":        "",
		"privateKeyId":    "",
		"privateKey":      "",
	}

	for k, wantV := range want {
		gotV, ok := got[k]
		if !ok {
			t.Errorf("missing key %q in config", k)
			continue
		}
		if !reflect.DeepEqual(gotV, wantV) {
			t.Errorf("config[%q] = %#v, want %#v", k, gotV, wantV)
		}
	}
}

func TestApplyAPIConfigToModel_GCS_roundtrip(t *testing.T) {
	apiConfig := map[string]interface{}{
		"projectId":       "abie-ua",
		"authMode":        "AUTO",
		"rootPath":        "/",
		"bucketWhitelist": []interface{}{"tempo-7d"},
		"asyncEnabled":    true,
		"cachingEnable":   true,
		"cachePercent":    float64(70), // Dremio's JSON numbers decode as float64
		"clientEmail":     "",
		"clientId":        "",
		"privateKeyId":    "",
	}

	var model sourceConfigModel
	diags := applyAPIConfigToModel("GCS", apiConfig, &model)
	if diags.HasError() {
		t.Fatalf("applyAPIConfigToModel returned diagnostics: %v", diags)
	}

	if got := model.ProjectID.ValueString(); got != "abie-ua" {
		t.Errorf("ProjectID = %q, want %q", got, "abie-ua")
	}
	if got := model.CachePercent.ValueInt64(); got != 70 {
		t.Errorf("CachePercent = %d, want 70", got)
	}
	var whitelist []string
	if d := model.BucketWhitelist.ElementsAs(context.Background(), &whitelist, false); d.HasError() {
		t.Fatalf("reading BucketWhitelist: %v", d)
	}
	if len(whitelist) != 1 || whitelist[0] != "tempo-7d" {
		t.Errorf("BucketWhitelist = %v, want [tempo-7d]", whitelist)
	}
}

func TestApplyAPIConfigToModel_GCS_nilBucketWhitelist(t *testing.T) {
	apiConfig := map[string]interface{}{
		"projectId":       "abie-ua",
		"authMode":        "AUTO",
		"rootPath":        "/",
		"bucketWhitelist": nil,
		"asyncEnabled":    true,
		"cachingEnable":   true,
		"cachePercent":    float64(70),
		"clientEmail":     "",
		"clientId":        "",
		"privateKeyId":    "",
	}

	var model sourceConfigModel
	diags := applyAPIConfigToModel("GCS", apiConfig, &model)
	if diags.HasError() {
		t.Fatalf("applyAPIConfigToModel returned diagnostics: %v", diags)
	}

	var whitelist []string
	if d := model.BucketWhitelist.ElementsAs(context.Background(), &whitelist, false); d.HasError() {
		t.Fatalf("reading BucketWhitelist: %v", d)
	}
	if len(whitelist) != 0 {
		t.Errorf("whitelist length = %d, want 0", len(whitelist))
	}
}

func TestSourceConfigToAPIConfig_NESSIE(t *testing.T) {
	config := sourceConfigModel{
		ProjectID:       types.StringValue("abie-ua"),
		AuthMode:        types.StringValue("AUTO"),
		RootPath:        types.StringValue("dev-nessie-warehouse-abie-ua/warehouse"),
		AsyncEnabled:    types.BoolValue(true),
		CachingEnable:   types.BoolValue(true),
		CachePercent:    types.Int64Value(100),
		NessieEndpoint:  types.StringValue("http://nessie.dev-nessie.svc.abie-ua.internal:19120/api/v2"),
		NessieAuthType:  types.StringValue("NONE"),
		Secure:          types.BoolValue(false),
		StorageProvider: types.StringValue("GOOGLE"),
		CredentialType:  types.StringValue("ACCESS_KEY"),
	}

	got, diags := sourceConfigToAPIConfig(context.Background(), "NESSIE", config, secureConfigModel{})
	if diags.HasError() {
		t.Fatalf("sourceConfigToAPIConfig returned diagnostics: %v", diags)
	}

	want := map[string]interface{}{
		"asyncEnabled":             true,
		"isCachingEnabled":         true,
		"maxCacheSpacePct":         int64(100),
		"storageProvider":          "GOOGLE",
		"googleProjectId":          "abie-ua",
		"googleAuthenticationType": "AUTO",
		"googleRootPath":           "dev-nessie-warehouse-abie-ua/warehouse",
		"nessieEndpoint":           "http://nessie.dev-nessie.svc.abie-ua.internal:19120/api/v2",
		"nessieAuthType":           "NONE",
		"secure":                   false,
		"credentialType":           "ACCESS_KEY",
	}

	for k, wantV := range want {
		gotV, ok := got[k]
		if !ok {
			t.Errorf("missing key %q in config", k)
			continue
		}
		if !reflect.DeepEqual(gotV, wantV) {
			t.Errorf("config[%q] = %#v, want %#v", k, gotV, wantV)
		}
	}
}

func TestApplyAPIConfigToModel_NESSIE_roundtrip(t *testing.T) {
	apiConfig := map[string]interface{}{
		"asyncEnabled":             true,
		"isCachingEnabled":         true,
		"maxCacheSpacePct":         float64(100), // Dremio's JSON numbers decode as float64
		"storageProvider":          "GOOGLE",
		"googleProjectId":          "abie-ua",
		"googleAuthenticationType": "AUTO",
		"googleRootPath":           "iceberg-data-abie",
		"googlePrivateKeyId":       "",
		"googleClientEmail":        "",
		"googleClientId":           "",
		"nessieEndpoint":           "http://nessie.nessie.svc.abie-ua.internal:19120/api/v2",
		"nessieAuthType":           "NONE",
		"secure":                   false,
		"credentialType":           "ACCESS_KEY",
	}

	var model sourceConfigModel
	diags := applyAPIConfigToModel("NESSIE", apiConfig, &model)
	if diags.HasError() {
		t.Fatalf("applyAPIConfigToModel returned diagnostics: %v", diags)
	}

	if got := model.ProjectID.ValueString(); got != "abie-ua" {
		t.Errorf("ProjectID = %q, want %q", got, "abie-ua")
	}
	if got := model.RootPath.ValueString(); got != "iceberg-data-abie" {
		t.Errorf("RootPath = %q, want %q", got, "iceberg-data-abie")
	}
	if got := model.CachePercent.ValueInt64(); got != 100 {
		t.Errorf("CachePercent = %d, want 100", got)
	}
	if got := model.Secure.ValueBool(); got != false {
		t.Errorf("Secure = %v, want false", got)
	}
	if got := model.NessieEndpoint.ValueString(); got != "http://nessie.nessie.svc.abie-ua.internal:19120/api/v2" {
		t.Errorf("NessieEndpoint = %q, unexpected", got)
	}
}

func TestSourceConfigToAPIConfig_UnsupportedType(t *testing.T) {
	_, diags := sourceConfigToAPIConfig(context.Background(), "POSTGRES", sourceConfigModel{}, secureConfigModel{})
	if !diags.HasError() {
		t.Fatal("expected an error for an unsupported source type, got none")
	}
}
