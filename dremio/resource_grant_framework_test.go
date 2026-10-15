package dremio

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

func grantState(t *testing.T, unknown bool) tfsdk.State {
	t.Helper()
	var response resource.SchemaResponse
	(&grantResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	state := tfsdk.State{Schema: response.Schema}
	var m grantModel
	m.read(&dapi.RBACGrant{ObjectType: "SPACE", ObjectID: "space-id", GranteeType: "ROLE", GranteeID: "role-id", Privilege: "SELECT", Tag: "v1"})
	if unknown {
		m.ID = types.StringUnknown()
		m.Tag = types.StringUnknown()
	}
	if diags := state.Set(context.Background(), &m); diags.HasError() {
		t.Fatal(diags)
	}
	return state
}

func TestGrantKeyIsTypedCanonicalAndRejectsALL(t *testing.T) {
	g := dapi.RBACGrant{ObjectType: "VDS", ObjectID: "dataset-id", GranteeType: "USER", GranteeID: "uid", Privilege: "SELECT"}
	key, err := g.Key()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := dapi.ParseGrantID(key)
	if err != nil || *parsed != g {
		t.Fatalf("invalid roundtrip: %v %v", parsed, err)
	}
	for _, id := range []string{"", key + "=", "name/SELECT", "W10"} {
		if _, err := dapi.ParseGrantID(id); err == nil {
			t.Fatalf("accepted invalid key: %q", id)
		}
	}
	g.Privilege = "ALL"
	if _, err := g.Key(); err == nil {
		t.Fatal("ALL must not be one managed grant")
	}
	g.Privilege = "SELECT"
	g.ObjectType = "PROJECT"
	if _, err := g.Key(); err == nil {
		t.Fatal("PROJECT must target SYSTEM")
	}
}

func TestGrantResourceLifecyclePreservesOtherPrivileges(t *testing.T) {
	ctx := context.Background()
	selectExists := false
	alterExists := true
	writes := 0
	client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/v3/rbac/grant-authority/SPACE/space-id" && req.Method == "GET" {
			_, _ = w.Write([]byte(`{"tag":"v1"}`))
			return
		}
		if req.URL.Path != "/api/v3/rbac/grants/SPACE/space-id/ROLE/role-id/SELECT" {
			t.Errorf("wrong target %s", req.URL)
			w.WriteHeader(500)
			return
		}
		switch req.Method {
		case "GET":
			if !selectExists {
				w.WriteHeader(404)
				return
			}
		case "PUT":
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || len(body) != 1 || body["tag"] != "v1" {
				t.Errorf("unexpected mutation body: %v %v", body, err)
			}
			selectExists = true
			writes++
		case "DELETE":
			if req.URL.Query().Get("tag") != "v2" {
				t.Error("lost OCC tag")
			}
			selectExists = false
			writes++
			w.WriteHeader(204)
			return
		default:
			t.Errorf("unexpected method: %s", req.Method)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(dapi.RBACGrant{ObjectType: "SPACE", ObjectID: "space-id", GranteeType: "ROLE", GranteeID: "role-id", Privilege: "SELECT", Tag: "v2"})
	}, false)
	r := &grantResource{roleResource: roleResource{client: client}}
	initial := grantState(t, true)
	create := resource.CreateResponse{State: initial}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: initial.Schema, Raw: initial.Raw}}, &create)
	if create.Diagnostics.HasError() {
		t.Fatal(create.Diagnostics)
	}
	read := resource.ReadResponse{State: create.State}
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.Equal(create.State.Raw) {
		t.Fatal("refresh must be a no-op")
	}
	update := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: tfsdk.Plan{Schema: read.State.Schema, Raw: read.State.Raw}}, &update)
	if update.Diagnostics.HasError() || writes != 1 {
		t.Fatal("no-op update changed privileges")
	}
	var remove resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: read.State}, &remove)
	if remove.Diagnostics.HasError() || selectExists || !alterExists || writes != 2 {
		t.Fatal("delete must revoke only SELECT")
	}
	read.State = create.State
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatal("explicit grant 404 must remove resource")
	}
}

func TestGrantReadDoesNotForgetDeniedOrMismatchedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"forbidden", `{"message":"status: 404"}`, 403}, {"conflict", `{}`, 409}, {"disabled", `{}`, 503}, {"malformed", `{}`, 200},
		{"wrong object", `{"objectType":"SPACE","objectId":"other-id","granteeType":"ROLE","granteeId":"role-id","privilege":"SELECT","tag":"v1"}`, 200},
		{"wrong privilege", `{"objectType":"SPACE","objectId":"space-id","granteeType":"ROLE","granteeId":"role-id","privilege":"ALTER","tag":"v1"}`, 200},
		{"wrong UID", `{"objectType":"SPACE","objectId":"space-id","granteeType":"USER","granteeId":"role-id","privilege":"SELECT","tag":"v1"}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}, false)
			state := grantState(t, false)
			response := resource.ReadResponse{State: state}
			(&grantResource{roleResource: roleResource{client: client}}).Read(context.Background(), resource.ReadRequest{State: state}, &response)
			if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
				t.Fatal("must preserve state and report error")
			}
		})
	}
}

func TestGrantWritesAreNotAutomaticallyRetried(t *testing.T) {
	for _, method := range []string{"PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			attempts := 0
			client := roleClient(t, func(w http.ResponseWriter, req *http.Request) { attempts++; w.WriteHeader(500) }, true)
			g := dapi.RBACGrant{ObjectType: "SPACE", ObjectID: "space-id", GranteeType: "USER", GranteeID: "uid", Privilege: "SELECT"}
			var err error
			if method == "PUT" {
				_, err = client.PutRBACGrant(g, "v1")
			} else {
				err = client.DeleteRBACGrant(g, "v1")
			}
			if err == nil || attempts != 1 {
				t.Fatal("ambiguous grant mutation was replayed")
			}
		})
	}
}

func TestGrantCreateRequiresImportInsteadOfAdoptingExistingAccess(t *testing.T) {
	client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			t.Error("existing access was overwritten")
		}
		_ = json.NewEncoder(w).Encode(dapi.RBACGrant{ObjectType: "SPACE", ObjectID: "space-id", GranteeType: "ROLE", GranteeID: "role-id", Privilege: "SELECT", Tag: "v1"})
	}, false)
	state := grantState(t, true)
	response := resource.CreateResponse{State: state}
	(&grantResource{roleResource: roleResource{client: client}}).Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("existing explicit grant must require import")
	}
}
