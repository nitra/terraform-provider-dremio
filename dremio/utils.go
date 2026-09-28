package dremio

import (
	"strings"

	dapi "github.com/saltxwater/go-dremio-api-client"
)

func interfaceListToStringList(itemsRaw []interface{}) []string {
	items := make([]string, len(itemsRaw))
	for i, raw := range itemsRaw {
		items[i] = raw.(string)
	}
	return items
}

// reflectionFieldListToStringList and interfaceListToReflectionFieldList are
// used by the still-SDKv2 dremio_aggr_reflection resource (dremio_raw_reflection
// moved to Framework and manages this conversion itself).
func reflectionFieldListToStringList(itemsRaw []dapi.ReflectionField) []string {
	items := make([]string, len(itemsRaw))
	for i, raw := range itemsRaw {
		items[i] = raw.Name
	}
	return items
}

func interfaceListToReflectionFieldList(itemsRaw []interface{}) []dapi.ReflectionField {
	items := make([]dapi.ReflectionField, len(itemsRaw))
	for i, raw := range itemsRaw {
		items[i] = dapi.ReflectionField{
			Name: raw.(string),
		}
	}
	return items
}

// isNotFoundError reports whether err came back from a Dremio 404 response.
// go-dremio-api-client has no typed error for this, only a formatted string
// ("status: 404, body: ..."), so this is a best-effort string match.
func isNotFoundError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "status: 404")
}
