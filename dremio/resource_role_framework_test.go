package dremio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

func roleClient(t *testing.T, handler http.HandlerFunc, retry bool) *dapi.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	config := dapi.Config{ApiKey: "test-token"}
	if retry {
		config.Client = &http.Client{Transport: &retryTransport{base: http.DefaultTransport, maxRetries: 3}}
	}
	client, err := dapi.NewClient(server.URL, config)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func roleState(t *testing.T, model roleResourceModel) tfsdk.State {
	t.Helper()
	var schema resource.SchemaResponse
	(&roleResource{}).Schema(context.Background(), resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	if diags := state.Set(context.Background(), model); diags.HasError() {
		t.Fatal(diags)
	}
	return state
}

func TestRoleResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	exists := false
	writes := 0
	client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Header.Get("Authorization") != "_dremiotest-token" {
			t.Error("authentication missing")
		}
		switch {
		case req.Method == "GET" && req.URL.Path == "/api/v3/rbac/roles":
			_, _ = w.Write([]byte(`{"tag":"v1","roles":{"rbac:admin":"ADMIN","rbac:public":"PUBLIC"}}`))
		case req.Method == "POST" && req.URL.Path == "/api/v3/rbac/roles":
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["tag"] != "v1" || body["name"] != "Analyst" || len(body) != 2 {
				t.Errorf("invalid create body: %v", body)
			}
			exists = true
			writes++
			_, _ = w.Write([]byte(`{"id":"role-id","name":"Analyst","tag":"v2"}`))
		case req.Method == "GET" && req.URL.Path == "/api/v3/rbac/roles/role-id":
			if !exists {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write([]byte(`{"id":"role-id","name":"Analyst","tag":"v2"}`))
		case req.Method == "DELETE" && req.URL.Path == "/api/v3/rbac/roles/role-id":
			if req.URL.Query().Get("tag") != "v2" {
				t.Error("delete lost current OCC tag")
			}
			exists = false
			writes++
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			w.WriteHeader(500)
		}
	}, false)
	r := &roleResource{client: client}
	initial := roleState(t, roleResourceModel{ID: types.StringUnknown(), Name: types.StringValue("Analyst"), Tag: types.StringUnknown()})
	var create resource.CreateResponse
	create.State.Schema = initial.Schema
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: initial.Schema, Raw: initial.Raw}}, &create)
	if create.Diagnostics.HasError() {
		t.Fatal(create.Diagnostics)
	}
	var read resource.ReadResponse
	read.State = create.State
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.Equal(create.State.Raw) {
		t.Fatalf("refresh changed state: %v", read.Diagnostics)
	}
	var update resource.UpdateResponse
	update.State = read.State
	r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: tfsdk.Plan{Schema: read.State.Schema, Raw: read.State.Raw}}, &update)
	if update.Diagnostics.HasError() || !update.State.Raw.Equal(read.State.Raw) || writes != 1 {
		t.Fatalf("no-op update wrote to server: %v", update.Diagnostics)
	}
	var remove resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: read.State}, &remove)
	if remove.Diagnostics.HasError() || exists || writes != 2 {
		t.Fatalf("delete failed: %v", remove.Diagnostics)
	}
	read.State = create.State
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatal("missing stable ID must remove resource from state")
	}
}

func TestRoleReadRejectsErrorsAndMalformedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"forbidden", `{}`, 403}, {"misleading forbidden body", `{"message":"status: 404"}`, 403}, {"conflict", `{}`, 409}, {"disabled", `{}`, 503},
		{"empty", `{}`, 200}, {"wrong ID", `{"id":"different","name":"Analyst","tag":"v1"}`, 200},
		{"missing tag", `{"id":"role-id","name":"Analyst"}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}, false)
			state := roleState(t, roleResourceModel{ID: types.StringValue("role-id"), Name: types.StringValue("Analyst"), Tag: types.StringValue("v1")})
			resp := resource.ReadResponse{State: state}
			(&roleResource{client: client}).Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if !resp.Diagnostics.HasError() || resp.State.Raw.IsNull() {
				t.Fatal("read must fail without forgetting an existing role")
			}
		})
	}
}

func TestRoleMutationNeverRetriesAmbiguousServerResponse(t *testing.T) {
	for _, method := range []string{"POST", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			attempts := 0
			client := roleClient(t, func(w http.ResponseWriter, req *http.Request) { attempts++; w.WriteHeader(500) }, true)
			var err error
			if method == "POST" {
				_, err = client.CreateRBACRole("Analyst", "v1")
			} else {
				err = client.DeleteRBACRole("role-id", "v1")
			}
			if err == nil || attempts != 1 {
				t.Fatalf("ambiguous write was replayed: attempts=%d err=%v", attempts, err)
			}
		})
	}
}

func TestRoleSystemMutationIsBlockedBeforeRequest(t *testing.T) {
	client := roleClient(t, func(w http.ResponseWriter, req *http.Request) { t.Error("system mutation reached server") }, false)
	for _, role := range []struct{ id, name string }{{"rbac:admin", "ADMIN"}, {"rbac:public", "PUBLIC"}} {
		if _, err := client.CreateRBACRole(role.name, "v1"); err == nil {
			t.Fatal("system role creation allowed")
		}
		if err := client.DeleteRBACRole(role.id, "v1"); err == nil {
			t.Fatal("system role deletion allowed")
		}
	}
}
