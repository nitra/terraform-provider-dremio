package dremio

import (
	"context"
	"reflect"
	"sort"
	"strings"
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

func TestSecureConfigPasswordPattern(t *testing.T) {
	valid := []string{
		"env:DB_PASSWORD",
		"file:///var/run/secrets/db-password",
		"file://relative/path",
	}
	for _, v := range valid {
		if !secureConfigPasswordPattern.MatchString(v) {
			t.Errorf("expected %q to match secureConfigPasswordPattern (env:/file: URI)", v)
		}
	}

	invalid := []string{
		"",
		"hunter2",
		"ENV:DB_PASSWORD",
		"envDB_PASSWORD",
		" env:DB_PASSWORD",
		"https://example.com/secret",
	}
	for _, v := range invalid {
		if secureConfigPasswordPattern.MatchString(v) {
			t.Errorf("expected %q NOT to match secureConfigPasswordPattern (literal password must be rejected)", v)
		}
	}
}

func TestDecideMetadataImpactGuard(t *testing.T) {
	cases := []struct {
		name         string
		impacting    bool
		allowed      bool
		wantBlock    bool
		wantInFields []string // substrings that must appear in message when blocked
	}{
		{name: "not impacting never blocks", impacting: false, allowed: false, wantBlock: false},
		{name: "not impacting ignores allowed", impacting: false, allowed: true, wantBlock: false},
		{name: "impacting and not allowed blocks", impacting: true, allowed: false, wantBlock: true,
			wantInFields: []string{"hostname", "DREMIO_ALLOW_METADATA_IMPACTING_CHANGE"}},
		{name: "impacting but allowed proceeds", impacting: true, allowed: true, wantBlock: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block, message := decideMetadataImpactGuard(tc.impacting, tc.allowed, "mysource", []string{"hostname", "port"})
			if block != tc.wantBlock {
				t.Fatalf("block = %v, want %v (message: %q)", block, tc.wantBlock, message)
			}
			if !tc.wantBlock && message != "" {
				t.Fatalf("expected empty message when not blocking, got %q", message)
			}
			for _, substr := range tc.wantInFields {
				if !strings.Contains(message, substr) {
					t.Errorf("expected message to mention %q, got %q", substr, message)
				}
			}
		})
	}
}

func TestDecideMetadataImpactGuard_NoChangedFields(t *testing.T) {
	// guardAgainstMetadataImpact's own error-path callers pass an empty
	// changedFields slice (the check failed before a diff could be computed) -
	// the message must still render sensibly instead of an empty field list.
	block, message := decideMetadataImpactGuard(true, false, "mysource", nil)
	if !block {
		t.Fatalf("expected block=true")
	}
	if !strings.Contains(message, "its configuration") {
		t.Errorf("expected a generic fallback phrase in message, got %q", message)
	}
}

func TestDiffAPIConfigFields(t *testing.T) {
	old := map[string]interface{}{
		"hostname":  "a",
		"port":      "1234",
		"password":  "ignored-old",
		"fetchSize": 200,
	}
	new := map[string]interface{}{
		"hostname": "b",    // changed
		"port":     "1234", // unchanged
		"password": "ignored-new",
		"username": "root", // added
	}

	got := diffAPIConfigFields(old, new)
	sort.Strings(got)
	want := []string{"fetchSize", "hostname", "username"} // fetchSize: removed, counts as changed
	sort.Strings(want)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diffAPIConfigFields() = %v, want %v", got, want)
	}
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
	_, diags := sourceConfigToAPIConfig(context.Background(), "ORACLE", sourceConfigModel{}, secureConfigModel{})
	if !diags.HasError() {
		t.Fatal("expected an error for an unsupported source type, got none")
	}
}

func TestSourceConfigToAPIConfig_MSSQL(t *testing.T) {
	config := sourceConfigModel{
		Hostname:                   types.StringValue("win-dev.gcp.abie.app"),
		Username:                   types.StringValue("nitra-viewer1"),
		AuthenticationType:         types.StringValue("MASTER"),
		FetchSize:                  types.Int64Value(200),
		Database:                   types.StringValue("SW_Central_SIU"),
		ShowOnlyConnectionDatabase: types.BoolValue(false),
		UseSsl:                     types.BoolValue(false),
		EnableServerVerification:   types.BoolValue(false),
		MaxIdleConns:               types.Int64Value(8),
		IdleTimeSec:                types.Int64Value(60),
		QueryTimeoutSec:            types.Int64Value(0),
		UserImpersonation:          types.BoolValue(false),
	}
	secure := secureConfigModel{Password: types.StringValue("s3cr3t")}

	got, diags := sourceConfigToAPIConfig(context.Background(), "MSSQL", config, secure)
	if diags.HasError() {
		t.Fatalf("sourceConfigToAPIConfig returned diagnostics: %v", diags)
	}

	want := map[string]interface{}{
		"hostname":                   "win-dev.gcp.abie.app",
		"username":                   "nitra-viewer1",
		"password":                   "s3cr3t",
		"authenticationType":         "MASTER",
		"fetchSize":                  int64(200),
		"database":                   "SW_Central_SIU",
		"showOnlyConnectionDatabase": false,
		"useSsl":                     false,
		"enableServerVerification":   false,
		"maxIdleConns":               int64(8),
		"idleTimeSec":                int64(60),
		"queryTimeoutSec":            int64(0),
		"userImpersonation":          false,
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

func TestApplyAPIConfigToModel_MSSQL_roundtrip(t *testing.T) {
	apiConfig := map[string]interface{}{
		"hostname":           "win-dev.gcp.abie.app",
		"username":           "nitra-viewer1",
		"authenticationType": "MASTER",
		"fetchSize":          float64(200),
		"database":           "SW_Central_SIU",
		"useSsl":             false,
		// port deliberately omitted here, mirroring the real sw_central
		// response: Dremio drops JDBC fields left at their zero/default
		// value entirely rather than serializing them.
		"maxIdleConns":    float64(8),
		"idleTimeSec":     float64(60),
		"queryTimeoutSec": float64(0),
	}

	model := sourceConfigModel{}
	diags := applyAPIConfigToModel("MSSQL", apiConfig, &model)
	if diags.HasError() {
		t.Fatalf("applyAPIConfigToModel returned diagnostics: %v", diags)
	}

	if got := model.Port.ValueString(); got != "" {
		t.Errorf("Port = %q, want empty (absent from the API response)", got)
	}
	if got := model.Hostname.ValueString(); got != "win-dev.gcp.abie.app" {
		t.Errorf("Hostname = %q, unexpected", got)
	}
	if got := model.MaxIdleConns.ValueInt64(); got != 8 {
		t.Errorf("MaxIdleConns = %d, want 8", got)
	}
}

func TestSourceConfigToAPIConfig_MYSQL(t *testing.T) {
	config := sourceConfigModel{
		Hostname:           types.StringValue("caps-db.dev-caps.svc.abie-ua.internal"),
		Port:               types.StringValue("3306"),
		Database:           types.StringValue("caps"),
		Username:           types.StringValue("caps"),
		AuthenticationType: types.StringValue("MASTER"),
		FetchSize:          types.Int64Value(200),
		NetWriteTimeout:    types.Int64Value(60),
		MaxIdleConns:       types.Int64Value(8),
		IdleTimeSec:        types.Int64Value(60),
		QueryTimeoutSec:    types.Int64Value(0),
	}
	secure := secureConfigModel{Password: types.StringValue("s3cr3t")}

	got, diags := sourceConfigToAPIConfig(context.Background(), "MYSQL", config, secure)
	if diags.HasError() {
		t.Fatalf("sourceConfigToAPIConfig returned diagnostics: %v", diags)
	}

	want := map[string]interface{}{
		"hostname":           "caps-db.dev-caps.svc.abie-ua.internal",
		"port":               "3306",
		"database":           "caps",
		"username":           "caps",
		"password":           "s3cr3t",
		"authenticationType": "MASTER",
		"fetchSize":          int64(200),
		"netWriteTimeout":    int64(60),
		"maxIdleConns":       int64(8),
		"idleTimeSec":        int64(60),
		"queryTimeoutSec":    int64(0),
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

func TestApplyAPIConfigToModel_MYSQL_roundtrip(t *testing.T) {
	apiConfig := map[string]interface{}{
		"hostname":           "caps-db.dev-caps.svc.abie-ua.internal",
		"port":               "3306",
		"database":           "caps",
		"username":           "caps",
		"authenticationType": "MASTER",
		"fetchSize":          float64(200),
		"netWriteTimeout":    float64(60),
		"maxIdleConns":       float64(8),
		"idleTimeSec":        float64(60),
		"queryTimeoutSec":    float64(0),
	}

	model := sourceConfigModel{}
	diags := applyAPIConfigToModel("MYSQL", apiConfig, &model)
	if diags.HasError() {
		t.Fatalf("applyAPIConfigToModel returned diagnostics: %v", diags)
	}

	if got := model.Database.ValueString(); got != "caps" {
		t.Errorf("Database = %q, want caps", got)
	}
	if got := model.NetWriteTimeout.ValueInt64(); got != 60 {
		t.Errorf("NetWriteTimeout = %d, want 60", got)
	}
}

func TestSourceConfigToAPIConfig_POSTGRES(t *testing.T) {
	config := sourceConfigModel{
		Hostname:                 types.StringValue("cluster-hasura-rw.dev-db.svc.abie-ua.internal"),
		Port:                     types.StringValue("5432"),
		Database:                 types.StringValue("hasura"),
		Username:                 types.StringValue("hasura"),
		AuthenticationType:       types.StringValue("MASTER"),
		FetchSize:                types.Int64Value(200),
		UseSsl:                   types.BoolValue(false),
		EncryptionValidationMode: types.StringValue("CERTIFICATE_AND_HOSTNAME_VALIDATION"),
		MaxIdleConns:             types.Int64Value(8),
		IdleTimeSec:              types.Int64Value(60),
		QueryTimeoutSec:          types.Int64Value(0),
	}
	secure := secureConfigModel{Password: types.StringValue("s3cr3t")}

	got, diags := sourceConfigToAPIConfig(context.Background(), "POSTGRES", config, secure)
	if diags.HasError() {
		t.Fatalf("sourceConfigToAPIConfig returned diagnostics: %v", diags)
	}

	// databaseName, not database - the one field POSTGRES names differently
	// from every other JDBC type modeled here.
	want := map[string]interface{}{
		"hostname":                 "cluster-hasura-rw.dev-db.svc.abie-ua.internal",
		"port":                     "5432",
		"databaseName":             "hasura",
		"username":                 "hasura",
		"password":                 "s3cr3t",
		"authenticationType":       "MASTER",
		"fetchSize":                int64(200),
		"useSsl":                   false,
		"encryptionValidationMode": "CERTIFICATE_AND_HOSTNAME_VALIDATION",
		"maxIdleConns":             int64(8),
		"idleTimeSec":              int64(60),
		"queryTimeoutSec":          int64(0),
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
	if _, ok := got["database"]; ok {
		t.Errorf("config unexpectedly has a \"database\" key; POSTGRES uses databaseName")
	}
}

func TestApplyAPIConfigToModel_POSTGRES_roundtrip(t *testing.T) {
	apiConfig := map[string]interface{}{
		"hostname":                 "cluster-hasura-rw.dev-db.svc.abie-ua.internal",
		"port":                     "5432",
		"databaseName":             "hasura",
		"username":                 "hasura",
		"authenticationType":       "MASTER",
		"fetchSize":                float64(200),
		"useSsl":                   false,
		"encryptionValidationMode": "CERTIFICATE_AND_HOSTNAME_VALIDATION",
		"maxIdleConns":             float64(8),
		"idleTimeSec":              float64(60),
		"queryTimeoutSec":          float64(0),
	}

	model := sourceConfigModel{}
	diags := applyAPIConfigToModel("POSTGRES", apiConfig, &model)
	if diags.HasError() {
		t.Fatalf("applyAPIConfigToModel returned diagnostics: %v", diags)
	}

	if got := model.Database.ValueString(); got != "hasura" {
		t.Errorf("Database = %q, want hasura (read from databaseName)", got)
	}
	if got := model.EncryptionValidationMode.ValueString(); got != "CERTIFICATE_AND_HOSTNAME_VALIDATION" {
		t.Errorf("EncryptionValidationMode = %q, unexpected", got)
	}
}
