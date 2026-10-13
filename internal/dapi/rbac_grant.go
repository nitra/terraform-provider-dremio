package dapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
)

type RBACGrant struct {
	ObjectType  string `json:"objectType"`
	ObjectID    string `json:"objectId"`
	GranteeType string `json:"granteeType"`
	GranteeID   string `json:"granteeId"`
	Privilege   string `json:"privilege"`
	Tag         string `json:"tag"`
}

func (g RBACGrant) Key() (string, error) {
	if g.ObjectID == "" || g.GranteeID == "" || g.Privilege == "" || g.Privilege == "ALL" || (g.GranteeType != "USER" && g.GranteeType != "ROLE") {
		return "", fmt.Errorf("stable object/principal IDs and one concrete privilege required")
	}
	switch g.ObjectType {
	case "PROJECT":
		if g.ObjectID != "SYSTEM" {
			return "", fmt.Errorf("PROJECT requires SYSTEM object ID")
		}
	case "SOURCE", "SPACE", "FOLDER", "PDS", "VDS", "FUNCTION":
	default:
		return "", fmt.Errorf("unsupported object type")
	}
	value, err := json.Marshal([]string{g.ObjectType, g.ObjectID, g.GranteeType, g.GranteeID, g.Privilege})
	return base64.RawURLEncoding.EncodeToString(value), err
}

func ParseGrantID(id string) (*RBACGrant, error) {
	value, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return nil, fmt.Errorf("invalid grant ID")
	}
	var parts []string
	if err := json.Unmarshal(value, &parts); err != nil || len(parts) != 5 {
		return nil, fmt.Errorf("invalid grant ID")
	}
	g := &RBACGrant{ObjectType: parts[0], ObjectID: parts[1], GranteeType: parts[2], GranteeID: parts[3], Privilege: parts[4]}
	canonical, err := g.Key()
	if err != nil || canonical != id {
		return nil, fmt.Errorf("invalid or non-canonical grant ID")
	}
	return g, nil
}

func (g RBACGrant) path() (string, error) {
	if _, err := g.Key(); err != nil {
		return "", err
	}
	return "/api/v3/rbac/grants/" + url.PathEscape(g.ObjectType) + "/" + url.PathEscape(g.ObjectID) + "/" + g.GranteeType + "/" + url.PathEscape(g.GranteeID) + "/" + url.PathEscape(g.Privilege), nil
}

func (c *Client) grantRequest(method string, g RBACGrant, tag string) (*RBACGrant, error) {
	path, err := g.path()
	if err != nil {
		return nil, err
	}
	var body *bytes.Buffer
	if method != "GET" {
		if tag == "" {
			return nil, fmt.Errorf("policy tag required")
		}
		value, _ := json.Marshal(map[string]string{"tag": tag})
		body = bytes.NewBuffer(value)
	}
	var result RBACGrant
	// Avoid a typed nil reader; the existing request logger checks nil bodies.
	if body == nil {
		err = c.request(method, path, nil, &result)
	} else {
		err = c.request(method, path, body, &result)
	}
	if err != nil {
		return nil, err
	}
	expected, _ := g.Key()
	actual, err := result.Key()
	if err != nil || actual != expected || result.Tag == "" {
		return nil, fmt.Errorf("invalid OSS RBAC grant identity or tag")
	}
	return &result, nil
}

func (c *Client) GetRBACGrant(g RBACGrant) (*RBACGrant, error) { return c.grantRequest("GET", g, "") }
func (c *Client) PutRBACGrant(g RBACGrant, tag string) (*RBACGrant, error) {
	return c.grantRequest("PUT", g, tag)
}
func (c *Client) DeleteRBACGrant(g RBACGrant, tag string) error {
	path, err := g.path()
	if err != nil {
		return err
	}
	if tag == "" {
		return fmt.Errorf("policy tag required")
	}
	return c.request("DELETE", path+"?tag="+url.QueryEscape(tag), nil, nil)
}
