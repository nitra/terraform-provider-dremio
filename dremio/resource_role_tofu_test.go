package dremio

import (
	"context"
	"encoding/json"
	"fmt"
	dapi "github.com/saltxwater/go-dremio-api-client"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in locally; the dedicated CI step requires this test with OpenTofu installed.
// Only an in-process HTTP fixture and a temporary local state are used.
func TestRoleOpenTofuLifecycle(t *testing.T) {
	runRBACOpenTofuLifecycle(t, false, false, false)
}

func TestMembershipOpenTofuLifecycle(t *testing.T) {
	runRBACOpenTofuLifecycle(t, true, false, false)
}

func TestGrantOpenTofuLifecycle(t *testing.T) {
	runRBACOpenTofuLifecycle(t, true, true, false)
}

func TestOwnershipOpenTofuLifecycle(t *testing.T) {
	runRBACOpenTofuLifecycle(t, false, false, true)
}

func runRBACOpenTofuLifecycle(t *testing.T, withMembership, withGrant, withOwnership bool) {
	tofu := os.Getenv("TOFU_RBAC_BINARY")
	if tofu == "" {
		t.Skip("set TOFU_RBAC_BINARY to enable the isolated OpenTofu acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var mu sync.Mutex
	exists := false
	memberExists := false
	grantExists := false
	ownerExists := false
	targetLive := true
	ownerType, ownerID := "ROLE", "fixture-role"
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if req.Header.Get("Authorization") != "_dremiofixture-token" {
			w.WriteHeader(403)
			return
		}
		switch {
		case req.URL.Path == "/api/v3/rbac/ownership/SPACE/fixture-space":
			switch req.Method {
			case "GET":
				if !ownerExists {
					w.WriteHeader(404)
					return
				}
			case "PUT":
				var body map[string]string
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["tag"] != fmt.Sprint(writes) || !targetLive {
					w.WriteHeader(409)
					return
				}
				ownerType, ownerID = body["ownerType"], body["ownerId"]
				if (ownerType != "ROLE" && ownerType != "USER") || ownerID == "" {
					w.WriteHeader(400)
					return
				}
				ownerExists = true
				writes++
			case "DELETE":
				if req.URL.Query().Get("tag") != fmt.Sprint(writes) || req.URL.Query().Get("ownerType") != ownerType || req.URL.Query().Get("ownerId") != ownerID {
					w.WriteHeader(409)
					return
				}
				if targetLive {
					http.Error(w, "Live object ownership cannot be destroyed", 400)
					return
				}
				ownerExists = false
				writes++
				w.WriteHeader(204)
				return
			default:
				w.WriteHeader(405)
				return
			}
			_ = json.NewEncoder(w).Encode(dapi.RBACOwnership{ObjectType: "SPACE", ObjectID: "fixture-space", OwnerType: ownerType, OwnerID: ownerID, Tag: fmt.Sprint(writes)})
		case req.Method == "GET" && req.URL.Path == "/api/v3/rbac/roles":
			roles := map[string]string{"rbac:admin": "ADMIN", "rbac:public": "PUBLIC"}
			if exists {
				roles["fixture-role"] = "Analyst"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag": fmt.Sprint(writes), "roles": roles})
		case req.Method == "POST" && req.URL.Path == "/api/v3/rbac/roles":
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["name"] != "Analyst" || body["tag"] != fmt.Sprint(writes) || exists {
				w.WriteHeader(409)
				return
			}
			exists = true
			writes++
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "fixture-role", "name": "Analyst", "tag": fmt.Sprint(writes)})
		case req.Method == "GET" && req.URL.Path == "/api/v3/rbac/roles/fixture-role":
			if !exists {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "fixture-role", "name": "Analyst", "tag": fmt.Sprint(writes)})
		case req.Method == "DELETE" && req.URL.Path == "/api/v3/rbac/roles/fixture-role":
			if !exists || memberExists || grantExists || req.URL.Query().Get("tag") != fmt.Sprint(writes) {
				w.WriteHeader(409)
				return
			}
			exists = false
			writes++
			w.WriteHeader(204)
		case req.URL.Path == "/api/v3/rbac/memberships/fixture-role/USER/fixture-user":
			switch req.Method {
			case "GET":
				if !memberExists {
					w.WriteHeader(404)
					return
				}
			case "PUT":
				var body map[string]string
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["tag"] != fmt.Sprint(writes) || !exists {
					w.WriteHeader(409)
					return
				}
				memberExists = true
				writes++
			case "DELETE":
				if req.URL.Query().Get("tag") != fmt.Sprint(writes) {
					w.WriteHeader(409)
					return
				}
				memberExists = false
				writes++
				w.WriteHeader(204)
				return
			default:
				t.Errorf("unexpected membership method: %s", req.Method)
				w.WriteHeader(500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"roleId": "fixture-role", "memberType": "USER", "memberId": "fixture-user", "tag": fmt.Sprint(writes)})
		case req.URL.Path == "/api/v3/rbac/grant-authority/PROJECT/SYSTEM":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag": fmt.Sprint(writes)})
		case req.URL.Path == "/api/v3/rbac/grants/PROJECT/SYSTEM/ROLE/fixture-role/CREATE_ROLE":
			switch req.Method {
			case "GET":
				if !grantExists {
					w.WriteHeader(404)
					return
				}
			case "PUT":
				var body map[string]string
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["tag"] != fmt.Sprint(writes) || !exists {
					w.WriteHeader(409)
					return
				}
				grantExists = true
				writes++
			case "DELETE":
				if req.URL.Query().Get("tag") != fmt.Sprint(writes) {
					w.WriteHeader(409)
					return
				}
				grantExists = false
				writes++
				w.WriteHeader(204)
				return
			default:
				t.Errorf("unexpected grant method: %s", req.Method)
				w.WriteHeader(500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"objectType": "PROJECT", "objectId": "SYSTEM", "granteeType": "ROLE", "granteeId": "fixture-role", "privilege": "CREATE_ROLE", "tag": fmt.Sprint(writes)})
		default:
			t.Errorf("unexpected fixture request %s %s", req.Method, req.URL)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	workspace := t.TempDir()
	plugins := filepath.Join(workspace, "plugins")
	if err := os.Mkdir(plugins, 0700); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(plugins, "terraform-provider-dremio"), "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	cliConfig := filepath.Join(workspace, "tofurc")
	config := fmt.Sprintf("provider_installation {\n dev_overrides {\n \"registry.opentofu.org/nitra/dremio\" = %q\n }\n direct {}\n}\n", plugins)
	if err := os.WriteFile(cliConfig, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	hcl := fmt.Sprintf(`terraform {
  required_providers {
    dremio = { source = "registry.opentofu.org/nitra/dremio" }
  }
}
provider "dremio" {
  dremio_url = %q
  api_key = "fixture-token"
}
resource "dremio_role" "analyst" {
  name = "Analyst"
}
`, server.URL)
	if withMembership {
		hcl += `
resource "dremio_role_membership" "reader" {
  role_id = dremio_role.analyst.id
  member_type = "USER"
  member_id = "fixture-user"
}
`
	}
	if withGrant {
		hcl += `
resource "dremio_grant" "role_creator" {
  object_type = "PROJECT"
  object_id = "SYSTEM"
  grantee_type = "ROLE"
  grantee_id = dremio_role.analyst.id
  privilege = "CREATE_ROLE"
}
`
	}
	if withOwnership {
		hcl += `
resource "dremio_ownership" "space" {
 object_type = "SPACE"
 object_id = "fixture-space"
 owner_type = "ROLE"
 owner_id = dremio_role.analyst.id
}
`
	}
	if err := os.WriteFile(filepath.Join(workspace, "main.tf"), []byte(hcl), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, tofu, args...)
		cmd.Dir = workspace
		cmd.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+cliConfig, "TF_IN_AUTOMATION=1", "TF_INPUT=0")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tofu %v: %v\n%s", args, err, output)
		}
	}
	run("apply", "-auto-approve", "-input=false", "-no-color")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	run("state", "rm", "dremio_role.analyst")
	run("import", "-input=false", "-no-color", "dremio_role.analyst", "fixture-role")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	if withMembership {
		key, err := dapi.MembershipID("fixture-role", "USER", "fixture-user")
		if err != nil {
			t.Fatal(err)
		}
		run("state", "rm", "dremio_role_membership.reader")
		run("import", "-input=false", "-no-color", "dremio_role_membership.reader", key)
		run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	}
	if withGrant {
		g := dapi.RBACGrant{ObjectType: "PROJECT", ObjectID: "SYSTEM", GranteeType: "ROLE", GranteeID: "fixture-role", Privilege: "CREATE_ROLE"}
		key, err := g.Key()
		if err != nil {
			t.Fatal(err)
		}
		run("state", "rm", "dremio_grant.role_creator")
		run("import", "-input=false", "-no-color", "dremio_grant.role_creator", key)
		run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	}
	if withOwnership {
		key, _ := (dapi.RBACOwnership{ObjectType: "SPACE", ObjectID: "fixture-space"}).Key()
		run("state", "rm", "dremio_ownership.space")
		run("import", "-input=false", "-no-color", "dremio_ownership.space", key)
		run("plan", "-detailed-exitcode", "-input=false", "-no-color")
		// Transfer in place without changing the canonical target key.
		hcl = strings.Replace(hcl, "owner_type = \"ROLE\"", "owner_type = \"USER\"", 1)
		hcl = strings.Replace(hcl, "owner_id = dremio_role.analyst.id", "owner_id = \"fixture-user\"", 1)
		if err := os.WriteFile(filepath.Join(workspace, "main.tf"), []byte(hcl), 0600); err != nil {
			t.Fatal(err)
		}
		run("apply", "-auto-approve", "-input=false", "-no-color")
		run("plan", "-detailed-exitcode", "-input=false", "-no-color")
		cmd := exec.CommandContext(ctx, tofu, "destroy", "-auto-approve", "-input=false", "-no-color")
		cmd.Dir = workspace
		cmd.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+cliConfig, "TF_IN_AUTOMATION=1", "TF_INPUT=0")
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "Unable to clean ownership") {
			t.Fatalf("live ownership destroy should fail: %v\n%s", err, output)
		}
		mu.Lock()
		if !ownerExists || ownerType != "USER" || ownerID != "fixture-user" {
			t.Fatal("failed destroy lost ownership")
		}
		targetLive = false
		mu.Unlock()
	}
	run("destroy", "-auto-approve", "-input=false", "-no-color")
	mu.Lock()
	defer mu.Unlock()
	wantWrites := 2
	if withMembership {
		wantWrites = 4
	}
	if withGrant {
		wantWrites += 2
	}
	if withOwnership {
		wantWrites += 3
	}
	if exists || memberExists || grantExists || ownerExists || writes != wantWrites {
		t.Fatalf("expected exactly create/delete, got exists=%v writes=%d", exists, writes)
	}
}
