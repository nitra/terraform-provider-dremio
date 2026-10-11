package dremio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
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

func TestUserCannotCreateOrDeleteAccounts(t *testing.T) {
	r := &userResource{}
	var create resource.CreateResponse
	r.Create(context.Background(), resource.CreateRequest{}, &create)
	if !create.Diagnostics.HasError() {
		t.Fatal("account creation must require import")
	}
	var remove resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{}, &remove)
	if !remove.Diagnostics.HasError() {
		t.Fatal("account deletion must be blocked")
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
