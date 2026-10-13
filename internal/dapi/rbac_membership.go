package dapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
)

type RBACMembership struct {
	RoleID     string `json:"roleId"`
	MemberType string `json:"memberType"`
	MemberID   string `json:"memberId"`
	Tag        string `json:"tag"`
}

// MembershipID is a canonical reversible key, not a generated policy entity ID.
func MembershipID(roleID, memberType, memberID string) (string, error) {
	if roleID == "" || memberID == "" || (memberType != "USER" && memberType != "ROLE") {
		return "", fmt.Errorf("role ID, USER/ROLE type and stable member ID required")
	}
	value, err := json.Marshal([]string{roleID, memberType, memberID})
	return base64.RawURLEncoding.EncodeToString(value), err
}

func ParseMembershipID(id string) (*RBACMembership, error) {
	value, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return nil, fmt.Errorf("invalid membership ID")
	}
	var parts []string
	if err := json.Unmarshal(value, &parts); err != nil || len(parts) != 3 {
		return nil, fmt.Errorf("invalid membership ID")
	}
	canonical, err := MembershipID(parts[0], parts[1], parts[2])
	if err != nil || canonical != id {
		return nil, fmt.Errorf("invalid or non-canonical membership ID")
	}
	return &RBACMembership{RoleID: parts[0], MemberType: parts[1], MemberID: parts[2]}, nil
}

func membershipPath(roleID, memberType, memberID string) (string, error) {
	if _, err := MembershipID(roleID, memberType, memberID); err != nil {
		return "", err
	}
	return "/api/v3/rbac/memberships/" + url.PathEscape(roleID) + "/" + memberType + "/" + url.PathEscape(memberID), nil
}

func validMembership(m *RBACMembership, roleID, memberType, memberID string) error {
	if m.RoleID != roleID || m.MemberType != memberType || m.MemberID != memberID || m.Tag == "" {
		return fmt.Errorf("invalid OSS RBAC membership identity or policy tag")
	}
	return nil
}

func (c *Client) GetRBACMembership(roleID, memberType, memberID string) (*RBACMembership, error) {
	path, err := membershipPath(roleID, memberType, memberID)
	if err != nil {
		return nil, err
	}
	var m RBACMembership
	if err := c.request("GET", path, nil, &m); err != nil {
		return nil, err
	}
	return &m, validMembership(&m, roleID, memberType, memberID)
}

func (c *Client) PutRBACMembership(roleID, memberType, memberID, tag string) (*RBACMembership, error) {
	path, err := membershipPath(roleID, memberType, memberID)
	if err != nil {
		return nil, err
	}
	if tag == "" {
		return nil, fmt.Errorf("policy tag required")
	}
	body, err := json.Marshal(map[string]string{"tag": tag})
	if err != nil {
		return nil, err
	}
	var m RBACMembership
	if err := c.request("PUT", path, bytes.NewBuffer(body), &m); err != nil {
		return nil, err
	}
	return &m, validMembership(&m, roleID, memberType, memberID)
}

func (c *Client) DeleteRBACMembership(roleID, memberType, memberID, tag string) error {
	path, err := membershipPath(roleID, memberType, memberID)
	if err != nil {
		return err
	}
	if tag == "" {
		return fmt.Errorf("policy tag required")
	}
	return c.request("DELETE", path+"?tag="+url.QueryEscape(tag), nil, nil)
}
