package dremio

import (
	"context"
	"encoding/json"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
	"net/http"
	"testing"
)

func ownershipState(t *testing.T, unknown bool) tfsdk.State {
	t.Helper()
	var response resource.SchemaResponse
	(&ownershipResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	state := tfsdk.State{Schema: response.Schema}
	var m ownershipModel
	m.read(&dapi.RBACOwnership{ObjectType: "SPACE", ObjectID: "space-id", OwnerType: "USER", OwnerID: "reader-id", Tag: "v1"})
	if unknown {
		m.ID = types.StringUnknown()
		m.Tag = types.StringUnknown()
	}
	if diags := state.Set(context.Background(), &m); diags.HasError() {
		t.Fatal(diags)
	}
	return state
}
func TestOwnershipKeyIdentifiesTargetNotMutableOwner(t *testing.T) {
	o := dapi.RBACOwnership{ObjectType: "SPACE", ObjectID: "space-id"}
	key, err := o.Key()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := dapi.ParseOwnershipID(key)
	if err != nil || *parsed != o {
		t.Fatal("invalid roundtrip")
	}
	o.OwnerType = "ROLE"
	o.OwnerID = "role-id"
	same, _ := o.Key()
	if key != same {
		t.Fatal("owner must not change target key")
	}
	for _, id := range []string{"", key + "=", "W10", "space/name"} {
		if _, err := dapi.ParseOwnershipID(id); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
	o.ObjectType = "PROJECT"
	o.ObjectID = "SYSTEM"
	if _, err := o.Key(); err == nil {
		t.Fatal("SYSTEM ownership must be unsupported")
	}
}
func TestOwnershipResourceAssignTransferAndSafeCleanup(t *testing.T) {
	ctx := context.Background()
	exists, live := false, true
	writes := 0
	current := dapi.RBACOwnership{ObjectType: "SPACE", ObjectID: "space-id", OwnerType: "USER", OwnerID: "reader-id", Tag: "v1"}
	client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/v3/rbac/roles" {
			_ = json.NewEncoder(w).Encode(map[string]any{"tag": "v1", "roles": map[string]string{"rbac:admin": "ADMIN", "rbac:public": "PUBLIC"}})
			return
		}
		if req.URL.Path != "/api/v3/rbac/ownership/SPACE/space-id" {
			t.Errorf("wrong target %s", req.URL)
			w.WriteHeader(500)
			return
		}
		switch req.Method {
		case "GET":
			if !exists {
				w.WriteHeader(404)
				return
			}
		case "PUT":
			var body map[string]string
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body["tag"] != current.Tag || len(body) != 3 {
				t.Errorf("invalid body %v", body)
			}
			exists = true
			current.OwnerType = body["ownerType"]
			current.OwnerID = body["ownerId"]
			current.Tag = "v2"
			writes++
		case "DELETE":
			if req.URL.Query().Get("tag") != current.Tag || req.URL.Query().Get("ownerType") != current.OwnerType || req.URL.Query().Get("ownerId") != current.OwnerID {
				t.Error("cleanup must match exact owner and tag")
			}
			if live {
				w.WriteHeader(400)
				return
			}
			exists = false
			writes++
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(current)
	}, false)
	r := &ownershipResource{roleResource: roleResource{client: client}}
	initial := ownershipState(t, true)
	create := resource.CreateResponse{State: initial}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: initial.Schema, Raw: initial.Raw}}, &create)
	if create.Diagnostics.HasError() {
		t.Fatal(create.Diagnostics)
	}
	read := resource.ReadResponse{State: create.State}
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.Equal(create.State.Raw) {
		t.Fatal("refresh must be no-op")
	}
	duplicate := resource.CreateResponse{State: initial}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: initial.Schema, Raw: initial.Raw}}, &duplicate)
	if !duplicate.Diagnostics.HasError() || writes != 1 {
		t.Fatal("existing ownership must require import")
	}
	var desired ownershipModel
	_ = read.State.Get(ctx, &desired)
	desired.OwnerType = types.StringValue("ROLE")
	desired.OwnerID = types.StringValue("role-id")
	plan := tfsdk.Plan{Schema: read.State.Schema}
	if diags := plan.Set(ctx, &desired); diags.HasError() {
		t.Fatal(diags)
	}
	update := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: plan}, &update)
	if update.Diagnostics.HasError() || writes != 2 || current.OwnerID != "role-id" {
		t.Fatal(update.Diagnostics)
	}
	noop := resource.UpdateResponse{State: update.State}
	r.Update(ctx, resource.UpdateRequest{State: update.State, Plan: tfsdk.Plan{Schema: update.State.Schema, Raw: update.State.Raw}}, &noop)
	if noop.Diagnostics.HasError() || writes != 2 {
		t.Fatal("no-op transferred ownership")
	}
	var remove resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: update.State}, &remove)
	if !remove.Diagnostics.HasError() || !exists || writes != 2 {
		t.Fatal("live ownership must be retained")
	}
	live = false
	remove = resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: update.State}, &remove)
	if remove.Diagnostics.HasError() || exists || writes != 3 {
		t.Fatal(remove.Diagnostics)
	}
	read.State = update.State
	r.Read(ctx, resource.ReadRequest{State: update.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatal("404 must remove resource")
	}
}
func TestOwnershipReadPreservesDeniedOrMalformedState(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{403, `{"message":"status: 404"}`}, {409, `{}`}, {503, `{}`}, {200, `{}`},
		{200, `{"objectType":"SPACE","objectId":"different-id","ownerType":"USER","ownerId":"reader-id","tag":"v1"}`},
		{200, `{"objectType":"SPACE","objectId":"space-id","ownerType":"NAME","ownerId":"reader-id","tag":"v1"}`},
	} {
		client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}, false)
		state := ownershipState(t, false)
		resp := resource.ReadResponse{State: state}
		(&ownershipResource{roleResource: roleResource{client: client}}).Read(context.Background(), resource.ReadRequest{State: state}, &resp)
		if !resp.Diagnostics.HasError() || resp.State.Raw.IsNull() {
			t.Fatal("error must retain state")
		}
	}
}
func TestOwnershipWritesAreNotRetried(t *testing.T) {
	for _, method := range []string{"PUT", "DELETE"} {
		attempts := 0
		client := roleClient(t, func(w http.ResponseWriter, req *http.Request) { attempts++; w.WriteHeader(500) }, true)
		o := dapi.RBACOwnership{ObjectType: "SPACE", ObjectID: "space-id", OwnerType: "USER", OwnerID: "reader-id"}
		var err error
		if method == "PUT" {
			_, err = client.PutRBACOwnership(o, "v1")
		} else {
			err = client.DeleteRBACOwnership(o, "v1")
		}
		if err == nil || attempts != 1 {
			t.Fatal("ambiguous ownership write replayed")
		}
	}
}

func TestOwnershipDeleteDoesNotCleanDifferentOwner(t *testing.T) {
	client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			t.Error("must not mutate a different owner's record")
		}
		_ = json.NewEncoder(w).Encode(dapi.RBACOwnership{ObjectType: "SPACE", ObjectID: "space-id", OwnerType: "ROLE", OwnerID: "new-owner-id", Tag: "v2"})
	}, false)
	state := ownershipState(t, false)
	var resp resource.DeleteResponse
	(&ownershipResource{roleResource: roleResource{client: client}}).Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("owner drift must block cleanup")
	}
}

func TestOwnershipImportSetsTargetWithoutInventingOwner(t *testing.T) {
	ctx := context.Background()
	o := dapi.RBACOwnership{ObjectType: "ROLE", ObjectID: "role-id"}
	key, _ := o.Key()
	state := ownershipState(t, false)
	resp := resource.ImportStateResponse{State: state}
	(&ownershipResource{}).ImportState(ctx, resource.ImportStateRequest{ID: key}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var m ownershipModel
	diags := resp.State.Get(ctx, &m)
	if diags.HasError() || m.ObjectType.ValueString() != "ROLE" || m.ObjectID.ValueString() != "role-id" || !m.OwnerID.IsNull() || !m.OwnerType.IsNull() {
		t.Fatal("import must not infer owner")
	}
}
