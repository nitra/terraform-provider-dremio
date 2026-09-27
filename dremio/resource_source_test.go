package dremio

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestGetSourceConfig_GCS(t *testing.T) {
	raw := map[string]interface{}{
		"type": "GCS",
		"name": "bucket",
		"config": []interface{}{
			map[string]interface{}{
				"project_id":       "abie-ua",
				"auth_mode":        "AUTO",
				"root_path":        "/",
				"bucket_whitelist": []interface{}{"tempo-7d"},
				"async_enabled":    true,
				"caching_enable":   true,
				"cache_percent":    70,
			},
		},
	}
	d := schema.TestResourceDataRaw(t, resourceSource().Schema, raw)

	got, err := getSourceConfig(d)
	if err != nil {
		t.Fatalf("getSourceConfig returned error: %v", err)
	}

	config, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", got)
	}

	want := map[string]interface{}{
		"projectId":       "abie-ua",
		"authMode":        "AUTO",
		"rootPath":        "/",
		"bucketWhitelist": []string{"tempo-7d"},
		"asyncEnabled":    true,
		"cachingEnable":   true,
		"cachePercent":    70,
		"clientEmail":     "",
		"clientId":        "",
		"privateKeyId":    "",
		"privateKey":      "",
	}

	for k, wantV := range want {
		gotV, ok := config[k]
		if !ok {
			t.Errorf("missing key %q in config", k)
			continue
		}
		if !reflect.DeepEqual(gotV, wantV) {
			t.Errorf("config[%q] = %#v, want %#v", k, gotV, wantV)
		}
	}
}

func TestReadSourceConfig_GCS_roundtrip(t *testing.T) {
	d := schema.TestResourceDataRaw(t, resourceSource().Schema, map[string]interface{}{
		"type": "GCS",
		"name": "bucket",
	})

	// Shape of what the Dremio API actually returns: JSON numbers decode as
	// float64, and a source with no bucket whitelist configured omits the
	// key entirely (encoded as Go nil, not an empty list).
	apiConfig := map[string]interface{}{
		"projectId":       "abie-ua",
		"authMode":        "AUTO",
		"rootPath":        "/",
		"bucketWhitelist": []interface{}{"tempo-7d"},
		"asyncEnabled":    true,
		"cachingEnable":   true,
		"cachePercent":    float64(70),
		"clientEmail":     "",
		"clientId":        "",
		"privateKeyId":    "",
	}

	if err := readSourceConfig(d, "GCS", apiConfig); err != nil {
		t.Fatalf("readSourceConfig returned error: %v", err)
	}

	if got := d.Get("config.0.project_id").(string); got != "abie-ua" {
		t.Errorf("project_id = %q, want %q", got, "abie-ua")
	}
	if got := d.Get("config.0.cache_percent").(int); got != 70 {
		t.Errorf("cache_percent = %d, want 70", got)
	}
	whitelist := d.Get("config.0.bucket_whitelist").([]interface{})
	if len(whitelist) != 1 || whitelist[0] != "tempo-7d" {
		t.Errorf("bucket_whitelist = %v, want [tempo-7d]", whitelist)
	}
}

func TestReadSourceConfig_GCS_nilBucketWhitelist(t *testing.T) {
	d := schema.TestResourceDataRaw(t, resourceSource().Schema, map[string]interface{}{
		"type": "GCS",
		"name": "bucket",
	})

	apiConfig := map[string]interface{}{
		"projectId":       "abie-ua",
		"authMode":        "AUTO",
		"rootPath":        "/",
		"bucketWhitelist": nil,
		"asyncEnabled":    true,
		"cachingEnable":   true,
		"cachePercent":    float64(70),
		"clientEmail":     "",
		"clientId":        "",
		"privateKeyId":    "",
	}

	if err := readSourceConfig(d, "GCS", apiConfig); err != nil {
		t.Fatalf("readSourceConfig returned error: %v", err)
	}

	if got := len(d.Get("config.0.bucket_whitelist").([]interface{})); got != 0 {
		t.Errorf("bucket_whitelist length = %d, want 0", got)
	}
}

func TestGetSourceConfig_UnsupportedType(t *testing.T) {
	d := schema.TestResourceDataRaw(t, resourceSource().Schema, map[string]interface{}{
		"type": "POSTGRES",
		"name": "whatever",
	})

	_, err := getSourceConfig(d)
	if err == nil {
		t.Fatal("expected an error for an unsupported source type, got nil")
	}
}

func TestIsNotFoundError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"404", fmt.Errorf("status: 404, body: {}"), true},
		{"500", fmt.Errorf("status: 500, body: {}"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isNotFoundError(c.err); got != c.want {
				t.Errorf("isNotFoundError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestRetryTransport_RetriesOn5xxThenSucceeds(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &retryTransport{base: http.DefaultTransport, maxRetries: 3, baseDelay: time.Millisecond},
	}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestRetryTransport_GivesUpAfterMaxRetries(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &retryTransport{base: http.DefaultTransport, maxRetries: 2, baseDelay: time.Millisecond},
	}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 { // initial attempt + 2 retries
		t.Errorf("attempts = %d, want 3", got)
	}
}
