package dremio

import (
	"strings"
)

// isNotFoundError reports whether err came back from a Dremio 404 response.
// go-dremio-api-client has no typed error for this, only a formatted string
// ("status: 404, body: ..."), so this is a best-effort string match.
func isNotFoundError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "status: 404")
}

// getQueryPath renders a catalog path as a dot-separated, double-quoted SQL
// identifier path (e.g. ["a", "b c"] -> `"a"."b c"`), used for the
// query_path attribute on every dataset resource.
func getQueryPath(path []string) string {
	qp := make([]string, len(path))
	for i, p := range path {
		qp[i] = "\"" + p + "\""
	}
	return strings.Join(qp, ".")
}
