package dremio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

func TestMembershipImportKeyIsCanonicalAndTyped(t *testing.T) {
	for _, kind := range []string{"USER", "ROLE"} {
		key, err := dapi.MembershipID("rbac:admin", kind, "stable-member")
		if err != nil {
			t.Fatal(err)
		}
		m, err := dapi.ParseMembershipID(key)
		if err != nil || m.RoleID != "rbac:admin" || m.MemberType != kind || m.MemberID != "stable-member" {
			t.Fatalf("bad import: %v %v", m, err)
		}
	}
	for _, id := range []string{"", "role/USER/name", "W10", "WyJhIiwiTkFNRSIsImIiXQ", "WyJhIiwiVVNFUiIsImIiXQ=="} {
		if _, err := dapi.ParseMembershipID(id); err == nil {
			t.Fatalf("accepted invalid key %q", id)
		}
	}
}

func membershipState(t *testing.T) tfsdk.State {
	t.Helper()
	var response resource.SchemaResponse
	(&membershipResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	state := tfsdk.State{Schema: response.Schema}
	key, _ := dapi.MembershipID("role-id", "USER", "user-id")
	m := membershipModel{ID: types.StringValue(key), RoleID: types.StringValue("role-id"), MemberType: types.StringValue("USER"), MemberID: types.StringValue("user-id"), Tag: types.StringValue("v1")}
	if diags := state.Set(context.Background(), &m); diags.HasError() {
		t.Fatal(diags)
	}
	return state
}

func TestMembershipReadPreservesStateOnDeniedOrWrongIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"forbidden", `{"message":"status: 404"}`, 403},
		{"conflict", `{}`, 409}, {"disabled", `{}`, 503}, {"malformed", `{}`, 200},
		{"wrong UID", `{"roleId":"role-id","memberType":"USER","memberId":"other-user","tag":"v1"}`, 200},
		{"wrong type", `{"roleId":"role-id","memberType":"ROLE","memberId":"user-id","tag":"v1"}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}, false)
			state := membershipState(t)
			response := resource.ReadResponse{State: state}
			r := &membershipResource{roleResource: roleResource{client: client}}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
			if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
				t.Fatal("must fail without forgetting the assignment")
			}
		})
	}
}

func TestMembershipMutationIsExactAndNotRetried(t *testing.T) {
	attempts := 0
	client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
		attempts++
		if req.Method != "PUT" || req.URL.Path != "/api/v3/rbac/memberships/role-id/USER/user-id" {
			t.Errorf("unexpected target %s %s", req.Method, req.URL)
		}
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["tag"] != "v1" || len(body) != 1 {
			t.Errorf("invalid body: %v %v", body, err)
		}
		w.WriteHeader(500)
	}, true)
	if _, err := client.PutRBACMembership("role-id", "USER", "user-id", "v1"); err == nil || attempts != 1 {
		t.Fatal("ambiguous assignment was replayed")
	}
}

func TestMembershipCreateRequiresImportAndDoesNotWriteExistingAssignment(t *testing.T) {
	client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			t.Error("existing assignment was overwritten")
		}
		_, _ = w.Write([]byte(`{"roleId":"role-id","memberType":"USER","memberId":"user-id","tag":"v1"}`))
	}, false)
	state := membershipState(t)
	response := resource.CreateResponse{State: state}
	(&membershipResource{roleResource: roleResource{client: client}}).Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("existing assignment must require import")
	}
}

func TestParallelRBACRoleCreatesSerializeSharedPolicyVersion(t *testing.T) {
	var mu sync.Mutex
	roles := map[string]string{"rbac:admin": "ADMIN", "rbac:public": "PUBLIC"}
	version := 0
	client := roleClient(t, func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if req.Method == "GET" {
			_ = json.NewEncoder(w).Encode(map[string]any{"tag": fmt.Sprint(version), "roles": roles})
			return
		}
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if body["tag"] != fmt.Sprint(version) {
			w.WriteHeader(409)
			return
		}
		version++
		id := fmt.Sprintf("role-%d", version)
		roles[id] = body["name"]
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "name": body["name"], "tag": fmt.Sprint(version)})
	}, false)
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			state := roleState(t, roleResourceModel{ID: types.StringUnknown(), Name: types.StringValue(fmt.Sprintf("Role%d", i)), Tag: types.StringUnknown()})
			response := resource.CreateResponse{State: state}
			(&roleResource{client: client}).Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}}, &response)
			if response.Diagnostics.HasError() {
				t.Errorf("parallel create: %v", response.Diagnostics)
			}
		}(i)
	}
	group.Wait()
	if version != 8 {
		t.Fatalf("expected 8 serialized writes, got %d", version)
	}
}
