package dremio

import (
	"net/http"
	"strings"
	"time"
)

// retryTransport retries transient failures (network errors, 429, and 5xx
// responses) with capped exponential backoff, so a momentary blip during
// tofu apply doesn't fail the whole run.
//
// Request bodies are re-read via req.GetBody, which net/http populates
// automatically for the *bytes.Buffer bodies go-dremio-api-client sends.
type retryTransport struct {
	base       http.RoundTripper
	maxRetries int
	baseDelay  time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	for attempt := 0; ; attempt++ {
		if attempt > 0 && req.GetBody != nil {
			body, bodyErr := req.GetBody()
			if bodyErr != nil {
				return nil, bodyErr
			}
			req.Body = body
		}

		resp, err = t.base.RoundTrip(req)

		// OSS RBAC writes use global OCC and server-generated IDs. An ambiguous
		// response must be reconciled by read/import, never replayed automatically.
		unsafeRBACWrite := strings.Contains(req.URL.Path, "/api/v3/rbac/") && req.Method != http.MethodGet
		retryable := !unsafeRBACWrite && (err != nil || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500)
		if !retryable || attempt >= t.maxRetries {
			return resp, err
		}

		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(t.baseDelay << attempt)
	}
}
