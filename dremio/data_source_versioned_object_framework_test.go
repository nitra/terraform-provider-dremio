package dremio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	dapi "github.com/saltxwater/go-dremio-api-client"
)

func TestVersionedObjectResolutionFeedsStableRBACResources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/rbac/versioned-objects" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		query := r.URL.Query()
		if query.Get("objectType") != "PDS" || query.Get("referenceType") != "BRANCH" || query.Get("referenceValue") != "main" || !reflect.DeepEqual(query["path"], []string{"s", "a", "sales"}) {
			t.Errorf("unexpected resolution query: %v", query)
		}
		_ = json.NewEncoder(w).Encode(dapi.RBACVersionedObject{ObjectType: "VERSIONED_PDS", ObjectID: "v1.c3RhYmxl"})
	}))
	defer server.Close()
	client, err := dapi.NewClient(server.URL, dapi.Config{ApiKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	object, err := client.ResolveRBACVersionedObject("PDS", []string{"s", "a", "sales"}, "BRANCH", "main")
	if err != nil {
		t.Fatal(err)
	}
	if object.ObjectType != "VERSIONED_PDS" || object.ObjectID != "v1.c3RhYmxl" {
		t.Fatalf("unexpected object: %+v", object)
	}
	if _, err := (dapi.RBACGrant{ObjectType: object.ObjectType, ObjectID: object.ObjectID, GranteeType: "ROLE", GranteeID: "role-id", Privilege: "SELECT"}).Key(); err != nil {
		t.Fatalf("grant must accept server-resolved object: %v", err)
	}
	if _, err := (dapi.RBACOwnership{ObjectType: object.ObjectType, ObjectID: object.ObjectID}).Key(); err != nil {
		t.Fatalf("ownership must accept server-resolved object: %v", err)
	}
}
