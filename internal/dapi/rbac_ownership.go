package dapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
)

type RBACOwnership struct {
	ObjectType string `json:"objectType"`
	ObjectID   string `json:"objectId"`
	OwnerType  string `json:"ownerType"`
	OwnerID    string `json:"ownerId"`
	Tag        string `json:"tag"`
}

// The key identifies the target, not its mutable owner.
func (o RBACOwnership) Key() (string, error) {
	if o.ObjectID == "" {
		return "", fmt.Errorf("stable target ID required")
	}
	switch o.ObjectType {
	case "ROLE", "SOURCE", "SPACE", "FOLDER", "PDS", "VDS", "FUNCTION":
	default:
		return "", fmt.Errorf("unsupported ownership target type")
	}
	value, err := json.Marshal([]string{o.ObjectType, o.ObjectID})
	return base64.RawURLEncoding.EncodeToString(value), err
}
func ParseOwnershipID(id string) (*RBACOwnership, error) {
	value, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return nil, fmt.Errorf("invalid ownership ID")
	}
	var parts []string
	if err := json.Unmarshal(value, &parts); err != nil || len(parts) != 2 {
		return nil, fmt.Errorf("invalid ownership ID")
	}
	o := &RBACOwnership{ObjectType: parts[0], ObjectID: parts[1]}
	key, err := o.Key()
	if err != nil || key != id {
		return nil, fmt.Errorf("invalid or non-canonical ownership ID")
	}
	return o, nil
}
func (o RBACOwnership) path() (string, error) {
	if _, err := o.Key(); err != nil {
		return "", err
	}
	return "/api/v3/rbac/ownership/" + o.ObjectType + "/" + url.PathEscape(o.ObjectID), nil
}
func (o RBACOwnership) validateOwner() error {
	if o.OwnerID == "" || (o.OwnerType != "USER" && o.OwnerType != "ROLE") {
		return fmt.Errorf("stable USER or ROLE owner required")
	}
	return nil
}
func (c *Client) GetRBACOwnership(o RBACOwnership) (*RBACOwnership, error) {
	return c.ownershipRequest("GET", o, "")
}
func (c *Client) PutRBACOwnership(o RBACOwnership, tag string) (*RBACOwnership, error) {
	return c.ownershipRequest("PUT", o, tag)
}
func (c *Client) ownershipRequest(method string, o RBACOwnership, tag string) (*RBACOwnership, error) {
	path, err := o.path()
	if err != nil {
		return nil, err
	}
	var result RBACOwnership
	if method == "GET" {
		err = c.request(method, path, nil, &result)
	} else {
		if err := o.validateOwner(); err != nil {
			return nil, err
		}
		if tag == "" {
			return nil, fmt.Errorf("policy tag required")
		}
		body, _ := json.Marshal(map[string]string{"ownerType": o.OwnerType, "ownerId": o.OwnerID, "tag": tag})
		err = c.request(method, path, bytes.NewBuffer(body), &result)
	}
	if err != nil {
		return nil, err
	}
	expected, _ := o.Key()
	actual, err := result.Key()
	if err != nil || actual != expected || result.Tag == "" || result.validateOwner() != nil {
		return nil, fmt.Errorf("invalid OSS RBAC ownership identity or tag")
	}
	if method == "PUT" && (o.OwnerType != result.OwnerType || o.OwnerID != result.OwnerID) {
		return nil, fmt.Errorf("ownership transfer response differs from requested owner")
	}
	return &result, nil
}
func (c *Client) DeleteRBACOwnership(o RBACOwnership, tag string) error {
	path, err := o.path()
	if err != nil {
		return err
	}
	if err := o.validateOwner(); err != nil {
		return err
	}
	if tag == "" {
		return fmt.Errorf("policy tag required")
	}
	query := url.Values{"tag": {tag}, "ownerType": {o.OwnerType}, "ownerId": {o.OwnerID}}
	return c.request("DELETE", path+"?"+query.Encode(), nil, nil)
}
