package dremio

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	dapi "github.com/saltxwater/go-dremio-api-client"
)

var gcsAuthModes = []string{"AUTO", "SERVICE_ACCOUNT_KEYS", "OAUTH2_TOKEN"}

// sourceTypes lists every value getSourceConfig/readSourceConfig actually
// implement. Keep in sync with those functions.
var sourceTypes = []string{"NAS", "MSSQL", "GCS"}

func resourceSource() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceSourceCreate,
		ReadContext:   resourceSourceRead,
		UpdateContext: resourceSourceUpdate,
		DeleteContext: resourceSourceDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Schema: map[string]*schema.Schema{
			"type": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice(sourceTypes, false),
			},
			"name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"description": {
				Type:     schema.TypeString,
				Optional: true,
				Default:  "",
			},
			"path": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"auth_ttl_ms": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  3600000,
			},
			"dataset_refresh_after_ms": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  3600000,
			},
			"dataset_expire_after_ms": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  3600000,
			},
			"names_refresh_ms": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  3600000,
			},
			"update_mode": {
				Type:     schema.TypeString,
				Optional: true,
				Default:  "PREFETCH_QUERIED",
			},
			"acc_refresh_period_ms": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  10800000,
			},
			"acc_grace_period_ms": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  32400000,
			},
			"acc_never_expire": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"acc_never_refresh": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"config": {
				Type:     schema.TypeList,
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"mount_path": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"username": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"hostname": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"port": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"authentication_type": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"fetch_size": {
							Type:     schema.TypeInt,
							Optional: true,
						},
						"database": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"show_only_connection_database": {
							Type:     schema.TypeBool,
							Optional: true,
						},
						"project_id": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"auth_mode": {
							Type:         schema.TypeString,
							Optional:     true,
							Default:      "AUTO",
							ValidateFunc: validation.StringInSlice(gcsAuthModes, false),
						},
						"root_path": {
							Type:     schema.TypeString,
							Optional: true,
							Default:  "/",
						},
						"bucket_whitelist": {
							Type:     schema.TypeList,
							Optional: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
						"async_enabled": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  true,
						},
						"caching_enable": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  true,
						},
						"cache_percent": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      70,
							ValidateFunc: validation.IntBetween(1, 100),
						},
						"client_email": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"client_id": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"private_key_id": {
							Type:     schema.TypeString,
							Optional: true,
						},
					},
				},
			},
			"secure_config": {
				Type:      schema.TypeList,
				Optional:  true,
				Sensitive: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"password": {
							Type:      schema.TypeString,
							Optional:  true,
							Sensitive: true,
						},
						"private_key": {
							Type:      schema.TypeString,
							Optional:  true,
							Sensitive: true,
						},
					},
				},
			},
		},
	}
}

func getSourceMetadataPolicy(d *schema.ResourceData) *dapi.SourceMetadataPolicy {
	return &dapi.SourceMetadataPolicy{
		AuthTTLMs:             d.Get("auth_ttl_ms").(int),
		DatasetRefreshAfterMs: d.Get("dataset_refresh_after_ms").(int),
		DatasetExpireAfterMs:  d.Get("dataset_expire_after_ms").(int),
		NamesRefreshMs:        d.Get("names_refresh_ms").(int),
		DatasetUpdateMode:     d.Get("update_mode").(string),
	}
}

func resourceSourceCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(*dapi.Client)

	config, err := getSourceConfig(d)
	if err != nil {
		return diag.FromErr(err)
	}
	space, err := c.NewSource(&dapi.NewSourceSpec{
		Name:                        d.Get("name").(string),
		Description:                 d.Get("description").(string),
		Type:                        d.Get("type").(string),
		Config:                      config,
		MetadataPolicy:              getSourceMetadataPolicy(d),
		AccelerationRefreshPeriodMs: d.Get("acc_refresh_period_ms").(int),
		AccelerationGracePeriodMs:   d.Get("acc_grace_period_ms").(int),
		AccelerationNeverExpire:     d.Get("acc_never_expire").(bool),
		AccelerationNeverRefresh:    d.Get("acc_never_refresh").(bool),
	})
	if err != nil {
		return diag.FromErr(err)
	}

	d.SetId(space.Id)

	return resourceSourceRead(ctx, d, m)
}

func resourceSourceRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(*dapi.Client)

	sourceId := d.Id()

	source, err := c.GetSource(sourceId)
	if isNotFoundError(err) {
		d.SetId("")
		return nil
	}
	if err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("name", source.Name); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("description", source.Description); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("type", source.Type); err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("path", source.Path); err != nil {
		return diag.FromErr(err)
	}

	config := source.Config.(map[string]interface{})
	if err := readSourceConfig(d, source.Type, config); err != nil {
		return diag.FromErr(err)
	}

	if source.MetadataPolicy != nil {
		if err := d.Set("auth_ttl_ms", source.MetadataPolicy.AuthTTLMs); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("dataset_refresh_after_ms", source.MetadataPolicy.DatasetRefreshAfterMs); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("dataset_expire_after_ms", source.MetadataPolicy.DatasetExpireAfterMs); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("names_refresh_ms", source.MetadataPolicy.NamesRefreshMs); err != nil {
			return diag.FromErr(err)
		}
		if err := d.Set("update_mode", source.MetadataPolicy.DatasetUpdateMode); err != nil {
			return diag.FromErr(err)
		}
	}
	if err := d.Set("acc_refresh_period_ms", source.AccelerationRefreshPeriodMs); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("acc_grace_period_ms", source.AccelerationGracePeriodMs); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("acc_never_expire", source.AccelerationNeverExpire); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("acc_never_refresh", source.AccelerationNeverRefresh); err != nil {
		return diag.FromErr(err)
	}
	return diag.Diagnostics{}
}

func resourceSourceUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(*dapi.Client)

	sourceId := d.Id()

	config, err := getSourceConfig(d)
	if err != nil {
		return diag.FromErr(err)
	}
	_, err = c.UpdateSource(sourceId, &dapi.UpdateSourceSpec{
		Description:                 d.Get("description").(string),
		Config:                      config,
		MetadataPolicy:              getSourceMetadataPolicy(d),
		AccelerationRefreshPeriodMs: d.Get("acc_refresh_period_ms").(int),
		AccelerationGracePeriodMs:   d.Get("acc_grace_period_ms").(int),
		AccelerationNeverExpire:     d.Get("acc_never_expire").(bool),
		AccelerationNeverRefresh:    d.Get("acc_never_refresh").(bool),
	})
	if err != nil {
		return diag.FromErr(err)
	}
	d.Set("last_updated", time.Now().Format(time.RFC850))

	return resourceSourceRead(ctx, d, m)
}

func resourceSourceDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(*dapi.Client)

	// Warning or errors can be collected in a slice type
	var diags diag.Diagnostics

	sourceId := d.Id()

	err := c.DeleteCatalogItem(sourceId)
	if err != nil {
		return diag.FromErr(err)
	}

	d.SetId("")

	return diags
}

func getSourceConfig(d *schema.ResourceData) (interface{}, error) {
	sType := d.Get("type").(string)
	if sType == "NAS" {
		return map[string]interface{}{
			"path": d.Get("config.0.mount_path").(string),
		}, nil
	}
	if sType == "MSSQL" {
		return map[string]interface{}{
			"username":                   d.Get("config.0.username").(string),
			"password":                   d.Get("secure_config.0.password").(string),
			"hostname":                   d.Get("config.0.hostname").(string),
			"port":                       d.Get("config.0.port").(string),
			"authenticationType":         d.Get("config.0.authentication_type").(string),
			"fetchSize":                  d.Get("config.0.fetch_size").(int),
			"database":                   d.Get("config.0.database").(string),
			"showOnlyConnectionDatabase": d.Get("config.0.show_only_connection_database").(bool),
		}, nil
	}
	if sType == "GCS" {
		return map[string]interface{}{
			"projectId":       d.Get("config.0.project_id").(string),
			"authMode":        d.Get("config.0.auth_mode").(string),
			"rootPath":        d.Get("config.0.root_path").(string),
			"bucketWhitelist": interfaceListToStringList(d.Get("config.0.bucket_whitelist").([]interface{})),
			"asyncEnabled":    d.Get("config.0.async_enabled").(bool),
			"cachingEnable":   d.Get("config.0.caching_enable").(bool),
			"cachePercent":    d.Get("config.0.cache_percent").(int),
			"clientEmail":     d.Get("config.0.client_email").(string),
			"clientId":        d.Get("config.0.client_id").(string),
			"privateKeyId":    d.Get("config.0.private_key_id").(string),
			"privateKey":      d.Get("secure_config.0.private_key").(string),
		}, nil
	}
	return nil, fmt.Errorf("unsupported source type %q, must be one of: %s", sType, strings.Join(sourceTypes, ", "))
}

func readSourceConfig(d *schema.ResourceData, sType string, config map[string]interface{}) error {
	if sType == "NAS" {
		err := d.Set("config", []interface{}{
			map[string]interface{}{
				"mount_path": config["path"].(string),
			},
		})
		if err != nil {
			return err
		}
	}
	if sType == "MSSQL" {
		err := d.Set("config", []interface{}{
			map[string]interface{}{
				"username":                      config["username"].(string),
				"hostname":                      config["hostname"].(string),
				"port":                          config["port"].(string),
				"authentication_type":           config["authenticationType"].(string),
				"fetch_size":                    config["fetchSize"].(float64),
				"database":                      config["database"].(string),
				"show_only_connection_database": config["showOnlyConnectionDatabase"].(bool),
			},
		})
		if err != nil {
			return err
		} /*
			err = d.Set("secure_config", []interface{}{
				map[string]interface{}{
					"username": config["username"].(string),
				},
			})
			if err != nil {
				return err
			}*/
	}
	if sType == "GCS" {
		var bucketWhitelist []interface{}
		if raw, ok := config["bucketWhitelist"].([]interface{}); ok {
			bucketWhitelist = raw
		}
		err := d.Set("config", []interface{}{
			map[string]interface{}{
				"project_id":       config["projectId"].(string),
				"auth_mode":        config["authMode"].(string),
				"root_path":        config["rootPath"].(string),
				"bucket_whitelist": bucketWhitelist,
				"async_enabled":    config["asyncEnabled"].(bool),
				"caching_enable":   config["cachingEnable"].(bool),
				"cache_percent":    config["cachePercent"].(float64),
				"client_email":     config["clientEmail"].(string),
				"client_id":        config["clientId"].(string),
				"private_key_id":   config["privateKeyId"].(string),
			},
		})
		if err != nil {
			return err
		}
		// privateKey is a secret field; Dremio never returns it on read, so it
		// is intentionally left out here, same as password for MSSQL above.
	}
	return nil
}
