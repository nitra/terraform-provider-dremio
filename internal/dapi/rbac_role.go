package dapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
)

// RBACRole is the OSS-specific role contract; never use username as identity.
type RBACRole struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Tag  string `json:"tag"`
}

type RBACRoles struct {
	Tag   string            `json:"tag"`
	Roles map[string]string `json:"roles"`
}

// Serialize a provider process's read-modify-write operations on the shared
// policy snapshot. Other processes/server writers remain guarded by KV OCC.
func (c *Client) BeginRBACMutation() func() {
	c.rbacMutation.Lock()
	return c.rbacMutation.Unlock
}

func (c *Client) GetRBACRoles() (*RBACRoles, error) {
	var roles RBACRoles
	if err := c.request("GET", "/api/v3/rbac/roles", nil, &roles); err != nil {
		return nil, err
	}
	if roles.Tag == "" || roles.Roles["rbac:admin"] != "ADMIN" || roles.Roles["rbac:public"] != "PUBLIC" {
		return nil, fmt.Errorf("invalid OSS RBAC role collection")
	}
	return &roles, nil
}

func (c *Client) GetRBACRole(id string) (*RBACRole, error) {
	if id == "" {
		return nil, fmt.Errorf("role ID required")
	}
	var role RBACRole
	if err := c.request("GET", "/api/v3/rbac/roles/"+url.PathEscape(id), nil, &role); err != nil {
		return nil, err
	}
	if role.ID != id || role.Name == "" || role.Tag == "" {
		return nil, fmt.Errorf("invalid OSS RBAC role response")
	}
	return &role, nil
}

func (c *Client) CreateRBACRole(name, tag string) (*RBACRole, error) {
	if name == "" || tag == "" || name == "ADMIN" || name == "PUBLIC" {
		return nil, fmt.Errorf("non-system role name and policy tag required")
	}
	body, err := json.Marshal(map[string]string{"name": name, "tag": tag})
	if err != nil {
		return nil, err
	}
	var role RBACRole
	if err := c.request("POST", "/api/v3/rbac/roles", bytes.NewBuffer(body), &role); err != nil {
		return nil, err
	}
	if role.ID == "" || role.ID == "rbac:admin" || role.ID == "rbac:public" || role.Name != name || role.Tag == "" {
		return nil, fmt.Errorf("invalid OSS RBAC created role response; refresh/import before retry")
	}
	return &role, nil
}

func (c *Client) DeleteRBACRole(id, tag string) error {
	if id == "" || tag == "" || id == "rbac:admin" || id == "rbac:public" {
		return fmt.Errorf("non-system role ID and policy tag required")
	}
	return c.request("DELETE", "/api/v3/rbac/roles/"+url.PathEscape(id)+"?tag="+url.QueryEscape(tag), nil, nil)
}
