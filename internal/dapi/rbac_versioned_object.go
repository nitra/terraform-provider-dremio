package dapi

import (
	"fmt"
	"net/url"
	"strings"
)

type RBACVersionedObject struct {
	ObjectType string `json:"objectType"`
	ObjectID   string `json:"objectId"`
}

// ResolveRBACVersionedObject obtains the current stable grant target from Dremio itself.
func (c *Client) ResolveRBACVersionedObject(objectType string, path []string, referenceType, referenceValue string) (*RBACVersionedObject, error) {
	if (objectType != "PDS" && objectType != "VDS") || len(path) < 2 || referenceValue == "" {
		return nil, fmt.Errorf("versioned PDS/VDS path and reference required")
	}
	switch referenceType {
	case "BRANCH", "TAG", "COMMIT", "REFERENCE":
	default:
		return nil, fmt.Errorf("unsupported reference type")
	}
	for _, part := range path {
		if part == "" {
			return nil, fmt.Errorf("non-empty path components required")
		}
	}
	query := url.Values{}
	query.Set("objectType", objectType)
	for _, part := range path {
		query.Add("path", part)
	}
	query.Set("referenceType", referenceType)
	query.Set("referenceValue", referenceValue)
	var result RBACVersionedObject
	if err := c.request("GET", "/api/v3/rbac/versioned-objects?"+query.Encode(), nil, &result); err != nil {
		return nil, err
	}
	if result.ObjectType != "VERSIONED_"+objectType || !strings.HasPrefix(result.ObjectID, "v1.") {
		return nil, fmt.Errorf("invalid versioned object identity response")
	}
	return &result, nil
}
