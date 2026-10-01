package dapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
)

// GetSourceUI fetches the v2 "SourceUI" representation of a source
// (GET /apiv2/source/<name>), which is the shape the isMetadataImpacting
// check expects. Deliberately untyped: this is only ever round-tripped back
// to Dremio (with its "config" field swapped out), never read field-by-field
// on our side.
func (c *Client) GetSourceUI(name string) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	path := fmt.Sprintf("/apiv2/source/%s", url.PathEscape(name))
	err := c.request("GET", path, nil, &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// IsSourceConfigMetadataImpacting asks Dremio itself (POST
// /apiv2/sources/isMetadataImpacting) whether sourceUI's config differs from
// the currently stored config in a way that would make Dremio delete and
// rediscover every dataset under the source - which silently drops any
// reflections, formats and permissions attached to those datasets. This is
// the same check the UI's "Warning" dialog is driven by; calling it directly
// means the provider doesn't have to maintain its own per-source-type table
// of which fields are safe, which would drift across Dremio versions.
func (c *Client) IsSourceConfigMetadataImpacting(sourceUI map[string]interface{}) (bool, error) {
	body, err := json.Marshal(sourceUI)
	if err != nil {
		return false, err
	}
	result := struct {
		IsMetadataImpacting bool `json:"isMetadataImpacting"`
	}{}
	err = c.request("POST", "/apiv2/sources/isMetadataImpacting", bytes.NewBuffer(body), &result)
	if err != nil {
		return false, err
	}
	return result.IsMetadataImpacting, nil
}
