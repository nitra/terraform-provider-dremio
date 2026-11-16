package dremio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

func TestUserProfileUpdateDoesNotTouchCredentialsOrPermissions(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPut || req.URL.Path != "/api/v3/user/existing-id" {
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := dapi.NewClient(server.URL, dapi.Config{ApiKey: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	err = client.UpdateUserProfile(dapi.User{ID: "existing-id", Name: "existing-user", FirstName: "New", LastName: "Name", Email: "user@example.com", Tag: "observed-version", Active: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password", "roles", "active", "identityType"} {
		if _, ok := body[key]; ok {
			t.Errorf("update must not include %s", key)
		}
	}
	if body["tag"] != "observed-version" {
		t.Error("update lost optimistic concurrency version")
	}
	if body["name"] != "existing-user" || body["firstName"] != "New" {
		t.Error("incorrect profile mapping")
	}
}

func TestUserReadHandlesMissingAndMalformedAccounts(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"existing", `{"id":"existing-id","name":"existing-user","tag":"v1","active":true}`, 200, false},
		{"missing", `{"errorMessage":"missing"}`, 404, true},
		{"wrong identity", `{"id":"other-id","name":"existing-user"}`, 200, true},
		{"empty response", `{}`, 200, true},
		{"enterprise account", `{"id":"existing-id","name":"existing-user","@type":"EnterpriseUser"}`, 200, true},
		{"account with roles", `{"id":"existing-id","name":"existing-user","roles":[]}`, 200, true},
		{"external account", `{"id":"existing-id","name":"existing-user","source":"external"}`, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client, err := dapi.NewClient(server.URL, dapi.Config{ApiKey: "test-token"})
			if err != nil {
				t.Fatal(err)
			}
			user, err := client.GetUser("existing-id")
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
			}
			if !tc.wantErr && (!user.Active || user.Name != "existing-user") {
				t.Fatal("incorrect user read")
			}
			if tc.status == 404 && !isNotFoundError(err) {
				t.Fatal("missing user was not detected")
			}
		})
	}
}

func userState(t *testing.T, model userResourceModel) tfsdk.State {
	t.Helper()
	var schema resource.SchemaResponse
	(&userResource{}).Schema(context.Background(), resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	if diags := state.Set(context.Background(), model); diags.HasError() {
		t.Fatal(diags)
	}
	return state
}

func TestUserCreateUsesWriteOnlyInitialPassword(t *testing.T) {
	var body map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/api/v3/user" {
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"id":"new-id","name":"new-user","firstName":"New","lastName":"User","email":"new@example.com","active":true}`))
	}))
	defer server.Close()
	client, err := dapi.NewClient(server.URL, dapi.Config{ApiKey: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	plan := userResourceModel{ID: types.StringUnknown(), Name: types.StringValue("new-user"), FirstName: types.StringValue("New"), LastName: types.StringValue("User"), Email: types.StringValue("new@example.com"), InitialPasswordWO: types.StringNull()}
	config := plan
	config.InitialPasswordWO = types.StringValue("initial-password")
	state := userState(t, plan)
	configState := userState(t, config)
	response := resource.CreateResponse{}
	response.State.Schema = state.Schema
	(&userResource{client: client}).Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}, Config: tfsdk.Config{Schema: configState.Schema, Raw: configState.Raw}}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if body["password"] != "initial-password" || len(body) != 5 {
		t.Fatalf("unexpected create body: %v", body)
	}
	var created userResourceModel
	if diags := response.State.Get(context.Background(), &created); diags.HasError() {
		t.Fatal(diags)
	}
	if created.ID.ValueString() != "new-id" || !created.InitialPasswordWO.IsNull() {
		t.Fatal("create kept a write-only password or lost the created identity")
	}
}

func TestUserCreateRequiresInitialPassword(t *testing.T) {
	r := &userResource{}
	state := userState(t, userResourceModel{ID: types.StringUnknown(), Name: types.StringValue("new-user"), FirstName: types.StringValue("New"), LastName: types.StringValue("User"), Email: types.StringValue("new@example.com"), InitialPasswordWO: types.StringNull()})
	create := resource.CreateResponse{}
	create.State.Schema = state.Schema
	r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}, Config: tfsdk.Config{Schema: state.Schema, Raw: state.Raw}}, &create)
	if !create.Diagnostics.HasError() {
		t.Fatal("account creation without a password must fail")
	}
}

func TestUserSchemaUsesSensitiveWriteOnlyInitialPassword(t *testing.T) {
	var response resource.SchemaResponse
	(&userResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	attribute, ok := response.Schema.Attributes["initial_password_wo"].(resourceschema.StringAttribute)
	if !ok || !attribute.Optional || !attribute.Sensitive || !attribute.WriteOnly || attribute.Computed {
		t.Fatal("initial password must be optional, sensitive, write-only and absent from state")
	}
}

func TestUserSchemaHasNoCredentialOrPermissionFields(t *testing.T) {
	var resp resource.SchemaResponse
	(&userResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	for _, key := range []string{"password", "roles", "permissions"} {
		if _, ok := resp.Schema.Attributes[key]; ok {
			t.Errorf("unexpected %s in schema", key)
		}
	}
}

// deleteScenario drives userResource.Delete against a fake Dremio and records
// every DELETE /apiv2/user request it makes.
type deleteScenario struct {
	user       string // body for GET /api/v3/user/u-1; empty means 404
	roles      string // body for GET /api/v3/rbac/roles; empty means 404
	memberOf   map[string]bool
	deletes    []string
	memberHits []string
}

func (sc *deleteScenario) run(t *testing.T, stateName string) resource.DeleteResponse {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v3/user/u-1":
			if sc.user == "" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(sc.user))
		case req.Method == http.MethodGet && req.URL.Path == "/api/v3/rbac/roles":
			if sc.roles == "" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(sc.roles))
		case req.Method == http.MethodGet && len(req.URL.Path) > len("/api/v3/rbac/memberships/"):
			sc.memberHits = append(sc.memberHits, req.URL.Path)
			for roleID, member := range sc.memberOf {
				if member && req.URL.Path == "/api/v3/rbac/memberships/"+roleID+"/USER/u-1" {
					_, _ = w.Write([]byte(`{"roleId":"` + roleID + `","memberType":"USER","memberId":"u-1","tag":"m1"}`))
					return
				}
			}
			http.Error(w, "not found", http.StatusNotFound)
		case req.Method == http.MethodDelete && req.URL.Path == "/apiv2/user/gone-user":
			sc.deletes = append(sc.deletes, req.URL.RequestURI())
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	client, err := dapi.NewClient(server.URL, dapi.Config{ApiKey: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	state := userState(t, userResourceModel{ID: types.StringValue("u-1"), Name: types.StringValue(stateName), FirstName: types.StringValue("G"), LastName: types.StringValue("U"), Email: types.StringValue("g@example.com"), InitialPasswordWO: types.StringNull(), Tag: types.StringValue("v1"), Active: types.BoolValue(true)})
	var response resource.DeleteResponse
	response.State = state
	(&userResource{client: client}).Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	return response
}

const (
	goneUserJSON = `{"id":"u-1","name":"gone-user","firstName":"G","lastName":"U","email":"g@example.com","tag":"v1","active":true}`
	rolesJSON    = `{"tag":"t1","roles":{"rbac:admin":"ADMIN","rbac:public":"PUBLIC","r1":"Analyst"}}`
)

func TestUserDeleteUsesLegacyEndpointWithObservedTag(t *testing.T) {
	sc := &deleteScenario{user: goneUserJSON, roles: rolesJSON}
	response := sc.run(t, "gone-user")
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if len(sc.deletes) != 1 || sc.deletes[0] != "/apiv2/user/gone-user?version=v1" {
		t.Fatalf("unexpected delete requests: %v", sc.deletes)
	}
	for _, hit := range sc.memberHits {
		if hit == "/api/v3/rbac/memberships/rbac:public/USER/u-1" {
			t.Fatal("implicit PUBLIC membership must not be checked")
		}
	}
	if len(sc.memberHits) != 2 {
		t.Fatalf("expected membership checks for admin and r1, got %v", sc.memberHits)
	}
}

func TestUserDeleteRefusesWhileRoleMember(t *testing.T) {
	sc := &deleteScenario{user: goneUserJSON, roles: rolesJSON, memberOf: map[string]bool{"r1": true}}
	response := sc.run(t, "gone-user")
	if !response.Diagnostics.HasError() {
		t.Fatal("deleting a role member must be refused")
	}
	if len(sc.deletes) != 0 {
		t.Fatalf("refused deletion still called DELETE: %v", sc.deletes)
	}
}

func TestUserDeleteIsIdempotentWhenAccountIsGone(t *testing.T) {
	sc := &deleteScenario{}
	response := sc.run(t, "gone-user")
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if len(sc.deletes) != 0 {
		t.Fatalf("missing account must not trigger DELETE: %v", sc.deletes)
	}
}

func TestUserDeleteRefusesWhenIdentityChanged(t *testing.T) {
	sc := &deleteScenario{user: goneUserJSON, roles: rolesJSON}
	response := sc.run(t, "someone-else")
	if !response.Diagnostics.HasError() {
		t.Fatal("name mismatch between state and server must be refused")
	}
	if len(sc.deletes) != 0 {
		t.Fatalf("identity mismatch still called DELETE: %v", sc.deletes)
	}
}

func TestUserDeleteWorksWithoutRBACAPI(t *testing.T) {
	sc := &deleteScenario{user: goneUserJSON}
	response := sc.run(t, "gone-user")
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if len(sc.deletes) != 1 {
		t.Fatalf("expected one DELETE without RBAC API, got %v", sc.deletes)
	}
}
