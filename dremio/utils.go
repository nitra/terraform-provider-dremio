package dremio

import (
	"strings"
)

func interfaceListToStringList(itemsRaw []interface{}) []string {
	items := make([]string, len(itemsRaw))
	for i, raw := range itemsRaw {
		items[i] = raw.(string)
	}
	return items
}

// isNotFoundError reports whether err came back from a Dremio 404 response.
// go-dremio-api-client has no typed error for this, only a formatted string
// ("status: 404, body: ..."), so this is a best-effort string match.
func isNotFoundError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "status: 404")
}
